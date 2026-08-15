package rs

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
	baseURL     = "https://uk.rs-online.com"
	searchURL   = baseURL + "/web/p/search"
	detailURL   = baseURL + "/web/p/"
)

// Provider implements the suppliers.Provider interface for RS Components.
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
		Key:          "rs-components",
		Name:         "RS Components",
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
		suppliers.CapFootprint,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return strings.Contains(domain, "rs-online.com") || strings.Contains(domain, "rs-online.")
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL formats:
	// https://uk.rs-online.com/web/p/XXXXX/XXXXX
	// https://uk.rs-online.com/web/c/...
	parts := strings.Split(rawURL, "/")
	for _, part := range parts {
		if len(part) == 8 && strings.HasPrefix(part, "RS") {
			return part, true
		}
	}
	return "", false
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	body := map[string]interface{}{
		"query":    keyword,
		"page":     1,
		"pageSize": 50,
		"currency": "GBP",
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

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RS Components search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("RS Components API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp rsSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode RS Components search response: %w", err)
	}

	return p.partsToSearchResults(apiResp.Results), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	url := fmt.Sprintf("%s%s.json", detailURL, providerID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("RS Components detail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("RS Components API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp rsDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode RS Components detail response: %w", err)
	}

	if apiResp.Product == nil {
		return nil, fmt.Errorf("no product found with RS number %s", providerID)
	}

	return p.productToDetail(apiResp.Product), nil
}

func (p *Provider) partsToSearchResults(parts []rsProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		imgURL := ""
		if len(part.Images) > 0 {
			imgURL = part.Images[0]
		}

		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "rs-components",
			ProviderID:      part.RsPartNumber,
			Name:            part.Description,
			Description:     part.Description,
			Category:        part.Category,
			Manufacturer:    part.Manufacturer,
			MPN:             part.ManufacturerPartNumber,
			PreviewImageURL: imgURL,
			ProviderURL:     fmt.Sprintf("%s/web/p/%s/", baseURL, part.RsPartNumber),
			Footprint:       part.Package,
		})
	}
	return results
}

func (p *Provider) productToDetail(product *rsProduct) *suppliers.PartDetailDTO {
	imgURL := ""
	if len(product.Images) > 0 {
		imgURL = product.Images[0]
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "rs-components",
			ProviderID:      product.RsPartNumber,
			Name:            product.Description,
			Description:     product.Description,
			Category:        product.Category,
			Manufacturer:    product.Manufacturer,
			MPN:             product.ManufacturerPartNumber,
			PreviewImageURL: imgURL,
			ProviderURL:     fmt.Sprintf("%s/web/p/%s/", baseURL, product.RsPartNumber),
			Footprint:       product.Package,
		},
	}

	if imgURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  imgURL,
			Name: product.RsPartNumber + ".jpg",
		})
	}

	if product.DatasheetURL != "" {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  product.DatasheetURL,
			Name: product.RsPartNumber + "_datasheet.pdf",
		})
	}

	// Pricing
	if product.Price != "" {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "RS Components",
			OrderNumber:     product.RsPartNumber,
			ProductURL:      fmt.Sprintf("%s/web/p/%s/", baseURL, product.RsPartNumber),
			Price:           product.Price,
			Currency:        product.Currency,
			MinimumOrderQty: product.MinimumOrderQuantity,
			InStock:         product.InStock,
		}
		detail.VendorInfos = append(detail.VendorInfos, vi)
	}

	// Parameters
	if product.Package != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Package",
			ValueText: product.Package,
			Group:     "Physical",
		})
	}
	if product.RoHSStatus != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "RoHS Status",
			ValueText: product.RoHSStatus,
			Group:     "Compliance",
		})
	}

	return detail
}

// RS Components API types

type rsSearchResponse struct {
	Results []rsProduct `json:"results"`
	Total   int         `json:"total"`
}

type rsDetailResponse struct {
	Product *rsProduct `json:"product"`
}

type rsProduct struct {
	RsPartNumber          string   `json:"rsPartNumber"`
	Description           string   `json:"description"`
	Category              string   `json:"category"`
	Manufacturer          string   `json:"manufacturer"`
	ManufacturerPartNumber string  `json:"manufacturerPartNumber"`
	Images                []string `json:"images"`
	Price                 string   `json:"price"`
	Currency              string   `json:"currency"`
	MinimumOrderQuantity  string   `json:"minimumOrderQuantity"`
	InStock               bool     `json:"inStock"`
	DatasheetURL          string   `json:"datasheetUrl"`
	Package               string   `json:"package"`
	RoHSStatus            string   `json:"rohsStatus"`
}
