// Package officeworks implements the suppliers.Provider interface for
// Officeworks (officeworks.com.au), an Australian office-supplies retailer.
//
// Officeworks uses Algolia for its public catalogue search, exposing a
// search-only Application ID and API key to the storefront. This provider
// calls the Algolia search API directly (no browser scraping required).
package officeworks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	defaultBaseURL    = "https://www.officeworks.com.au"
	algoliaEndpoint   = "https://k535caawve-dsn.algolia.net/1/indexes/*/queries"
	defaultAppID      = "K535CAAWVE"
	defaultSearchKey  = "8a831febe0110932cfa06ff0e2024b4f"
	defaultIndexName  = "prod-product-wc-bestmatch-personal"
	algoliaSearchPath = "/1/indexes/*/queries"

	capURLPrefix = "https://www.officeworks.com.au/shop/officeworks/p/"
)

// Provider implements the suppliers.Provider interface for Officeworks via Algolia.
type Provider struct {
	httpClient *http.Client
	appID      string
	searchKey  string
	indexName  string
}

func init() {
	suppliers.Register(NewProvider())
}

// NewProvider creates an Officeworks provider with the known public Algolia
// credentials. These can be overridden via environment variables for testing
// or if the frontend changes.
func NewProvider() *Provider {
	appID := getenv("OFFICEWORKS_ALGOLIA_APP_ID", defaultAppID)
	searchKey := getenv("OFFICEWORKS_ALGOLIA_SEARCH_KEY", defaultSearchKey)
	indexName := getenv("OFFICEWORKS_ALGOLIA_INDEX", defaultIndexName)

	return NewProviderWithClient(nil, appID, searchKey, indexName)
}

// NewProviderWithClient creates an Officeworks provider using the given HTTP
// client (used by tests to inject a fake transport). A nil client uses a
// default 20s-timeout client.
func NewProviderWithClient(client *http.Client, appID, searchKey, indexName string) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Provider{
		httpClient: client,
		appID:      appID,
		searchKey:  searchKey,
		indexName:  indexName,
	}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "officeworks",
		Name:         "Officeworks",
		BaseURL:      defaultBaseURL,
		SupportsAuth: false,
		AuthType:     "api_key",
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
	return domain == "officeworks.com.au" || domain == "www.officeworks.com.au"
}

// ExtractPartIDFromURL extracts the Officeworks SKU from a product URL.
// Officeworks product URLs look like:
//   https://www.officeworks.com.au/shop/officeworks/p/<urlKeyword>-<SKU>
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if u.Host != "officeworks.com.au" && u.Host != "www.officeworks.com.au" {
		return "", false
	}
	// The SKU is the last segment after the final hyphen in the urlKeyword
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[len(parts)-2] != "p" {
		return "", false
	}
	slug := parts[len(parts)-1]
	// slug format: <urlKeyword>-<SKU>
	idx := strings.LastIndex(slug, "-")
	if idx < 0 {
		return "", false
	}
	sku := strings.ToUpper(slug[idx+1:])
	if sku == "" {
		return "", false
	}
	return sku, true
}

// algoliaRequest represents the Algolia multi-query API request body.
type algoliaRequest struct {
	Requests []algoliaQuery `json:"requests"`
}

type algoliaQuery struct {
	IndexName string `json:"indexName"`
	Params    string `json:"params"`
}

// algoliaResponse represents the Algolia multi-query API response.
type algoliaResponse struct {
	Results []algoliaResult `json:"results"`
}

type algoliaResult struct {
	Hits           []algoliaHit `json:"hits"`
	NbHits         int          `json:"nbHits"`
	Page           int          `json:"page"`
	NbPages        int          `json:"nbPages"`
	HitsPerPage    int          `json:"hitsPerPage"`
	ProcessingTime int          `json:"processingTimeMS"`
}

