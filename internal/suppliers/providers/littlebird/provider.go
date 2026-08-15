// Package littlebird implements the suppliers.Provider interface for Little
// Bird Electronics (littlebird.com.au), an Australian 3D-printing and maker
// electronics retailer. Both search results and product pages expose full
// JSON-LD structured data server-side, so no API key is required.
package littlebird

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const baseURL = "https://littlebird.com.au"

var ldJSONRE = regexp.MustCompile(`(?s)<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>`)

// Provider implements the suppliers.Provider interface for Little Bird.
type Provider struct {
	httpClient *http.Client
}

func init() {
	suppliers.Register(NewProvider())
}

func NewProvider() *Provider {
	return NewProviderWithClient(nil)
}

// NewProviderWithClient creates a Little Bird provider using the given HTTP
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
		Key:          "littlebird",
		Name:         "Little Bird Electronics",
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
	return domain == "littlebird.com.au" || domain == "www.littlebird.com.au" ||
		domain == "littlebirdelectronics.com.au" || domain == "www.littlebirdelectronics.com.au"
}

// ExtractPartIDFromURL extracts the Shopify handle from a product URL like
// "https://littlebird.com.au/products/silk-like-pla-filament-1-75mm-1kg-roll-pink".
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	path := strings.Split(strings.Split(rawURL, "?")[0], "#")[0]
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "products" {
		return "", false
	}
	handle := parts[len(parts)-1]
	if handle == "" {
		return "", false
	}
	return handle, true
}

// SearchByKeyword searches Little Bird and parses the JSON-LD ItemList that the
// server renders into the search page. Each search returns up to 20 products.
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	searchURL := fmt.Sprintf("%s/search?q=%s&type=product", baseURL, urlEscape(keyword))
	body, err := p.fetch(ctx, searchURL)
	if err != nil {
		return nil, err
	}

	list, err := parseItemList(body)
	if err != nil {
		return nil, fmt.Errorf("Little Bird search parse failed: %w", err)
	}

	var results []suppliers.SearchResultDTO
	for _, item := range list.ProductItems() {
		p := item.Product
		if p.SKU == "" || p.Name == "" {
			continue
		}
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "littlebird",
			ProviderID:      p.SKU,
			Name:            p.Name,
			Description:     p.Description,
			Manufacturer:    p.Brand.Name,
			PreviewImageURL: p.FirstImage(),
			ProviderURL:     p.URL,
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no Little Bird products found for %q", keyword)
	}
	return results, nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	productURL, err := p.resolveProductURL(ctx, providerID)
	if err != nil {
		return nil, err
	}

	body, err := p.fetch(ctx, productURL)
	if err != nil {
		return nil, err
	}

	prod, err := parseProduct(body)
	if err != nil {
		return nil, fmt.Errorf("Little Bird detail parse failed: %w", err)
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "littlebird",
			ProviderID:      prod.SKU,
			Name:            prod.Name,
			Description:     prod.Description,
			Manufacturer:    prod.Brand.Name,
			PreviewImageURL: prod.FirstImage(),
			ProviderURL:     prod.URL,
		},
		Notes: prod.Description,
	}

	for _, img := range prod.Images {
		detail.Images = append(detail.Images, suppliers.FileDTO{URL: img, Name: prod.SKU + ".jpg"})
	}

	if len(prod.Offers) > 0 {
		offer := prod.Offers[0]
		price := fmt.Sprintf("%.2f", offer.Price)
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Little Bird Electronics",
			OrderNumber:     prod.SKU,
			ProductURL:      prod.URL,
			Currency:        offer.Currency,
			Price:           price,
			MinimumOrderQty: "1",
			InStock:         offer.InStock(),
		}
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                price,
			Currency:             offer.Currency,
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
		detail.VendorInfos = append(detail.VendorInfos, vi)
	}

	if prod.CountryOfOrigin != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Country of Origin",
			ValueText: prod.CountryOfOrigin,
			Group:     "General",
		})
	}
	return detail, nil
}

// resolveProductURL finds a product page URL for a SKU by searching Little Bird
// for the SKU and matching the exact structured-data SKU.
func (p *Provider) resolveProductURL(ctx context.Context, sku string) (string, error) {
	searchURL := fmt.Sprintf("%s/search?q=%s&type=product", baseURL, urlEscape(sku))
	body, err := p.fetch(ctx, searchURL)
	if err != nil {
		return "", err
	}

	list, err := parseItemList(body)
	if err != nil {
		return "", fmt.Errorf("Little Bird SKU search parse failed: %w", err)
	}

	want := strings.ToUpper(sku)
	for _, item := range list.ProductItems() {
		if strings.EqualFold(item.Product.SKU, want) {
			if item.Product.URL != "" {
				return item.Product.URL, nil
			}
		}
	}
	return "", fmt.Errorf("no Little Bird product found with SKU %s", sku)
}

