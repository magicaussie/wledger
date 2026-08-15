package element14

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	baseURL = "https://api.element14.com/catalog/products"
)

// Provider implements the suppliers.Provider interface for Element14.
type Provider struct {
	httpClient *http.Client
	apiKey     string
	storeID    string // full store domain, e.g. "au.element14.com"
}

func init() {
	suppliers.Register(NewProvider("", "au.element14.com"))
}

func NewProvider(apiKey, storeID string) *Provider {
	if storeID == "" {
		storeID = "au.element14.com"
	}
	return &Provider{
		httpClient: &http.Client{Timeout: 20 * time.Second},
		apiKey:     apiKey,
		storeID:    storeID,
	}
}

// SetAPIKey implements the suppliers.APIKeyProvider interface.
func (p *Provider) SetAPIKey(apiKey string) {
	p.apiKey = apiKey
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "element14",
		Name:         "Element14",
		BaseURL:      "https://www.element14.com",
		SupportsAuth: true,
		AuthType:     "api_key",
	}
}

func (p *Provider) IsActive() bool {
	return p.apiKey != ""
}

func (p *Provider) GetCapabilities() []suppliers.Capability {
	return []suppliers.Capability{
		suppliers.CapBasic,
		suppliers.CapPicture,
		suppliers.CapDatasheet,
		suppliers.CapPrice,
		suppliers.CapDistributor,
		suppliers.CapManufacturer,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return strings.Contains(domain, "element14.com")
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	parts := strings.Split(rawURL, "/")
	for _, part := range parts {
		if part == "p" {
			continue
		}
		if len(part) >= 6 && !strings.Contains(part, ".") {
			// element14 product URLs end with /p/XXXXXX or /dp/XXXXXX
			if isNumeric(part) {
				return part, true
			}
		}
	}
	return "", false
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Element14 API key not configured")
	}

	q := p.baseQuery()
	q.Set("term", "any:"+keyword)
	q.Set("resultsSettings.offset", "0")
	q.Set("resultsSettings.numberOfResults", "50")
	q.Set("resultsSettings.responseGroup", "large")

	apiResp, err := p.doGet(ctx, q)
	if err != nil {
		return nil, err
	}

	return p.partsToSearchResults(apiResp.KeywordSearchReturn.Products), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Element14 API key not configured")
	}

	q := p.baseQuery()
	q.Set("term", "id:"+providerID)
	q.Set("resultsSettings.offset", "0")
	q.Set("resultsSettings.numberOfResults", "1")
	q.Set("resultsSettings.responseGroup", "large")

	apiResp, err := p.doGet(ctx, q)
	if err != nil {
		return nil, err
	}

	products := apiResp.PremierFarnellPartNumberReturn.Products
	if len(products) == 0 {
		products = apiResp.KeywordSearchReturn.Products
	}
	if len(products) == 0 {
		return nil, fmt.Errorf("no product found with SKU %s", providerID)
	}

	return p.productToDetail(&products[0]), nil
}

func (p *Provider) baseQuery() url.Values {
	q := url.Values{}
	q.Set("callInfo.responseDataFormat", "JSON")
	q.Set("callInfo.apiKey", p.apiKey)
	q.Set("storeInfo.id", p.storeID)
	return q
}

func (p *Provider) doGet(ctx context.Context, q url.Values) (*element14Response, error) {
	reqURL := baseURL + "?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Element14 request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Element14 API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp element14Response
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Element14 response: %w", err)
	}

	return &apiResp, nil
}

func (p *Provider) partsToSearchResults(parts []element14Product) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "element14",
			ProviderID:      part.SKU,
			Name:            part.DisplayName,
			Description:     part.attributeSummary(),
			Category:        "",
			Manufacturer:    part.BrandName,
			MPN:             part.TranslatedManufacturerPartNumber,
			PreviewImageURL: p.imageURL(part.Image),
			ProviderURL:     p.productURL(part.SKU),
			Footprint:       "",
		})
	}
	return results
}

