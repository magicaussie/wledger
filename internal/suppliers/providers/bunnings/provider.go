package bunnings

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

const (
	baseURL = "https://www.bunnings.com.au"
)

var (
	nextDataRE = regexp.MustCompile(`<script[^>]*id="__NEXT_DATA__"[^>]*>(.*?)</script>`)
	codeRE     = regexp.MustCompile(`_p(\d+)$`)
)

// Provider implements the suppliers.Provider interface for Bunnings Warehouse.
// Product data is extracted from the __NEXT_DATA__ JSON embedded in the page,
// so no API key or OAuth is required.
type Provider struct {
	httpClient *http.Client
}

func init() {
	suppliers.Register(NewProvider())
}

func NewProvider() *Provider {
	return &Provider{
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "bunnings",
		Name:         "Bunnings Warehouse",
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
	return domain == "bunnings.com.au" || domain == "www.bunnings.com.au"
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	m := codeRE.FindStringSubmatch(strings.Split(rawURL, "?")[0])
	if m == nil {
		return "", false
	}
	return m[1], true
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	searchURL := fmt.Sprintf("%s/search/products?q=%s", baseURL, urlQueryEscape(keyword))

	nd, err := p.fetchNextData(ctx, searchURL)
	if err != nil {
		return nil, err
	}

	products, err := nd.searchProducts()
	if err != nil {
		return nil, fmt.Errorf("Bunnings search parse failed: %w", err)
	}

	return p.partsToSearchResults(products), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	routingURL, err := p.resolveProductURL(ctx, providerID)
	if err != nil {
		return nil, err
	}

	nd, err := p.fetchNextData(ctx, baseURL+routingURL)
	if err != nil {
		return nil, err
	}

	detail, err := nd.detailProduct()
	if err != nil {
		return nil, fmt.Errorf("Bunnings detail parse failed: %w", err)
	}
	price, _ := nd.detailPrice()

	return p.productToDetail(detail, price), nil
}

func (p *Provider) fetchNextData(ctx context.Context, pageURL string) (*nextData, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Bunnings request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Bunnings returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Bunnings response: %w", err)
	}

	m := nextDataRE.FindSubmatch(body)
	if m == nil {
		return nil, fmt.Errorf("no __NEXT_DATA__ found in Bunnings page")
	}

	var nd nextData
	if err := json.Unmarshal(m[1], &nd); err != nil {
		return nil, fmt.Errorf("failed to decode __NEXT_DATA__: %w", err)
	}

	return &nd, nil
}

// resolveProductURL finds the product page routing URL for an item code by
// searching Bunnings for the code. The routing URL encodes the product slug.
func (p *Provider) resolveProductURL(ctx context.Context, code string) (string, error) {
	searchURL := fmt.Sprintf("%s/search/products?q=%s", baseURL, urlQueryEscape(code))

	nd, err := p.fetchNextData(ctx, searchURL)
	if err != nil {
		return "", err
	}

	products, err := nd.searchProducts()
	if err != nil {
		return "", fmt.Errorf("Bunnings code search parse failed: %w", err)
	}

	for _, prod := range products {
		if prod.Code == code || prod.ItemNumber == code {
			if prod.RoutingURL != "" {
				return prod.RoutingURL, nil
			}
		}
	}

	return "", fmt.Errorf("no Bunnings product found with code %s", code)
}

func (p *Provider) partsToSearchResults(parts []bunningsProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:         "bunnings",
			ProviderID:          part.Code,
			Name:                part.Name,
			Description:         strings.Join(part.KeySellingPoints, " "),
			Category:            part.Category(),
			Manufacturer:        part.BrandName,
			MPN:                 "",
			PreviewImageURL:     part.ThumbnailImageURL,
			ManufacturingStatus: "",
			ProviderURL:         baseURL + part.RoutingURL,
			Footprint:           "",
		})
	}
	return results
}