func (p *Provider) fetch(ctx context.Context, pageURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Little Bird request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("Little Bird returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return io.ReadAll(resp.Body)
}

// parseItemList decodes the JSON-LD ItemList block from a search page.
func parseItemList(body []byte) (*itemList, error) {
	for _, m := range ldJSONRE.FindAllSubmatch(body, -1) {
		var list itemList
		if err := json.Unmarshal(m[1], &list); err != nil {
			continue
		}
		if list.Type == "ItemList" || list.NumberOfItems > 0 {
			return &list, nil
		}
	}
	return nil, fmt.Errorf("no ItemList found in Little Bird search page")
}

// parseProduct decodes the JSON-LD Product block from a product page.
func parseProduct(body []byte) (*product, error) {
	for _, m := range ldJSONRE.FindAllSubmatch(body, -1) {
		var prod product
		if err := json.Unmarshal(m[1], &prod); err != nil {
			continue
		}
		if prod.Type == "Product" && prod.Name != "" {
			return &prod, nil
		}
	}
	return nil, fmt.Errorf("no Product found in Little Bird product page")
}

type itemList struct {
	Type          string     `json:"@type"`
	NumberOfItems int        `json:"numberOfItems"`
	Elements      []listItem `json:"itemListElement"`
}

type listItem struct {
	Position int     `json:"position"`
	Product  product `json:"item"`
}

func (l *itemList) ProductItems() []listItem {
	var items []listItem
	for _, el := range l.Elements {
		if el.Product.Name != "" || el.Product.SKU != "" {
			items = append(items, el)
		}
	}
	return items
}

type product struct {
	Type            string
	Name            string
	URL             string
	Description     string
	SKU             string
	Brand           brand
	Manufacturer    string
	CountryOfOrigin string
	Images          []string
	Offers          []offer
}

// UnmarshalJSON tolerates the shape variance in Little Bird's JSON-LD: image
// may be a single URL or an array, brand a string or object, and offers a
// single object or an array.
func (p *product) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type            string          `json:"@type"`
		Name            string          `json:"name"`
		URL             string          `json:"url"`
		Description     string          `json:"description"`
		SKU             string          `json:"sku"`
		Brand           json.RawMessage `json:"brand"`
		Manufacturer    json.RawMessage `json:"manufacturer"`
		CountryOfOrigin json.RawMessage `json:"countryOfOrigin"`
		Image           json.RawMessage `json:"image"`
		Offers          json.RawMessage `json:"offers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	p.Type = raw.Type
	p.Name = raw.Name
	p.URL = raw.URL
	p.Description = raw.Description
	p.SKU = raw.SKU

	switch {
	case len(raw.CountryOfOrigin) == 0:
	case raw.CountryOfOrigin[0] == '"':
		var name string
		if json.Unmarshal(raw.CountryOfOrigin, &name) == nil {
			p.CountryOfOrigin = name
		}
	default:
		var c struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw.CountryOfOrigin, &c) == nil {
			p.CountryOfOrigin = c.Name
		}
	}

	switch {
	case len(raw.Brand) == 0:
	case raw.Brand[0] == '"':
		var name string
		if json.Unmarshal(raw.Brand, &name) == nil {
			p.Brand.Name = name
		}
	default:
		var b brand
		if json.Unmarshal(raw.Brand, &b) == nil {
			p.Brand = b
		}
	}

	switch {
	case len(raw.Manufacturer) == 0:
	case raw.Manufacturer[0] == '"':
		var name string
		if json.Unmarshal(raw.Manufacturer, &name) == nil {
			p.Manufacturer = name
		}
	default:
		var m struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw.Manufacturer, &m) == nil {
			p.Manufacturer = m.Name
		}
	}

	switch {
	case len(raw.Image) == 0:
	case raw.Image[0] == '"':
		var img string
		if json.Unmarshal(raw.Image, &img) == nil {
			p.Images = []string{img}
		}
	default:
		var imgs []string
		if json.Unmarshal(raw.Image, &imgs) == nil {
			p.Images = imgs
		}
	}

	if len(raw.Offers) == 0 {
		return nil
	}
	if raw.Offers[0] == '[' {
		var offers []offer
		if json.Unmarshal(raw.Offers, &offers) == nil {
			p.Offers = offers
		}
		return nil
	}
	var single offer
	if json.Unmarshal(raw.Offers, &single) == nil {
		p.Offers = []offer{single}
	}
	return nil
}

type brand struct {
	Name string `json:"name"`
}

func (p *product) FirstImage() string {
	if len(p.Images) == 0 {
		return ""
	}
	return p.Images[0]
}

type offer struct {
	Price        float64
	Currency     string
	Availability string
}

func (o *offer) InStock() bool {
	return strings.Contains(strings.ToLower(o.Availability), "instock")
}

// UnmarshalJSON handles both plain Offer objects and AggregateOffer objects
// (which expose lowPrice/highPrice instead of price).
func (o *offer) UnmarshalJSON(data []byte) error {
	var raw struct {
		Price        *float64 `json:"price"`
		LowPrice     *float64 `json:"lowPrice"`
		HighPrice    *float64 `json:"highPrice"`
		Currency     string   `json:"priceCurrency"`
		Availability string   `json:"availability"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch {
	case raw.Price != nil:
		o.Price = *raw.Price
	case raw.LowPrice != nil:
		o.Price = *raw.LowPrice
	case raw.HighPrice != nil:
		o.Price = *raw.HighPrice
	}
	o.Currency = raw.Currency
	o.Availability = raw.Availability
	return nil
}

func urlEscape(s string) string {
	return strings.ReplaceAll(s, " ", "+")
}