func (p *Provider) productToDetail(product *element14Product) *suppliers.PartDetailDTO {
	imgURL := p.imageURL(product.Image)

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "element14",
			ProviderID:      product.SKU,
			Name:            product.DisplayName,
			Description:     product.attributeSummary(),
			Category:        "",
			Manufacturer:    product.BrandName,
			MPN:             product.TranslatedManufacturerPartNumber,
			PreviewImageURL: imgURL,
			ProviderURL:     p.productURL(product.SKU),
			Footprint:       "",
		},
	}

	if imgURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  imgURL,
			Name: product.SKU + ".jpg",
		})
	}

	for _, ds := range product.Datasheets {
		if ds.URL != "" {
			detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
				URL:  ds.URL,
				Name: product.SKU + "_datasheet.pdf",
			})
		}
	}

	vi := suppliers.PurchaseInfoDTO{
		DistributorName: "Element14",
		OrderNumber:     product.SKU,
		ProductURL:      p.productURL(product.SKU),
		InStock:         product.Stock.Level > 0,
		MinimumOrderQty: fmt.Sprintf("%d", product.TranslatedMinimumOrderQuantity),
	}
	for _, pb := range product.Prices {
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          pb.From,
			Price:                fmt.Sprintf("%.4f", pb.Cost),
			Currency:             "AUD",
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
	}
	detail.VendorInfos = append(detail.VendorInfos, vi)

	for _, attr := range product.Attributes {
		if attr.AttributeValue != "" {
			detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
				Name:      attr.AttributeLabel,
				ValueText: attr.AttributeValue,
				Unit:      attr.AttributeUnit,
				Group:     "General",
			})
		}
	}

	if product.Stock.Level > 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "In Stock",
			ValueText: fmt.Sprintf("%d", product.Stock.Level),
			Group:     "General",
		})
	}

	return detail
}

func (p *Provider) imageURL(img element14Image) string {
	if img.BaseName == "" {
		return ""
	}
	name := strings.TrimPrefix(img.BaseName, "/")
	if strings.HasPrefix(img.VrntPath, "farnell") {
		return fmt.Sprintf("https://%s/productimages/standard/%s", p.storeID, name)
	}
	return fmt.Sprintf("https://%s/productimages/%s%s", p.storeID, strings.TrimPrefix(img.VrntPath, "/"), strings.TrimPrefix(name, "/"))
}

func (p *Provider) productURL(sku string) string {
	return fmt.Sprintf("https://%s/p/%s", p.storeID, sku)
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Element14 API types

type element14Response struct {
	KeywordSearchReturn struct {
		NumberOfResults int                `json:"numberOfResults"`
		Products        []element14Product `json:"products"`
	} `json:"keywordSearchReturn"`
	PremierFarnellPartNumberReturn struct {
		NumberOfResults int                `json:"numberOfResults"`
		Products        []element14Product `json:"products"`
	} `json:"premierFarnellPartNumberReturn"`
}

type element14Product struct {
	SKU                             string                `json:"sku"`
	DisplayName                     string                `json:"displayName"`
	BrandName                       string                `json:"brandName"`
	TranslatedManufacturerPartNumber string               `json:"translatedManufacturerPartNumber"`
	TranslatedMinimumOrderQuantity  int                   `json:"translatedMinimumOrderQuality"`
	UnitOfMeasure                   string                `json:"unitOfMeasure"`
	Image                           element14Image        `json:"image"`
	Datasheets                      []element14Datasheet  `json:"datasheets"`
	Prices                          []element14Price      `json:"prices"`
	Attributes                      []element14Attribute  `json:"attributes"`
	Stock                           element14Stock        `json:"stock"`
	InventoryCode                   int                   `json:"inventoryCode"`
	ReleaseStatusCode               int                   `json:"releaseStatusCode"`
	ProductStatus                   string                `json:"productStatus"`
}

type element14Image struct {
	BaseName string `json:"baseName"`
	VrntPath string `json:"vrntPath"`
}

type element14Datasheet struct {
	URL string `json:"url"`
}

type element14Price struct {
	From int     `json:"from"`
	To   int     `json:"to"`
	Cost float64 `json:"cost"`
}

type element14Attribute struct {
	AttributeLabel string `json:"attributeLabel"`
	AttributeValue string `json:"attributeValue"`
	AttributeUnit  string `json:"attributeUnit"`
}

type element14Stock struct {
	Level int `json:"level"`
}

func (p *element14Product) attributeSummary() string {
	if len(p.Attributes) == 0 {
		return ""
	}
	var parts []string
	for _, a := range p.Attributes {
		if a.AttributeValue != "" {
			parts = append(parts, a.AttributeLabel+": "+a.AttributeValue)
		}
	}
	return strings.Join(parts, "\n")
}