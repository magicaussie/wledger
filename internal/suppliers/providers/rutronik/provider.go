package rutronik

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	baseURL   = "https://www.rutronik.com"
	searchURL = baseURL + "/api/search/products"
	detailURL = baseURL + "/api/products"
)

// Provider implements the suppliers.Provider interface for Rutronik.
type Provider struct {
	httpClient *http.Client
	apiKey     string
}

func init() {
	suppliers.Register(NewProvider(""))
}

func NewProvider(apiKey string) *Provider {
	return &Provider{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiKey:     apiKey,
	}
}

// SetAPIKey implements the suppliers.APIKeyProvider interface.
func (p *Provider) SetAPIKey(apiKey string) {
	p.apiKey = apiKey
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "rutronik",
		Name:         "Rutronik",
		BaseURL:      baseURL,
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
		suppliers.CapFootprint,
		suppliers.CapDistributor,
		suppliers.CapManufacturer,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return strings.Contains(domain, "rutronik.com")
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.rutronik.com/product/XXXXX/XXXXX
	parts := strings.Split(rawURL, "/")
	for i, part := range parts {
		if part == "product" && i+1 < len(parts) {
			code := parts[i+1]
			if code != "" {
				return code, true
			}
		}
	}
	return "", false
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Rutronik API key not configured")
	}

	body := rutronikSearchRequest{
		Query:    keyword,
		PageSize: 50,
		Page:     1,
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Rutronik search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Rutronik API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp rutronikSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Rutronik search response: %w", err)
	}

	return p.partsToSearchResults(apiResp.Data), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Rutronik API key not configured")
	}

	url := fmt.Sprintf("%s/%s", detailURL, providerID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Rutronik detail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Rutronik API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp rutronikDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Rutronik detail response: %w", err)
	}

	if apiResp.Data == nil {
		return nil, fmt.Errorf("no product found with article number %s", providerID)
	}

	return p.productToDetail(apiResp.Data), nil
}

func (p *Provider) partsToSearchResults(parts []rutronikProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		imgURL := ""
		if len(part.Images) > 0 {
			imgURL = part.Images[0]
		}

		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "rutronik",
			ProviderID:      part.ArticleNumber,
			Name:            part.Description,
			Description:     part.Description,
			Category:        part.Category,
			Manufacturer:    part.Manufacturer,
			MPN:             part.ManufacturerPartNumber,
			PreviewImageURL: imgURL,
			ProviderURL:     fmt.Sprintf("%s/product/%s", baseURL, part.ArticleNumber),
			Footprint:       part.Package,
		})
	}
	return results
}

func (p *Provider) productToDetail(product *rutronikProduct) *suppliers.PartDetailDTO {
	imgURL := ""
	if len(product.Images) > 0 {
		imgURL = product.Images[0]
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "rutronik",
			ProviderID:      product.ArticleNumber,
			Name:            product.Description,
			Description:     product.Description,
			Category:        product.Category,
			Manufacturer:    product.Manufacturer,
			MPN:             product.ManufacturerPartNumber,
			PreviewImageURL: imgURL,
			ProviderURL:     fmt.Sprintf("%s/product/%s", baseURL, product.ArticleNumber),
			Footprint:       product.Package,
		},
	}

	if imgURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  imgURL,
			Name: product.ArticleNumber + ".jpg",
		})
	}

	if product.DatasheetURL != "" {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  product.DatasheetURL,
			Name: product.ArticleNumber + "_datasheet.pdf",
		})
	}

	if len(product.PriceBreaks) > 0 {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Rutronik",
			OrderNumber:     product.ArticleNumber,
			ProductURL:      fmt.Sprintf("%s/product/%s", baseURL, product.ArticleNumber),
		}
		for _, pb := range product.PriceBreaks {
			vi.Prices = append(vi.Prices, suppliers.PriceDTO{
				MinQuantity:          pb.Quantity,
				Price:                pb.Price,
				Currency:             pb.Currency,
				IncludesTax:          false,
				PriceRelatedQuantity: 1,
			})
		}
		detail.VendorInfos = append(detail.VendorInfos, vi)
	}

	if product.Package != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Package",
			ValueText: product.Package,
			Group:     "Physical",
		})
	}

	return detail
}

// Rutronik API types

type rutronikSearchRequest struct {
	Query    string `json:"query"`
	PageSize int    `json:"pageSize"`
	Page     int    `json:"page"`
}

type rutronikSearchResponse struct {
	Data []rutronikProduct `json:"data"`
}

type rutronikDetailResponse struct {
	Data *rutronikProduct `json:"data"`
}

type rutronikProduct struct {
	ArticleNumber          string               `json:"articleNumber"`
	Description            string               `json:"description"`
	Category               string               `json:"category"`
	Manufacturer           string               `json:"manufacturer"`
	ManufacturerPartNumber string               `json:"manufacturerPartNumber"`
	Images                 []string             `json:"images"`
	DatasheetURL           string               `json:"datasheetUrl"`
	Package                string               `json:"package"`
	PriceBreaks            []rutronikPriceBreak `json:"priceBreaks"`
}

type rutronikPriceBreak struct {
	Quantity int     `json:"quantity"`
	Price    string  `json:"price"`
	Currency string  `json:"currency"`
}
