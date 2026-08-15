// Package coreelectronics implements the suppliers.Provider interface for Core
// Electronics (core-electronics.com.au), an Australian maker-electronics
// retailer. Search results are taken from the product suggestions that the
// server renders into the search page; product details are parsed from the
// server-rendered Magento product pages. No API key is required.
package coreelectronics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const baseURL = "https://core-electronics.com.au"

// Provider implements the suppliers.Provider interface for Core Electronics.
type Provider struct {
	httpClient *http.Client
}

func init() {
	suppliers.Register(NewProvider())
}

func NewProvider() *Provider {
	return NewProviderWithClient(nil)
}

// NewProviderWithClient creates a Core Electronics provider using the given
// HTTP client (used by tests to inject a fake transport). A nil client uses a
// default 20s-timeout client.
func NewProviderWithClient(client *http.Client) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Provider{httpClient: client}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "core-electronics",
		Name:         "Core Electronics",
		BaseURL:      baseURL,
		SupportsAuth: false,
		AuthType:     "scraping",
	}
}

func (p *Provider) IsActive() bool {
	return true
}

func (p *Provider) GetCapabilities() []suppliers.Capability {
	return []suppliers.Capability{
		suppliers.CapBasic,
		suppliers.CapPicture,
		suppliers.CapPrice,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return domain == "core-electronics.com.au" || domain == "www.core-electronics.com.au"
}

// ExtractPartIDFromURL extracts the product slug path from a Core Electronics
// product URL like "https://core-electronics.com.au/bambu-lab-h2c-ams-combo.html".
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	trimmed := strings.Trim(u.Path, "/")
	if trimmed == "" || strings.HasPrefix(trimmed, "search") || strings.HasPrefix(trimmed, "category") || strings.HasPrefix(trimmed, "catalogsearch") {
		return "", false
	}
	if strings.HasSuffix(trimmed, ".html") || strings.Contains(trimmed, "-") {
		return strings.TrimSuffix(trimmed, ".html"), true
	}
	return "", false
}

// SearchByKeyword searches Core Electronics. The main results list is rendered
// client-side, so results come from the product suggestions the server embeds
// in the search page (up to ~6 items, with name, price, image and URL).
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	searchURL := fmt.Sprintf("%s/search/?q=%s", baseURL, urlEscape(keyword))
	doc, err := p.fetchDoc(ctx, searchURL)
	if err != nil {
		return nil, err
	}

	var results []suppliers.SearchResultDTO
	doc.Find("p.menu-pro-cover").Each(func(_ int, s *goquery.Selection) {
		link := s.Find(".product-title a")
		href, ok := link.Attr("href")
		name := strings.TrimSpace(link.Text())
		if !ok || href == "" || name == "" {
			return
		}
		slug, ok := p.ExtractPartIDFromURL(href)
		if !ok {
			return
		}
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "core-electronics",
			ProviderID:      slug,
			Name:            name,
			PreviewImageURL: coreImageURL(s.Find(".product-image img")),
			ProviderURL:     href,
		})
	})

	if len(results) == 0 {
		return nil, fmt.Errorf("no Core Electronics products found for %q", keyword)
	}
	return results, nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if strings.HasSuffix(providerID, ".html") {
		providerID = strings.TrimSuffix(providerID, ".html")
	}
	pageURL := fmt.Sprintf("%s/%s.html", baseURL, strings.Trim(providerID, "/"))

	doc, err := p.fetchDoc(ctx, pageURL)
	if err != nil {
		return nil, err
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey: "core-electronics",
			ProviderID:  providerID,
			Name:        strings.TrimSpace(doc.Find("h1.page-title").Text()),
			ProviderURL: pageURL,
		},
		Notes: coreDescription(doc),
	}

	if sku, ok := doc.Find(`meta[itemprop="sku"]`).Attr("content"); ok && sku != "" {
		detail.ProviderID = sku
		detail.MPN = sku
	}

	if img := coreImageURL(doc.Find(`link[itemprop="image"]`).First()); img != "" {
		detail.PreviewImageURL = img
		detail.Images = append(detail.Images, suppliers.FileDTO{URL: img, Name: detail.ProviderID + ".jpg"})
	}

	if price := corePrice(doc); price != "" {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Core Electronics",
			OrderNumber:     detail.ProviderID,
			ProductURL:      pageURL,
			Currency:        "AUD",
			Price:           price,
			MinimumOrderQty: "1",
			InStock:         coreInStock(doc),
		}
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                price,
			Currency:             "AUD",
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
		detail.VendorInfos = append(detail.VendorInfos, vi)
	}

	detail.Parameters = coreSpecParameters(doc)
	return detail, nil
}

