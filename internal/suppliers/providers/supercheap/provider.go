// Package supercheap implements the suppliers.Provider interface for
// Supercheap Auto Australia (supercheapauto.com.au), an Australian automotive
// retailer running on Salesforce Commerce Cloud (Demandware).
//
// Product data is parsed from the public search and product pages. No API key
// or browser automation is required for the current markup.
package supercheap

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	baseURL     = "https://www.supercheapauto.com.au"
	defaultPage = 24

	searchURL = "/search"
)

var (
	// itemNoRE matches the trailing Item No. in a product URL, e.g.
	// /p/century-century-hi-performance-car-battery-55d23l-mf/602508.html
	// or /p/autobacs-japan-titan-heavy-duty-scissors/SPO10272164.html
	// Item numbers may be numeric or alphanumeric.
	itemNoRE = regexp.MustCompile(`/([A-Za-z0-9]+)\.html$`)
	// priceRE matches "$187.99" price strings.
	priceRE = regexp.MustCompile(`[0-9]+\.[0-9]{2}`)
)

// blockSignals are substrings that indicate a challenge/block page rather
// than real product content. Kept deliberately strong to avoid false
// positives from normal SFCC pages.
var blockSignals = []string{
	"verify you are human",
	"attention required",
	"access denied",
	"enable javascript to continue",
	"enable javascript and cookies",
}

// Provider implements the suppliers.Provider interface for Supercheap Auto.
type Provider struct {
	httpClient *http.Client
}

func init() {
	suppliers.Register(NewProvider())
}

// NewProvider creates a Supercheap Auto provider.
func NewProvider() *Provider {
	return NewProviderWithClient(nil)
}

// NewProviderWithClient creates a Supercheap Auto provider using the given
// HTTP client (used by tests to inject a fake transport). A nil client uses
// a default 25s-timeout client.
func NewProviderWithClient(client *http.Client) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	return &Provider{
		httpClient: client,
	}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "supercheap",
		Name:         "Supercheap Auto",
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
	return domain == "supercheapauto.com.au" || domain == "www.supercheapauto.com.au"
}

// SearchCacheTTL and DetailCacheTTL keep scraper traffic low. Prices and
// promotions change, so shorter TTLs than the global default are used.
func (p *Provider) SearchCacheTTL() time.Duration { return 15 * time.Minute }
func (p *Provider) DetailCacheTTL() time.Duration { return 45 * time.Minute }

// ExtractPartIDFromURL extracts the Item No. from a Supercheap product URL,
// e.g. /p/century-century-hi-performance-car-battery-55d23l-mf/602508.html
// -> "602508".
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if u.Host != "supercheapauto.com.au" && u.Host != "www.supercheapauto.com.au" {
		return "", false
	}
	m := itemNoRE.FindStringSubmatch(strings.TrimRight(u.Path, "/"))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// SearchByKeyword searches Supercheap Auto and parses the product grid.
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	u, err := url.Parse(baseURL + searchURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", keyword)
	u.RawQuery = q.Encode()

	doc, finalURL, err := p.fetchDoc(ctx, u.String())
	if err != nil {
		return nil, err
	}
	if err := detectBlock(doc); err != nil {
		return nil, err
	}

	// A search for an exact Item No. (e.g. "602508") redirects directly to the
	// product page. Treat it as a single-result search.
	if itemNoRE.MatchString(finalURL.Path) {
		if detail := p.productFromPDP(doc, finalURL); detail != nil {
			return []suppliers.SearchResultDTO{*detail}, nil
		}
		return nil, fmt.Errorf("no Supercheap products found for %q", keyword)
	}

	results := p.parseGrid(doc)
	if len(results) == 0 {
		return nil, fmt.Errorf("no Supercheap products found for %q", keyword)
	}
	return results, nil
}

// GetDetails fetches a Supercheap product page and parses the rich product
// data from the embedded JSON-LD and the specification tables.
func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if !itemNoRE.MatchString("/" + providerID + ".html") {
		return nil, fmt.Errorf("invalid Supercheap item number %q", providerID)
	}

	// Resolve the canonical product URL by searching for the Item No., which
	// redirects to the product page.
	u, _ := url.Parse(baseURL + searchURL)
	q := u.Query()
	q.Set("q", providerID)
	u.RawQuery = q.Encode()

	doc, finalURL, err := p.fetchDoc(ctx, u.String())
	if err != nil {
		return nil, err
	}
	if err := detectBlock(doc); err != nil {
		return nil, err
	}
	if !itemNoRE.MatchString(finalURL.Path) {
		// The search did not resolve to a single product page; look for a tile.
		for _, r := range p.parseGrid(doc) {
			if r.ProviderID == providerID && r.ProviderURL != "" {
				pu, _ := url.Parse(r.ProviderURL)
				doc, finalURL, err = p.fetchDoc(ctx, r.ProviderURL)
				if err != nil {
					return nil, err
				}
				_ = pu
				break
			}
		}
	}
	if !itemNoRE.MatchString(finalURL.Path) {
		return nil, fmt.Errorf("no Supercheap product page found for item %s", providerID)
	}

	return p.productDetail(ctx, doc, finalURL, providerID)
}