// algoliaHit represents a single product hit from the Officeworks catalogue.
type algoliaHit struct {
	ObjectID                 string     `json:"objectID"`
	SKU                      string     `json:"sku"`
	Name                     string     `json:"name"`
	Brand                    string     `json:"brand"`
	Published                bool       `json:"published"`
	RangedOnline             bool       `json:"rangedOnline"`
	RangedRetail             bool       `json:"rangedRetail"`
	Price                    int64      `json:"price"` // prices are in cents
	Gtin                     string     `json:"gtin"`
	ManufacturerPartNumber   string     `json:"manufacturerPartNumber"`
	Image                    string     `json:"image"`
	URLKeyword               string     `json:"urlKeyword"`
	SeoPath                  string     `json:"seoPath"`
	Status                   string     `json:"status"`
	UnitsPerPack             int        `json:"unitsPerPack"`
	Rating                   float64    `json:"rating"`
	ReviewCount              int        `json:"reviewCount"`
	LeadTimeDays             *int       `json:"leadTimeDays"`
	ClearanceOnline          bool       `json:"clearanceOnline"`
	Availability             []string   `json:"availability"`
	AvailState               []string   `json:"availState"`
	Categories               []string   `json:"categories"`
	LeafCategory            []string   `json:"leafCategory"`
	Color                     string     `json:"colour"`
	CableLength               string     `json:"cableLength"`
	ConnectorType             []string  `json:"connectorType"`
	InputOutputCableType      string     `json:"inputOutputCableType"`
	ConfigurableType          string     `json:"configurableType"`
	ProductType               string     `json:"productType"`
	ProductWebHierFolderPath  string     `json:"productWebHierFolderPath"`
	ProductRankPersonal       int        `json:"productRankPersonal"`
}

func (h *algoliaHit) priceString() string {
	// price is in cents; convert to dollars with 2 decimal places
	return fmt.Sprintf("%.2f", float64(h.Price)/100)
}

func (h *algoliaHit) productURL() string {
	if h.SeoPath != "" {
		return defaultBaseURL + h.SeoPath
	}
	if h.URLKeyword != "" {
		return capURLPrefix + h.URLKeyword
	}
	return ""
}

func (h *algoliaHit) imageURL() string {
	if h.Image == "" {
		return ""
	}
	// Algolia returns protocol-relative URLs
	if strings.HasPrefix(h.Image, "//") {
		return "https:" + h.Image
	}
	if strings.HasPrefix(h.Image, "http") {
		return h.Image
	}
	return ""
}

func (h *algoliaHit) inStock() bool {
	// If availability contains state codes, product is available
	if len(h.Availability) > 0 {
		return true
	}
	return strings.EqualFold(h.Status, "Active") && h.RangedOnline
}

func (h *algoliaHit) category() string {
	if len(h.LeafCategory) > 0 {
		return h.LeafCategory[0]
	}
	if len(h.Categories) > 0 {
		return h.Categories[0]
	}
	return ""
}

func (h *algoliaHit) description() string {
	// Build a useful description from available product attributes
	parts := []string{}
	if h.CableLength != "" {
		parts = append(parts, h.CableLength+" cable")
	}
	if h.Color != "" {
		parts = append(parts, h.Color)
	}
	if h.InputOutputCableType != "" {
		parts = append(parts, h.InputOutputCableType)
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	if h.Name != "" {
		return h.Name
	}
	return h.Brand
}

// SearchByKeyword searches Officeworks via the Algolia search API.
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	hits, err := p.algoliaSearch(ctx, keyword, 24)
	if err != nil {
		return nil, err
	}

	var results []suppliers.SearchResultDTO
	for _, hit := range hits {
		if hit.SKU == "" || hit.Name == "" {
			continue
		}
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "officeworks",
			ProviderID:      hit.SKU,
			Name:            hit.Name,
			Description:     hit.description(),
			Manufacturer:    hit.Brand,
			MPN:             hit.ManufacturerPartNumber,
			Category:        hit.category(),
			PreviewImageURL: hit.imageURL(),
			ProviderURL:     hit.productURL(),
		})
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no Officeworks products found for %q", keyword)
	}
	return results, nil
}

