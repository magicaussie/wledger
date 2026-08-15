package heilind

import (
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
	baseURL = "https://www.heilind.com"
)

// Provider implements the suppliers.Provider interface for Heilind Electronics.
type Provider struct {
	httpClient *http.Client
}

func init() {
	suppliers.Register(NewProvider())
}

func NewProvider() *Provider {
	return &Provider{
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "heilind",
		Name:         "Heilind Electronics",
		BaseURL:      baseURL,
		SupportsAuth: false,
		AuthType:     "none",
	}
}

func (p *Provider) IsActive() bool {
	return false
}

func (p *Provider) GetCapabilities() []suppliers.Capability {
	return []suppliers.Capability{
		suppliers.CapBasic,
		suppliers.CapPicture,
		suppliers.CapDatasheet,
		suppliers.CapPrice,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return strings.Contains(domain, "heilind.com")
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.heilind.com/product/XXXXX
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
	url := fmt.Sprintf("%s/api/search?q=%s", baseURL, keyword)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Heilind search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Heilind API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp heilindSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Heilind search response: %w", err)
	}

	return p.partsToSearchResults(apiResp.Products), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	url := fmt.Sprintf("%s/api/products/%s", baseURL, providerID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Heilind detail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Heilind API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp heilindDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Heilind detail response: %w", err)
	}

	if apiResp.Product == nil {
		return nil, fmt.Errorf("no product found with code %s", providerID)
	}

	return p.productToDetail(apiResp.Product), nil
}

func (p *Provider) partsToSearchResults(parts []heilindProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "heilind",
			ProviderID:      part.PartNumber,
			Name:            part.Description,
			Description:     part.Description,
			Category:        part.Category,
			Manufacturer:    part.Manufacturer,
			MPN:             part.ManufacturerPartNumber,
			PreviewImageURL: part.ImageURL,
			ProviderURL:     fmt.Sprintf("%s/product/%s", baseURL, part.PartNumber),
			Footprint:       "",
		})
	}
	return results
}

func (p *Provider) productToDetail(product *heilindProduct) *suppliers.PartDetailDTO {
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "heilind",
			ProviderID:      product.PartNumber,
			Name:            product.Description,
			Description:     product.Description,
			Category:        product.Category,
			Manufacturer:    product.Manufacturer,
			MPN:             product.ManufacturerPartNumber,
			PreviewImageURL: product.ImageURL,
			ProviderURL:     fmt.Sprintf("%s/product/%s", baseURL, product.PartNumber),
			Footprint:       "",
		},
	}

	if product.ImageURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  product.ImageURL,
			Name: product.PartNumber + ".jpg",
		})
	}

	if product.DatasheetURL != "" {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  product.DatasheetURL,
			Name: product.PartNumber + "_datasheet.pdf",
		})
	}

	if len(product.PriceBreaks) > 0 {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Heilind",
			OrderNumber:     product.PartNumber,
			ProductURL:      fmt.Sprintf("%s/product/%s", baseURL, product.PartNumber),
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

	return detail
}

// Heilind API types

type heilindSearchResponse struct {
	Products []heilindProduct `json:"products"`
}

type heilindDetailResponse struct {
	Product *heilindProduct `json:"product"`
}

type heilindProduct struct {
	PartNumber              string             `json:"partNumber"`
	Description             string             `json:"description"`
	Category                string             `json:"category"`
	Manufacturer            string             `json:"manufacturer"`
	ManufacturerPartNumber  string             `json:"manufacturerPartNumber"`
	ImageURL                string             `json:"imageUrl"`
	DatasheetURL            string             `json:"datasheetUrl"`
	PriceBreaks             []heilindPriceBreak `json:"priceBreaks"`
}

type heilindPriceBreak struct {
	Quantity int     `json:"quantity"`
	Price    string  `json:"price"`
	Currency string  `json:"currency"`
}
