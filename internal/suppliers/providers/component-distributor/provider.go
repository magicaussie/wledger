package cdi

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
	baseURL = "https://www.cdilib.com"
)

// Provider implements the suppliers.Provider interface for Component Distributor Inc.
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
		Key:          "cdi",
		Name:         "Component Distributor Inc (CDI)",
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
	return strings.Contains(domain, "cdilib.com")
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.cdilib.com/XXXXX
	parts := strings.Split(rawURL, "/")
	for _, part := range parts {
		if part != "" && part != "www.cdilib.com" && !strings.Contains(part, "product") {
			return part, true
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
		return nil, fmt.Errorf("CDI search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CDI API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp cdiSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode CDI search response: %w", err)
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
		return nil, fmt.Errorf("CDI detail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("CDI API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp cdiDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode CDI detail response: %w", err)
	}

	if apiResp.Product == nil {
		return nil, fmt.Errorf("no product found with SKU %s", providerID)
	}

	return p.productToDetail(apiResp.Product), nil
}

func (p *Provider) partsToSearchResults(parts []cdiProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "cdi",
			ProviderID:      part.SKU,
			Name:            part.Description,
			Description:     part.Description,
			Category:        part.Category,
			Manufacturer:    part.Manufacturer,
			MPN:             part.ManufacturerPartNumber,
			PreviewImageURL: part.ImageURL,
			ProviderURL:     fmt.Sprintf("%s/product/%s", baseURL, part.SKU),
			Footprint:       "",
		})
	}
	return results
}

func (p *Provider) productToDetail(product *cdiProduct) *suppliers.PartDetailDTO {
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "cdi",
			ProviderID:      product.SKU,
			Name:            product.Description,
			Description:     product.Description,
			Category:        product.Category,
			Manufacturer:    product.Manufacturer,
			MPN:             product.ManufacturerPartNumber,
			PreviewImageURL: product.ImageURL,
			ProviderURL:     fmt.Sprintf("%s/product/%s", baseURL, product.SKU),
			Footprint:       "",
		},
	}

	if product.ImageURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  product.ImageURL,
			Name: product.SKU + ".jpg",
		})
	}

	if product.DatasheetURL != "" {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  product.DatasheetURL,
			Name: product.SKU + "_datasheet.pdf",
		})
	}

	if len(product.PriceBreaks) > 0 {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Component Distributor",
			OrderNumber:     product.SKU,
			ProductURL:      fmt.Sprintf("%s/product/%s", baseURL, product.SKU),
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

// CDI API types

type cdiSearchResponse struct {
	Products []cdiProduct `json:"products"`
}

type cdiDetailResponse struct {
	Product *cdiProduct `json:"product"`
}

type cdiProduct struct {
	SKU                   string            `json:"sku"`
	Description           string            `json:"description"`
	Category              string            `json:"category"`
	Manufacturer          string            `json:"manufacturer"`
	ManufacturerPartNumber string           `json:"manufacturerPartNumber"`
	ImageURL              string            `json:"imageUrl"`
	DatasheetURL          string            `json:"datasheetUrl"`
	PriceBreaks           []cdiPriceBreak   `json:"priceBreaks"`
}

type cdiPriceBreak struct {
	Quantity int     `json:"quantity"`
	Price    string  `json:"price"`
	Currency string  `json:"currency"`
}