// GetDetails retrieves product details. Since Algolia returns rich product
// data in the search hits, we can do a single SKU search and map the first
// result directly — no HTML scraping required.
func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	// Search Algolia for the exact SKU; exact matches rank first.
	hits, err := p.algoliaSearch(ctx, providerID, 1)
	if err != nil {
		return nil, err
	}

	if len(hits) == 0 {
		return nil, fmt.Errorf("no Officeworks product found for code %q", providerID)
	}

	hit := hits[0]
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:         "officeworks",
			ProviderID:          hit.SKU,
			Name:                hit.Name,
			Description:         hit.description(),
			Category:            hit.category(),
			Manufacturer:        hit.Brand,
			MPN:                 hit.ManufacturerPartNumber,
			ManufacturingStatus: parseManufacturingStatusString(hit.Status),
			PreviewImageURL:     hit.imageURL(),
			ProviderURL:         hit.productURL(),
			Footprint:           "",
		},
		Notes: hit.description(),
	}

	if hit.imageURL() != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  hit.imageURL(),
			Name: hit.SKU + ".jpg",
		})
	}

	vi := suppliers.PurchaseInfoDTO{
		DistributorName: "Officeworks",
		OrderNumber:     hit.SKU,
		ProductURL:      hit.productURL(),
		Currency:        "AUD",
		Price:           hit.priceString(),
		MinimumOrderQty: "1",
		InStock:         hit.inStock(),
	}
	vi.Prices = append(vi.Prices, suppliers.PriceDTO{
		MinQuantity:          1,
		Price:                hit.priceString(),
		Currency:             "AUD",
		IncludesTax:          true,
		PriceRelatedQuantity: 1,
	})
	detail.VendorInfos = append(detail.VendorInfos, vi)

	// Add useful parameters from the Algolia hit
	if hit.Gtin != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Barcode (GTIN)",
			ValueText: hit.Gtin,
			Group:     "General",
		})
	}
	if hit.ManufacturerPartNumber != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Manufacturer Part Number",
			ValueText: hit.ManufacturerPartNumber,
			Group:     "General",
		})
	}
	if hit.UnitsPerPack > 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Units Per Pack",
			ValueText: strconv.Itoa(hit.UnitsPerPack),
			Group:     "General",
		})
	}
	if hit.CableLength != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Cable Length",
			ValueText: hit.CableLength,
			Group:     "Physical",
		})
	}
	if hit.Color != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Colour",
			ValueText: hit.Color,
			Group:     "Physical",
		})
	}
	if len(hit.ConnectorType) > 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Connector Type",
			ValueText: strings.Join(hit.ConnectorType, ", "),
			Group:     "Physical",
		})
	}

	return detail, nil
}

func (p *Provider) algoliaSearch(ctx context.Context, query string, hitsPerPage int) ([]algoliaHit, error) {
	params := fmt.Sprintf("query=%s&hitsPerPage=%d&page=0", url.QueryEscape(query), hitsPerPage)

	reqBody := algoliaRequest{
		Requests: []algoliaQuery{
			{
				IndexName: p.indexName,
				Params:    params,
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("officeworks: failed to encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", algoliaEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Algolia-Application-Id", p.appID)
	req.Header.Set("X-Algolia-API-Key", p.searchKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("officeworks: Algolia request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("officeworks: Algolia returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var algoliaResp algoliaResponse
	if err := json.NewDecoder(resp.Body).Decode(&algoliaResp); err != nil {
		return nil, fmt.Errorf("officeworks: failed to decode Algolia response: %w", err)
	}

	if len(algoliaResp.Results) == 0 {
		return nil, fmt.Errorf("officeworks: empty Algolia response")
	}

	return algoliaResp.Results[0].Hits, nil
}

func parseManufacturingStatusString(status string) string {
	switch strings.ToLower(status) {
	case "active":
		return "active"
	case "discontinued":
		return "discontinued"
	case "eol", "end of life":
		return "eol"
	case "nrnd":
		return "nrnd"
	case "announced":
		return "announced"
	default:
		return "unknown"
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
