package datasheet

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
	baseURL       = "https://www.datasheet.ca"
	searchURL     = baseURL + "/search_part/keyword"
	detailURL     = baseURL + "/api/part"
)

// Provider implements the suppliers.Provider interface for Datasheet (datasheet.ca).
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
		Key:          "datasheet",
		Name:         "Datasheet.ca",
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
	return domain == "datasheet.ca" || domain == "www.datasheet.ca"
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.datasheet.ca/view/XXXXXXX.html
	// or: https://www.datasheet.ca/part-details/XXXXXXX
	parts := strings.Split(rawURL, "/")
	for i, part := range parts {
		if (part == "view" || part == "part-details") && i+1 < len(parts) {
			code := strings.TrimSuffix(parts[i+1], ".html")
			if code != "" {
				return code, true
			}
		}
	}
	return "", false
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	body := map[string]interface{}{
		"keyword": keyword,
		"page":    1,
		"per_page": 50,
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
		return nil, fmt.Errorf("Datasheet search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Datasheet API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp datasheetSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Datasheet search response: %w", err)
	}

	return p.partsToSearchResults(apiResp.Data), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	url := fmt.Sprintf("%s?part_number=%s", detailURL, providerID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Datasheet detail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Datasheet API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp datasheetDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Datasheet detail response: %w", err)
	}

	if apiResp.Data == nil {
		return nil, fmt.Errorf("no part found with ID %s", providerID)
	}

	return p.partToDetail(apiResp.Data), nil
}

func (p *Provider) partsToSearchResults(parts []datasheetPart) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "datasheet",
			ProviderID:      part.PartNumber,
			Name:            part.Description,
			Description:     part.Description,
			Category:        part.Category,
			Manufacturer:    part.Manufacturer,
			MPN:             part.PartNumber,
			PreviewImageURL: part.ImageURL,
			ProviderURL:     fmt.Sprintf("%s/view/%s.html", baseURL, part.PartNumber),
			Footprint:       "",
		})
	}
	return results
}

func (p *Provider) partToDetail(part *datasheetPart) *suppliers.PartDetailDTO {
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "datasheet",
			ProviderID:      part.PartNumber,
			Name:            part.Description,
			Description:     part.Description,
			Category:        part.Category,
			Manufacturer:    part.Manufacturer,
			MPN:             part.PartNumber,
			PreviewImageURL: part.ImageURL,
			ProviderURL:     fmt.Sprintf("%s/view/%s.html", baseURL, part.PartNumber),
			Footprint:       "",
		},
	}

	if part.ImageURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  part.ImageURL,
			Name: part.PartNumber + ".jpg",
		})
	}

	if part.DatasheetURL != "" {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  part.DatasheetURL,
			Name: part.PartNumber + "_datasheet.pdf",
		})
	}

	return detail
}

// Datasheet API types

type datasheetSearchResponse struct {
	Data []datasheetPart `json:"data"`
}

type datasheetDetailResponse struct {
	Data *datasheetPart `json:"data"`
}

type datasheetPart struct {
	PartNumber     string `json:"part_number"`
	Description    string `json:"description"`
	Category       string `json:"category"`
	Manufacturer   string `json:"manufacturer"`
	ImageURL       string `json:"image_url"`
	DatasheetURL   string `json:"datasheet_url"`
}
