package mouser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	baseURL        = "https://api.mouser.com/api/v2"
	searchEndpoint = "/search/keyword"
	detailEndpoint = "/search/partnumber"
)

// Provider implements the suppliers.Provider interface for Mouser Electronics.
type Provider struct {
	httpClient *http.Client
	apiKey     string
}

// NewProvider creates a new Mouser provider with the given API key.
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
		Key:          "mouser",
		Name:         "Mouser Electronics",
		BaseURL:      "https://www.mouser.com",
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
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return domain == "mouser.com" || domain == "www.mouser.com"
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.mouser.com/ProductDetail/Mouser/XXXXXX?...
	// or: https://www.mouser.com/c/?q=XXXX
	parts := strings.Split(rawURL, "/")
	for i, part := range parts {
		if part == "ProductDetail" && i+1 < len(parts) {
			return parts[i+1], true
		}
	}
	return "", false
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Mouser API key not configured")
	}

	body := mouserSearchRequest{
		SearchByKeywordRequest: &keywordRequest{
			Keyword:       keyword,
			RecordCount:   50,
			StartRecord:   0,
			SearchOptions: "string",
			CharacterSet:  "utf8",
		},
	}

	resp, err := p.post(ctx, searchEndpoint, body)
	if err != nil {
		return nil, fmt.Errorf("mouser search request failed: %w", err)
	}
	defer resp.Body.Close()

	var apiResp mouserSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode mouser search response: %w", err)
	}

	return p.partsToSearchResults(apiResp.SearchResults.Parts), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("Mouser API key not configured")
	}

	body := mouserSearchRequest{
		SearchByPartRequest: &searchByPartRequest{
			MouserPartNumber:  providerID,
			PartSearchOptions: "Exact",
		},
	}

	resp, err := p.post(ctx, detailEndpoint, body)
	if err != nil {
		return nil, fmt.Errorf("mouser detail request failed: %w", err)
	}
	defer resp.Body.Close()

	var apiResp mouserSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode mouser detail response: %w", err)
	}

	if len(apiResp.Errors) > 0 {
		return nil, fmt.Errorf("mouser detail failed: %s", apiResp.Errors[0].Message)
	}

	if len(apiResp.SearchResults.Parts) == 0 {
		return nil, fmt.Errorf("no part found with ID %s", providerID)
	}

	part := apiResp.SearchResults.Parts[0]
	return p.partToDetail(part), nil
}

func (p *Provider) post(ctx context.Context, endpoint string, body any) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s%s?apiKey=%s", baseURL, endpoint, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("mouser API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return resp, nil
}

func (p *Provider) partsToSearchResults(parts []mouserPart) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:         "mouser",
			ProviderID:          part.MouserPartNumber,
			Name:                part.Description,
			Description:         part.Description,
			Category:            part.Category,
			Manufacturer:        part.Manufacturer,
			MPN:                 part.ManufacturerPartNumber,
			PreviewImageURL:     part.ImagePath,
			ManufacturingStatus: normalizeMfgStatus(part.Lifecycle),
			ProviderURL:         part.ProductDetailURL,
			Footprint:           "",
		})
	}
	return results
}

func (p *Provider) partToDetail(part mouserPart) *suppliers.PartDetailDTO {
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:         "mouser",
			ProviderID:          part.MouserPartNumber,
			Name:                part.Description,
			Description:         part.Description,
			Category:            part.Category,
			Manufacturer:        part.Manufacturer,
			MPN:                 part.ManufacturerPartNumber,
			PreviewImageURL:     part.ImagePath,
			ManufacturingStatus: normalizeMfgStatus(part.Lifecycle),
			ProviderURL:         part.ProductDetailURL,
		},
		Notes:                  part.AdditionalDescription,
		ManufacturerProductURL: part.ManufacturerProductPage,
	}

	// Datasheets
	for _, ds := range part.DataSheetURLs {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  ds,
			Name: extractFilename(ds),
		})
	}
	if len(detail.Datasheets) == 0 && part.DataSheetURL != "" {
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  part.DataSheetURL,
			Name: extractFilename(part.DataSheetURL),
		})
	}

	// Images
	if part.ImagePath != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  part.ImagePath,
			Name: extractFilename(part.ImagePath),
		})
	}

	// Pricing
	if len(part.PriceBreaks) > 0 {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Mouser Electronics",
			OrderNumber:     part.MouserPartNumber,
			ProductURL:      part.ProductDetailURL,
			Price:           part.PriceBreaks[0].Price,
			Currency:        part.PriceBreaks[0].Currency,
			InStock:         parseStock(part.AvailabilityInStock),
		}
		for _, pb := range part.PriceBreaks {
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

	if part.Min != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Minimum Order Quantity",
			ValueText: part.Min,
			Group:     "Ordering",
		})
	}
	if part.Reel != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Reel Quantity",
			ValueText: part.Reel,
			Group:     "Ordering",
		})
	}

	return detail
}