func (p *Provider) fetchDoc(ctx context.Context, pageURL string) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Core Electronics request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Core Electronics returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Core Electronics page: %w", err)
	}
	return doc, nil
}

// coreDescription returns the product description text with Magento PageBuilder
// CSS and markup stripped out.
func coreDescription(doc *goquery.Document) string {
	sel := doc.Find("div.product.attribute.description")
	if sel.Length() == 0 {
		return ""
	}
	html, _ := goquery.OuterHtml(sel)
	html = stripStyleBlocks(html)
	html = regexp.MustCompile(`\[data-pb-style=[^]]*\]`).ReplaceAllString(html, "")
	text := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, " ")
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text)
}

// coreSpecParameters parses the two-column specification tables that Magento
// PageBuilder embeds inside the product description.
func coreSpecParameters(doc *goquery.Document) []suppliers.ParameterDTO {
	var params []suppliers.ParameterDTO
	sel := doc.Find("div.product.attribute.description")
	if sel.Length() == 0 {
		return params
	}
	sel.Find("table tr").Each(func(_ int, tr *goquery.Selection) {
		cells := tr.Find("td")
		if cells.Length() != 2 {
			return
		}
		label := strings.TrimSpace(cells.Eq(0).Text())
		value := strings.TrimSpace(cells.Eq(1).Text())
		if label == "" || value == "" || strings.Contains(label, "Specifications") {
			return
		}
		params = append(params, suppliers.ParameterDTO{
			Name:      label,
			ValueText: value,
			Group:     "Specifications",
		})
	})
	return params
}

func corePrice(doc *goquery.Document) string {
	if sel := doc.Find(".price-wrapper .price").First(); sel.Length() > 0 {
		if t := strings.TrimSpace(sel.Text()); t != "" {
			return normalizePrice(t)
		}
	}
	if sel := doc.Find("div.price-box span.price").First(); sel.Length() > 0 {
		if t := strings.TrimSpace(sel.Text()); t != "" {
			return normalizePrice(t)
		}
	}
	return ""
}

func coreInStock(doc *goquery.Document) bool {
	text := doc.Text()
	lower := strings.ToLower(text)
	if strings.Contains(lower, "out of stock") {
		return false
	}
	return strings.Contains(lower, "in stock")
}

// normalizePrice strips currency symbols and thousands separators, e.g.
// "$3,680.50" -> "3680.50".
func normalizePrice(price string) string {
	price = strings.TrimSpace(price)
	price = strings.ReplaceAll(price, "$", "")
	price = strings.ReplaceAll(price, ",", "")
	return strings.TrimSpace(price)
}

func stripStyleBlocks(s string) string {
	return regexp.MustCompile(`(?s)<style[^>]*>.*?</style>`).ReplaceAllString(s, "")
}

// coreImageURL reads a real image URL, preferring data-amsrc (lazy-loaded)
// attributes over base64 placeholders.
func coreImageURL(s *goquery.Selection) string {
	if s.Length() == 0 {
		return ""
	}
	if src, ok := s.Attr("data-amsrc"); ok && src != "" {
		return src
	}
	if src, ok := s.Attr("href"); ok && src != "" && !strings.HasPrefix(src, "data:") {
		return src
	}
	if src, ok := s.Attr("src"); ok && src != "" && !strings.HasPrefix(src, "data:") {
		return src
	}
	return ""
}

func urlEscape(s string) string {
	return strings.ReplaceAll(s, " ", "+")
}
