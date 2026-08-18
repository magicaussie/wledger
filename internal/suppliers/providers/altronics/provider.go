// Package altronics implements the suppliers.Provider interface for Altronics
// (altronics.com.au), an Australian electronics retailer. Product data is
// scraped from server-rendered nopCommerce pages, so no API key is required.
package altronics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const baseURL = "https://www.altronics.com.au"

// Provider implements the suppliers.Provider interface for Altronics.
type Provider struct {
	httpClient *http.Client
}

func init() {
	suppliers.Register(NewProvider())
}

func NewProvider() *Provider {
	return NewProviderWithClient(nil)
}

// NewProviderWithClient creates an Altronics provider using the given HTTP
// client (used by tests to inject a fake transport). A nil client uses a
// default 20s-timeout client.
func NewProviderWithClient(client *http.Client) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Provider{httpClient: client}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "altronics",
		Name:         "Altronics",
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
	return domain == "altronics.com.au" || domain == "www.altronics.com.au"
}

// ExtractPartIDFromURL extracts the Altronics SKU from a product URL. Altronics
// product slugs always start with the SKU, e.g. "/product/z1621a-5k-10k-ldr".
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	path := strings.Split(rawURL, "?")[0]
	idx := strings.Index(path, "/product/")
	if idx < 0 {
		return "", false
	}
	slug := strings.TrimPrefix(path[idx:], "/product/")
	if slug == "" {
		return "", false
	}
	sku := strings.ToUpper(strings.SplitN(slug, "-", 2)[0])
	if sku == "" {
		return "", false
	}
	return sku, true
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	searchURL := fmt.Sprintf("%s/search?q=%s", baseURL, urlEscape(keyword))
	doc, err := p.fetchDoc(ctx, searchURL)
	if err != nil {
		return nil, err
	}

	var results []suppliers.SearchResultDTO
	doc.Find("div.product-item").Each(func(_ int, s *goquery.Selection) {
		sku := strings.TrimSpace(s.Find("div.sku").Text())
		title := s.Find("h2.product-title a")
		name := strings.TrimSpace(title.Text())
		href, _ := title.Attr("href")
		if sku == "" || name == "" || href == "" {
			return
		}

		result := suppliers.SearchResultDTO{
			ProviderKey:     "altronics",
			ProviderID:      sku,
			Name:            name,
			Description:     strings.TrimSpace(s.Find("div.description").Text()),
			PreviewImageURL: imageURL(s.Find("div.picture img")),
			ProviderURL:     baseURL + href,
		}
		results = append(results, result)
	})

	if len(results) == 0 {
		return nil, fmt.Errorf("no Altronics products found for %q", keyword)
	}
	return results, nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	routingURL, err := p.resolveProductURL(ctx, providerID)
	if err != nil {
		return nil, err
	}

	doc, err := p.fetchDoc(ctx, baseURL+routingURL)
	if err != nil {
		return nil, err
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey: "altronics",
			ProviderID:  strings.ToUpper(providerID),
			Name:        strings.TrimSpace(doc.Find("div.product-name h1").First().Text()),
			Category:    categoryFromBreadcrumb(doc),
			Description: productDescription(doc),
			Manufacturer: manufacturer(doc),
			MPN:         mpn(doc),
			ProviderURL: baseURL + routingURL,
		},
		Notes: description(doc),
	}

	if img := imageURL(doc.Find("div.picture img")); img != "" {
		detail.PreviewImageURL = img
		detail.Images = append(detail.Images, suppliers.FileDTO{URL: img, Name: detail.ProviderID + ".jpg"})
	}

	if price := strings.TrimSpace(doc.Find("span.now-price").First().Text()); price != "" {
		prices := parsePrice(price)
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Altronics",
			OrderNumber:     detail.ProviderID,
			ProductURL:      baseURL + routingURL,
			Currency:        "AUD",
			Price:           prices,
			MinimumOrderQty: "1",
		}
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                prices,
			Currency:             "AUD",
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
		detail.VendorInfos = append(detail.VendorInfos, vi)
	}

	detail.Parameters = specParameters(doc)
	return detail, nil
}