func normalizeMfgStatus(lifecycle string) string {
	switch strings.ToLower(lifecycle) {
	case "active":
		return "Active"
	case "obsolete":
		return "Obsolete"
	case "end of life", "eol":
		return "End of Life"
	case "not recommended for new designs", "nrnd":
		return "Not Recommended for New Design"
	default:
		return lifecycle
	}
}

func extractFilename(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return url
}

func parseStock(s string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	return n > 0
}

// Mouser API request/response types

type mouserSearchRequest struct {
	SearchByKeywordRequest *keywordRequest      `json:"SearchByKeywordRequest,omitempty"`
	SearchByPartRequest    *searchByPartRequest `json:"SearchByPartRequest,omitempty"`
}

type keywordRequest struct {
	Keyword       string `json:"Keyword"`
	RecordCount   int    `json:"RecordCount"`
	StartRecord   int    `json:"StartRecord"`
	SearchOptions string `json:"SearchOptions"`
	CharacterSet  string `json:"CharacterSet"`
}

type searchByPartRequest struct {
	MouserPartNumber  string `json:"mouserPartNumber"`
	PartSearchOptions string `json:"partSearchOptions,omitempty"`
}

type mouserSearchResponse struct {
	Errors        []mouserError `json:"Errors"`
	SearchResults mouserResults `json:"SearchResults"`
}

type mouserError struct {
	ID      any    `json:"Id"`
	Name    string `json:"Name"`
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

type mouserResults struct {
	NumberOfResults int          `json:"NumberOfResults"`
	Parts           []mouserPart `json:"Parts"`
}

type mouserPart struct {
	AverageSampleRating     string        `json:"AverageSampleRating"`
	AvailabilityInStock     string        `json:"AvailabilityInStock"`
	Category                string        `json:"Category"`
	CatPageURL              string        `json:"CatPageURL"`
	Currency                string        `json:"Currency"`
	DataSheetURL            string        `json:"DataSheetUrl"`
	DataSheetURLs           []string      `json:"DataSheetURLs"`
	DependentOn             string        `json:"DependentOn"`
	Description             string        `json:"Description"`
	AdditionalDescription   string        `json:"AdditionalDescription"`
	FamilyID                int           `json:"FamilyID"`
	Family                  string        `json:"Family"`
	ImagePath               string        `json:"ImagePath"`
	InventoryLevel          int           `json:"InventoryLevel"`
	InventoryURL            string        `json:"InventoryURL"`
	IsAlternate             bool          `json:"IsAlternate"`
	Lifecycle               string        `json:"Lifecycle"`
	Manufacturer            string        `json:"Manufacturer"`
	ManufacturerPartNumber  string        `json:"ManufacturerPartNumber"`
	ManufacturerProductPage string        `json:"ManufacturerProductPage"`
	Min                     string        `json:"Min"`
	MultiSimBlue            int           `json:"MultiSimBlue"`
	MouserPartNumber        string        `json:"MouserPartNumber"`
	NewItem                 bool          `json:"NewItem"`
	NewPart                 bool          `json:"NewPart"`
	PartDescriptionNotes    string        `json:"PartDescriptionNotes"`
	PriceBreaks             []mouserPrice `json:"PriceBreaks"`
	ProductDetailURL        string        `json:"ProductDetailUrl"`
	ProductFamily           string        `json:"ProductFamily"`
	Reel                    string        `json:"Reel"`
	ROHSStatus              string        `json:"ROHSStatus"`
	ReplacedByPartNumber    string        `json:"ReplacedByPartNumber"`
	SearchResults           string        `json:"SearchResults"`
	Series                  string        `json:"Series"`
	TariffCode              string        `json:"TariffCode"`
	UnitPrice               string        `json:"UnitPrice"`
	URLProductMarking       string        `json:"URLProductMarking"`
}

type mouserPrice struct {
	BreakQuantity  int    `json:"BreakQuantity"`
	Price          string `json:"Price"`
	Currency       string `json:"Currency"`
	Quantity       int    `json:"Quantity"`
	QuantityWithDP string `json:"QuantityWithDP"`
}

func init() {
	suppliers.Register(NewProvider(""))
}