// fetchDoc performs a GET request and returns a goquery document plus the
// final URL after redirects.
func (p *Provider) fetchDoc(ctx context.Context, pageURL string) (*goquery.Document, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-AU,en;q=0.9")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("supercheap request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, nil, fmt.Errorf("supercheap returned status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 20*1024*1024))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse Supercheap page: %w", err)
	}

	final := resp.Request.URL
	if final == nil {
		final, _ = url.Parse(pageURL)
	}
	return doc, final, nil
}

// detectBlock checks whether the returned page looks like a challenge page.
func detectBlock(doc *goquery.Document) error {
	title := strings.ToLower(strings.TrimSpace(doc.Find("title").Text()))
	if strings.Contains(title, "please go to") || strings.Contains(title, "attention required") {
		return fmt.Errorf("supercheap challenge page detected")
	}

	// Strip script/style content: SFCC embeds endpoint names like
	// "RateLimiter-HideCaptcha" in page JS config, which would otherwise
	// trigger a false positive.
	var text strings.Builder
	doc.Find("body").Each(func(_ int, s *goquery.Selection) {
		s.Clone().Find("script, style, noscript, template").Remove()
		text.WriteString(s.Text())
	})
	body := strings.ToLower(text.String())
	for _, sig := range blockSignals {
		if strings.Contains(body, sig) {
			return fmt.Errorf("supercheap challenge page detected")
		}
	}
	return nil
}

// parseGrid parses product cards from a search/category page.
func (p *Provider) parseGrid(doc *goquery.Document) []suppliers.SearchResultDTO {
	var results []suppliers.SearchResultDTO

	doc.Find("ul#search-result-items li.grid-tile, ul.search-result-items li.grid-tile").Each(func(_ int, s *goquery.Selection) {
		r, ok := p.parseTile(s)
		if ok {
			results = append(results, r)
		}
	})

	return results
}

func (p *Provider) parseTile(s *goquery.Selection) (suppliers.SearchResultDTO, bool) {
	link := s.Find("a.name-link").First()
	if link.Length() == 0 {
		link = s.Find("a.thumb-link").First()
	}
	href, _ := link.Attr("href")

	productURL := absURL(href)
	itemNo := extractItemNo(productURL)

	// Fall back to the gtm-product-impression item_id if the URL has no item no.
	if itemNo == "" {
		if hid, ok := s.Find("input.gtm-product-impression").Attr("value"); ok {
			var imp struct {
				ItemID string `json:"item_id"`
			}
			if json.Unmarshal([]byte(htmlUnescape(hid)), &imp) == nil && imp.ItemID != "" {
				itemNo = imp.ItemID
			}
		}
	}
	if itemNo == "" {
		return suppliers.SearchResultDTO{}, false
	}

	name := strings.TrimSpace(link.Text())
	if name == "" {
		name = strings.TrimSpace(s.Find("a.thumb-link").AttrOr("title", ""))
	}
	brand := strings.TrimSpace(s.Find(".brand-name").Text())

	imgSel := s.Find(".product-image img").First()
	img, _ := imgSel.Attr("data-src")
	if img == "" || strings.HasPrefix(img, "data:") {
		img, _ = imgSel.Attr("src")
	}
	img = htmlUnescape(absURL(img))

	salesPrice := priceFromText(s.Find(".product-sales-price .the-price").First().Text())
	standardPrice := priceFromText(s.Find(".product-standard-price .the-price").First().Text())

	rating := 0.0
	if widthStr, ok := s.Find(".bv-rating-stars-on").First().Attr("style"); ok {
		if m := regexp.MustCompile(`width:\s*([0-9.]+)%`).FindStringSubmatch(widthStr); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				rating = v / 100 * 5
			}
		}
	}
	reviewCount := 0
	if cnt := strings.TrimSpace(s.Find(".bv-rating-ratio-count").First().Text()); cnt != "" {
		if m := regexp.MustCompile(`([0-9]+)`).FindStringSubmatch(cnt); m != nil {
			reviewCount, _ = strconv.Atoi(m[1])
		}
	}

	var description strings.Builder
	if salesPrice != "" {
		description.WriteString("Price " + salesPrice)
	}
	if standardPrice != "" && standardPrice != salesPrice {
		description.WriteString(" Was " + standardPrice)
	}
	if rating > 0 {
		description.WriteString(fmt.Sprintf(" Rating %.1f", rating))
	}
	if reviewCount > 0 {
		description.WriteString(fmt.Sprintf(" (%d reviews)", reviewCount))
	}
	description.WriteString(availabilityText(s))

	return suppliers.SearchResultDTO{
		ProviderKey:     "supercheap",
		ProviderID:      itemNo,
		Name:            name,
		Description:     strings.TrimSpace(description.String()),
		Manufacturer:    brand,
		MPN:             itemNo,
		PreviewImageURL: img,
		ProviderURL:     productURL,
		Footprint:       "",
	}, true
}