// resolveProductURL finds the product page URL for a SKU by searching Altronics
// for the SKU, matching the Bunnings pattern of resolving IDs to routing URLs.
func (p *Provider) resolveProductURL(ctx context.Context, sku string) (string, error) {
	searchURL := fmt.Sprintf("%s/search?q=%s", baseURL, urlEscape(sku))
	doc, err := p.fetchDoc(ctx, searchURL)
	if err != nil {
		return "", err
	}

	want := strings.ToUpper(sku)
	var found string
	doc.Find("div.product-item").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if strings.EqualFold(strings.TrimSpace(s.Find("div.sku").Text()), want) {
			if href, ok := s.Find("h2.product-title a").Attr("href"); ok {
				found = href
				return false
			}
		}
		return true
	})
	if found == "" {
		return "", fmt.Errorf("no Altronics product found with SKU %s", sku)
	}
	return found, nil
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
		return nil, fmt.Errorf("Altronics request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Altronics returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Altronics page: %w", err)
	}
	return doc, nil
}

// specParameters parses the "prod-specs" block on a product page, where each
// <p> is formatted as "Label: Value".
func specParameters(doc *goquery.Document) []suppliers.ParameterDTO {
	var params []suppliers.ParameterDTO
	doc.Find("div.prod-specs p").Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		label, value, ok := strings.Cut(text, ":")
		if !ok {
			return
		}
		label = strings.TrimSpace(label)
		value = strings.TrimSpace(value)
		if label == "" || value == "" {
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

func categoryFromBreadcrumb(doc *goquery.Document) string {
	var cats []string
	doc.Find("div.breadcrumb a[href*='/category/'] span").Each(func(_ int, s *goquery.Selection) {
		if name := strings.TrimSpace(s.Text()); name != "" && !strings.EqualFold(name, "Home") {
			cats = append(cats, name)
		}
	})
	if len(cats) == 0 {
		return ""
	}
	return cats[len(cats)-1]
}

func description(doc *goquery.Document) string {
	text := strings.TrimSpace(doc.Find("div.full-description").Text())
	if idx := strings.Index(text, "Specifications:"); idx > 0 {
		text = strings.TrimSpace(text[:idx])
	}
	return text
}

// productDescription extracts a useful description from the product page.
// The visible "full-description" block can be hidden until a tab is opened, so
// fall back to the JSON-LD description or the meta description.
func productDescription(doc *goquery.Document) string {
	if d := description(doc); d != "" {
		return d
	}

	// JSON-LD Product block.
	var ld struct {
		Description string `json:"description"`
	}
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		if ld.Description != "" {
			return
		}
		var data struct {
			Description string `json:"description"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(s.Text())), &data) == nil && data.Description != "" {
			ld.Description = data.Description
		}
	})
	if ld.Description != "" {
		return strings.TrimSpace(ld.Description)
	}

	if m, ok := doc.Find(`meta[name="description"]`).Attr("content"); ok {
		return strings.TrimSpace(m)
	}
	return ""
}

// manufacturer extracts the product brand from the JSON-LD block. Altronics
// pages rarely expose an explicit brand element; when present, brand may be an
// object {"name": ...} or an empty array [].
func manufacturer(doc *goquery.Document) string {
	var brand string
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		if brand != "" {
			return
		}
		var data struct {
			Brand json.RawMessage `json:"brand"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(s.Text())), &data) != nil {
			return
		}
		if len(data.Brand) == 0 || string(data.Brand) == "[]" || string(data.Brand) == "null" {
			return
		}
		var obj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(data.Brand, &obj) == nil && obj.Name != "" {
			brand = obj.Name
		}
	})
	return brand
}

// mpn extracts a manufacturer part number. Altronics pages expose the SKU and
// optionally a real manufacturer part number; use the SKU as a fallback.
func mpn(doc *goquery.Document) string {
	sku := strings.TrimSpace(doc.Find("div.sku").First().Text())
	sku = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(sku), "SKU:"))
	return sku
}

func imageURL(s *goquery.Selection) string {
	if s.Length() == 0 {
		return ""
	}
	if src, ok := s.Attr("src"); ok && src != "" && !strings.HasPrefix(src, "data:") {
		return src
	}
	if src, ok := s.Attr("data-src"); ok {
		return src
	}
	return ""
}

// parsePrice normalises an Altronics price string like "$2.90" to "2.90".
func parsePrice(price string) string {
	price = strings.TrimSpace(price)
	price = strings.ReplaceAll(price, "$", "")
	price = strings.ReplaceAll(price, ",", "")
	if f, err := strconv.ParseFloat(price, 64); err == nil {
		return fmt.Sprintf("%.2f", f)
	}
	return price
}

func urlEscape(s string) string {
	return strings.ReplaceAll(s, " ", "+")
}