func (p *Provider) productToDetail(prod *bunningsDetailProduct, price *bunningsPrice) *suppliers.PartDetailDTO {
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "bunnings",
			ProviderID:      prod.Code,
			Name:            prod.Name,
			Description:     prod.Description(),
			Category:        prod.Category(),
			Manufacturer:    prod.Brand.Name,
			MPN:             prod.ModelNumber(),
			PreviewImageURL: prod.PrimaryImageURL(),
			ProviderURL:     p.detailPageURL(prod),
			Footprint:       "",
		},
		Notes: "",
	}

	for _, img := range prod.imageURLs() {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  img,
			Name: prod.Code + ".jpg",
		})
	}

	vi := suppliers.PurchaseInfoDTO{
		DistributorName: "Bunnings Warehouse",
		OrderNumber:     prod.Code,
		ProductURL:      p.detailPageURL(prod),
		MinimumOrderQty: "1",
		InStock:         prod.availableForDelivery(),
	}

	if price != nil && price.Value > 0 {
		vi.Currency = price.CurrencyISOCode()
		vi.Price = fmt.Sprintf("%.2f", price.Value)
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                fmt.Sprintf("%.2f", price.Value),
			Currency:             price.CurrencyISOCode(),
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
	}
	detail.VendorInfos = append(detail.VendorInfos, vi)

	for _, param := range prod.Parameters() {
		detail.Parameters = append(detail.Parameters, param)
	}

	return detail
}

func (p *Provider) detailPageURL(prod *bunningsDetailProduct) string {
	for _, opt := range prod.BaseOptions {
		if s := opt.Selected.RoutingURL; s != "" {
			return baseURL + s
		}
	}
	if prod.Code != "" {
		return fmt.Sprintf("%s/search/products?q=%s", baseURL, urlQueryEscape(prod.Code))
	}
	return baseURL
}

func urlQueryEscape(s string) string {
	return strings.ReplaceAll(s, " ", "+")
}

// nextData is the root of the __NEXT_DATA__ JSON object.
type nextData struct {
	Props struct {
		PageProps struct {
			DehydratedState struct {
				Queries []struct {
					State struct {
						Data json.RawMessage `json:"data"`
					} `json:"state"`
				} `json:"queries"`
			} `json:"dehydratedState"`
		} `json:"pageProps"`
	} `json:"props"`
}

// searchProducts returns the product list from the search page's query data.
func (nd *nextData) searchProducts() ([]bunningsProduct, error) {
	for _, q := range nd.Props.PageProps.DehydratedState.Queries {
		var probe struct {
			Results []struct {
				Raw bunningsProduct `json:"raw"`
			} `json:"results"`
		}
		if err := json.Unmarshal(q.State.Data, &probe); err != nil {
			continue
		}
		if len(probe.Results) == 0 {
			continue
		}
		products := make([]bunningsProduct, 0, len(probe.Results))
		for _, r := range probe.Results {
			if r.Raw.Code != "" && r.Raw.ObjectType == "Product" {
				products = append(products, r.Raw)
			}
		}
		if len(products) > 0 {
			return products, nil
		}
	}
	return nil, fmt.Errorf("no search results found in page data")
}

// detailProduct returns the full product object from a product page.
func (nd *nextData) detailProduct() (*bunningsDetailProduct, error) {
	for _, q := range nd.Props.PageProps.DehydratedState.Queries {
		var prod bunningsDetailProduct
		if err := json.Unmarshal(q.State.Data, &prod); err != nil {
			continue
		}
		if prod.Code != "" && prod.Name != "" {
			return &prod, nil
		}
	}
	return nil, fmt.Errorf("no product detail found in page data")
}

// detailPrice returns the price block from a product page.
func (nd *nextData) detailPrice() (*bunningsPrice, error) {
	for _, q := range nd.Props.PageProps.DehydratedState.Queries {
		var price bunningsPrice
		if err := json.Unmarshal(q.State.Data, &price); err != nil {
			continue
		}
		if price.FormattedValue != "" {
			return &price, nil
		}
	}
	return nil, fmt.Errorf("no price found in page data")
}

// bunningsProduct is the raw product record from a search result.
type bunningsProduct struct {
	Code              string   `json:"code"`
	ItemNumber        string   `json:"itemnumber"`
	Name              string   `json:"name"`
	Title             string   `json:"title"`
	BrandName         string   `json:"brandname"`
	ObjectType        string   `json:"objecttype"`
	ImageURL          string   `json:"imageurl"`
	ThumbnailImageURL string   `json:"thumbnailimageurl"`
	RoutingURL        string   `json:"productroutingurl"`
	KeySellingPoints  []string `json:"keysellingpoints"`
	SuperCategories   []string `json:"supercategories"`
}