// productFromPDP extracts a single search result from a product detail page.
func (p *Provider) productFromPDP(doc *goquery.Document, productURL *url.URL) *suppliers.SearchResultDTO {
	itemNo := extractItemNo(productURL.String())
	if itemNo == "" {
		return nil
	}

	info := jsonLdProduct(doc)
	name := info.Name
	if name == "" {
		name = strings.TrimSpace(doc.Find("h1.product-name, h1").First().Text())
	}
	brand := info.Brand.Name
	if brand == "" {
		brand = strings.TrimSpace(doc.Find(".brand-name").First().Text())
	}
	img := ""
	if len(info.Image) > 0 {
		img = absURL(htmlUnescape(info.Image[0]))
	}
	if img == "" {
		imgSel := doc.Find(".product-image img, .product-primary-image img").First()
		img, _ = imgSel.Attr("data-src")
		if img == "" || strings.HasPrefix(img, "data:") {
			img, _ = imgSel.Attr("src")
		}
		if img != "" {
			img = absURL(htmlUnescape(img))
		}
	}

	var desc strings.Builder
	if len(info.Offers.PriceSpecification) > 0 {
		desc.WriteString("Price " + info.Offers.PriceSpecification[0].Price)
	}

	return &suppliers.SearchResultDTO{
		ProviderKey:     "supercheap",
		ProviderID:      itemNo,
		Name:            name,
		Description:     strings.TrimSpace(desc.String()),
		Manufacturer:    brand,
		MPN:             itemNo,
		PreviewImageURL: img,
		ProviderURL:     productURL.String(),
	}
}

// productDetail parses a product page into a PartDetailDTO.
func (p *Provider) productDetail(ctx context.Context, doc *goquery.Document, productURL *url.URL, providerID string) (*suppliers.PartDetailDTO, error) {
	info := jsonLdProduct(doc)

	itemNo := providerID
	if itemNo == "" {
		itemNo = extractItemNo(productURL.String())
	}
	name := info.Name
	if name == "" {
		name = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	brand := info.Brand.Name
	if brand == "" {
		brand = strings.TrimSpace(doc.Find(".brand-name").First().Text())
	}
	if brand == "" {
		if m := regexp.MustCompile(`data-brand="([^"]+)"`).FindStringSubmatch(doc.Text()); m != nil {
			brand = m[1]
		}
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "supercheap",
			ProviderID:      itemNo,
			Name:            name,
			Description:     strings.TrimSpace(info.Description),
			Category:        info.Category,
			Manufacturer:    brand,
			MPN:             itemNo,
			PreviewImageURL: "",
			ProviderURL:     productURL.String(),
		},
	}

	// Images
	seen := make(map[string]bool)
	for _, u := range info.Image {
		u = absURL(htmlUnescape(u))
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		detail.Images = append(detail.Images, suppliers.FileDTO{URL: u, Name: itemNo + ".jpg"})
		if detail.PreviewImageURL == "" {
			detail.PreviewImageURL = u
		}
	}
	if detail.PreviewImageURL == "" {
		imgSel := doc.Find(".product-image img, .product-primary-image img").First()
		src, _ := imgSel.Attr("data-src")
		if src == "" || strings.HasPrefix(src, "data:") {
			src, _ = imgSel.Attr("src")
		}
		if src != "" {
			detail.PreviewImageURL = absURL(htmlUnescape(src))
		}
	}

	// Price
	price := ""
	currency := "AUD"
	if len(info.Offers.PriceSpecification) > 0 {
		ps := info.Offers.PriceSpecification[0]
		price = ps.Price
		if ps.PriceCurrency != "" {
			currency = ps.PriceCurrency
		}
	}
	if price == "" {
		price = priceFromText(doc.Find(".product-sales-price .the-price, .product-pricing .the-price").First().Text())
	}
	if price == "" {
		price = priceFromText(doc.Find(".price-sales .promo-price, .product-price .price-sales").First().Text())
	}

	// Availability
	inStock := strings.Contains(strings.ToLower(info.Offers.Availability), "instock") ||
		strings.Contains(strings.ToLower(info.Offers.Availability), "in_stock")
	if !inStock {
		availText := strings.ToLower(doc.Find(".product-availability, .availability, .stock-message").First().Text())
		inStock = strings.Contains(availText, "in stock") || strings.Contains(availText, "available")
	}

	vi := suppliers.PurchaseInfoDTO{
		DistributorName: "Supercheap Auto",
		OrderNumber:     itemNo,
		ProductURL:      productURL.String(),
		Currency:        currency,
		MinimumOrderQty: "1",
		InStock:         inStock,
	}
	if price != "" {
		vi.Price = price
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                price,
			Currency:             currency,
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
	}
	detail.VendorInfos = append(detail.VendorInfos, vi)

	// Technical specifications
	for _, spec := range specTable(doc) {
		detail.Parameters = append(detail.Parameters, spec)
	}
	// JSON-LD offers an aggregate rating
	if info.AggregateRating.RatingValue > 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Rating",
			ValueText: fmt.Sprintf("%.1f / 5 (%d reviews)", info.AggregateRating.RatingValue, info.AggregateRating.ReviewCount),
			Group:     "Ratings",
		})
	}
	// Features
	for _, f := range features(doc) {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Feature",
			ValueText: f,
			Group:     "Features",
		})
	}

	return detail, nil
}

