package semikron

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
	baseURL = "https://www.semikron-danfoss.com"
)

// Provider implements the suppliers.Provider interface for Semikron-Danfoss.
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
		Key:          "semikron",
		Name:         "Semikron-Danfoss",
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
		suppliers.CapFootprint,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return strings.Contains(domain, "semikron-danfoss.com") ||
		strings.Contains(domain, "semikron.com")
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.semikron-danfoss.com/...
	parts := strings.Split(rawURL, "/")
	for i, part := range parts {
		if part == "products" && i+1 < len(parts) {
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
		return nil, fmt.Errorf("Semikron search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Semikron API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp semikronSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Semikron search response: %w", err)
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
		return nil, fmt.Errorf("Semikron detail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Semikron API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp semikronDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Semikron detail response: %w", err)
	}

	if apiResp.Product == nil {
		return nil, fmt.Errorf("no product found with code %s", providerID)
	}

	return p.productToDetail(apiResp.Product), nil
}

func (p *Provider) partsToSearchResults(parts []semikronProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "semikron",
			ProviderID:      part.ProductCode,
			Name:            part.Name,
			Description:     part.Description,
			Category:        part.Category,
			Manufacturer:    "Semikron-Danfoss",
			MPN:             part.ProductCode,
			PreviewImageURL: part.ImageURL,
			ProviderURL:     fmt.Sprintf("%s/products/%s", baseURL, part.ProductCode),
			Footprint:       part.Package,
		})
	}
	return results
}

func (p *Provider) productToDetail(product *semikronProduct) *suppliers.PartDetailDTO {
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "semikron",
			ProviderID:      product.ProductCode,
			Name:            product.Name,
			Description:     product.Description,
			Category:        product.Category,
			Manufacturer:    "Semikron-Danfoss",
			MPN:             product.ProductCode,
			PreviewImageURL: product.ImageURL,
			ProviderURL:     fmt.Sprintf("%s/products/%s", baseURL, product.ProductCode),
			Footprint:       product.Package,
		},
	}

	if product.ImageURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  product.ImageURL,
			Name: product.ProductCode + ".jpg",
		})
	}

	if product.DatasheetURL != "" {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  product.DatasheetURL,
			Name: product.ProductCode + "_datasheet.pdf",
		})
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

// Semikron API types

type semikronSearchResponse struct {
	Products []semikronProduct `json:"products"`
}

type semikronDetailResponse struct {
	Product *semikronProduct `json:"product"`
}

type semikronProduct struct {
	ProductCode   string `json:"productCode"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Category      string `json:"category"`
	ImageURL      string `json:"imageUrl"`
	DatasheetURL  string `json:"datasheetUrl"`
	Package       string `json:"package"`
}