// Category returns the leaf category name, e.g. "Drill Drivers".
func (b *bunningsProduct) Category() string {
	for i := len(b.SuperCategories) - 1; i >= 0; i-- {
		c := b.SuperCategories[i]
		if name := categoryName(c); name != "" {
			return name
		}
	}
	return ""
}

func categoryName(s string) string {
	idx := strings.Index(s, "--")
	if idx > 0 {
		return s[:idx]
	}
	return s
}

type bunningsPrice struct {
	CurrencyISO    string  `json:"currencyIso"`
	FormattedValue string  `json:"formattedValue"`
	Value          float64 `json:"value"`
}

func (b *bunningsPrice) CurrencyISOCode() string {
	if b.CurrencyISO != "" {
		return b.CurrencyISO
	}
	return "AUD"
}

type bunningsImage struct {
	ImageType    string `json:"imageType"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnailUrl"`
}

type bunningsBrand struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type bunningsFeature struct {
	Code          string `json:"code"`
	Name          string `json:"name"`
	FeatureValues []struct {
		Value string `json:"value"`
	} `json:"featureValues"`
}

type bunningsClassification struct {
	Code     string           `json:"code"`
	Name     string           `json:"name"`
	Features []bunningsFeature `json:"features"`
}

type bunningsBaseOption struct {
	Selected struct {
		Code       string `json:"code"`
		RoutingURL string `json:"routingUrl"`
	} `json:"selected"`
}

type bunningsDetailProduct struct {
	Code           string                `json:"code"`
	Name           string                `json:"name"`
	ItemNumber     string                `json:"itemNumber"`
	Brand          bunningsBrand         `json:"brand"`
	Images         []bunningsImage       `json:"images"`
	Feature        struct {
		Description string `json:"description"`
	} `json:"feature"`
	Classifications []bunningsClassification `json:"classifications"`
	BaseOptions     []bunningsBaseOption     `json:"baseOptions"`
}

func (b *bunningsDetailProduct) imageURLs() []string {
	var urls []string
	seen := make(map[string]bool)
	for _, img := range b.Images {
		u := img.URL
		if u == "" {
			u = img.ThumbnailURL
		}
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		urls = append(urls, u)
	}
	return urls
}

func (b *bunningsDetailProduct) PrimaryImageURL() string {
	urls := b.imageURLs()
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
}

func (b *bunningsDetailProduct) Description() string {
	if s := strings.TrimSpace(b.Feature.Description); s != "" {
		return s
	}
	return ""
}

func (b *bunningsDetailProduct) Category() string {
	leaf := ""
	for _, c := range b.Classifications {
		if c.Name != "" {
			leaf = c.Name
		}
	}
	return leaf
}

// ModelNumber returns the model number feature value, if exposed.
func (b *bunningsDetailProduct) ModelNumber() string {
	for _, c := range b.Classifications {
		for _, f := range c.Features {
			if strings.EqualFold(f.Code, "modelNumber") {
				if v := featureValue(f); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// availableForDelivery maps the Bunnings stock flags to a boolean.
func (b *bunningsDetailProduct) availableForDelivery() bool {
	for _, c := range b.Classifications {
		for _, f := range c.Features {
			if strings.EqualFold(f.Code, "bunnings_stock") || strings.EqualFold(f.Code, "stock") {
				return strings.EqualFold(featureValue(f), "in stock")
			}
		}
	}
	return true
}

func (b *bunningsDetailProduct) Parameters() []suppliers.ParameterDTO {
	var params []suppliers.ParameterDTO
	for _, c := range b.Classifications {
		group := c.Name
		if group == "" {
			group = "General"
		}
		for _, f := range c.Features {
			if v := featureValue(f); v != "" {
				params = append(params, suppliers.ParameterDTO{
					Name:      f.Name,
					ValueText: v,
					Group:     group,
				})
			}
		}
	}
	if nd := b.ModelNumber(); nd != "" {
		params = append(params, suppliers.ParameterDTO{
			Name:      "Model Number",
			ValueText: nd,
			Group:     "General",
		})
	}
	return params
}

func featureValue(f bunningsFeature) string {
	if len(f.FeatureValues) > 0 {
		return strings.TrimSpace(f.FeatureValues[0].Value)
	}
	return ""
}