// jsonLdProduct extracts the schema.org Product JSON-LD from a product page.
func jsonLdProduct(doc *goquery.Document) ldProduct {
	var info ldProduct
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		if info.Name != "" {
			return
		}
		var data ldProduct
		if err := json.Unmarshal([]byte(s.Text()), &data); err != nil {
			return
		}
		if data.Name != "" {
			info = data
		}
	})
	return info
}

type ldProduct struct {
	Context string `json:"@context"`
	Type    string `json:"@type"`
	Name    string `json:"name"`
	Image   []string `json:"image"`
	Description string `json:"description"`
	Category string `json:"category"`
	Brand   struct {
		Name string `json:"name"`
	} `json:"brand"`
	Offers struct {
		PriceSpecification []struct {
			Price         string `json:"price"`
			PriceCurrency string `json:"priceCurrency"`
		} `json:"priceSpecification"`
		Availability string `json:"availability"`
	} `json:"offers"`
	AggregateRating struct {
		RatingValue  float64 `json:"ratingValue"`
		ReviewCount  int     `json:"reviewCount"`
	} `json:"aggregateRating"`
	URL string `json:"url"`
}

// specTable parses the technical specification table on a product page.
func specTable(doc *goquery.Document) []suppliers.ParameterDTO {
	var params []suppliers.ParameterDTO
	doc.Find("table.table-scroll tr").Each(func(_ int, tr *goquery.Selection) {
		name := strings.TrimSpace(tr.Find("th[scope='row']").Text())
		if name == "" {
			return
		}
		value := strings.TrimSpace(tr.Find("td").First().Text())
		if value == "" {
			return
		}
		params = append(params, suppliers.ParameterDTO{
			Name:      name,
			ValueText: value,
			Group:     "Specifications",
		})
	})
	return params
}

// features parses the feature bullets on a product page.
func features(doc *goquery.Document) []string {
	var out []string
	doc.Find("#product-features ul li, .product-features ul li").Each(func(_ int, s *goquery.Selection) {
		txt := strings.TrimSpace(s.Text())
		if txt != "" {
			out = append(out, txt)
		}
	})
	return out
}

// availabilityText summarises the delivery/pickup states shown on a tile.
func availabilityText(s *goquery.Selection) string {
	var parts []string
	parts = append(parts, strings.TrimSpace(s.Find(".no-home-delivery span, .home-delivery span").Text()))
	parts = append(parts, strings.TrimSpace(s.Find(".customer-order-only span, .in-store-pickup span, .store-inventory span").Text()))
	var kept []string
	for _, pt := range parts {
		if pt != "" && pt != "Add To Cart" {
			kept = append(kept, pt)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return " | " + strings.Join(kept, " | ")
}

// extractItemNo pulls the trailing Item No. from a Supercheap product URL.
func extractItemNo(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	m := itemNoRE.FindStringSubmatch(strings.TrimRight(u.Path, "/"))
	if m == nil {
		return ""
	}
	return m[1]
}

// absURL resolves a possibly-relative URL against the Supercheap base URL.
func absURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.IsAbs() {
		return u.String()
	}
	base, _ := url.Parse(baseURL)
	return base.ResolveReference(u).String()
}

// priceFromText extracts a price like "$187.99" from raw text.
func priceFromText(text string) string {
	m := priceRE.FindString(text)
	if m == "" {
		return ""
	}
	return "$" + m
}

func htmlUnescape(s string) string {
	if s == "" {
		return ""
	}
	return html.UnescapeString(s)
}
