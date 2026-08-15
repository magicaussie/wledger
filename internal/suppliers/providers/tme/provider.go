package tme

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	baseURL     = "https://api.tme.eu"
	searchURL   = baseURL + "/Products/Search.json"
	detailURL   = baseURL + "/Products/GetProductDetails.json"
	filesURL    = baseURL + "/Products/GetProductFiles.json"
	pricesURL  = baseURL + "/Products/GetProductPrices.json"
)

// Provider implements the suppliers.Provider interface for TME.
type Provider struct {
	httpClient *http.Client
	apiKey     string
	apiSecret  string
}

func init() {
	suppliers.Register(NewProvider("", ""))
}

func NewProvider(apiKey, apiSecret string) *Provider {
	return &Provider{
		httpClient: &http.Client{Timeout: 15 * time.Second},
		apiKey:     apiKey,
		apiSecret:  apiSecret,
	}
}

// SetAPIKey implements the suppliers.APIKeyProvider interface.
func (p *Provider) SetAPIKey(apiKey string) {
	p.apiKey = apiKey
}

// SetSecret sets the API secret for TME authentication.
func (p *Provider) SetSecret(secret string) {
	p.apiSecret = secret
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "tme",
		Name:         "TME (Transfer Multisort Elektronik)",
		BaseURL:      "https://www.tme.eu",
		SupportsAuth: true,
		AuthType:     "api_key+secret",
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
	return domain == "tme.eu" || domain == "www.tme.eu"
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.tme.eu/en/details/XXXX/XXXX/XXXX/
	parts := strings.Split(rawURL, "/")
	for i, part := range parts {
		if part == "details" && i+1 < len(parts) {
			code := parts[i+1]
			if code != "" {
				return code, true
			}
		}
	}
	return "", false
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	params := map[string]string{
		"SearchTerm": keyword,
		"Country":    "US",
		"Language":   "EN",
		"Currency":   "USD",
		"RowCount":   "50",
		"Offset":     "0",
	}

	body, err := p.doRequest(ctx, searchURL, params)
	if err != nil {
		return nil, fmt.Errorf("TME search failed: %w", err)
	}

	var apiResp tmeSearchResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode TME search response: %w", err)
	}

	return p.partsToSearchResults(apiResp.Data), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	params := map[string]string{
		"SymbolList": providerID,
		"Country":    "US",
		"Language":   "EN",
		"Currency":   "USD",
	}

	body, err := p.doRequest(ctx, detailURL, params)
	if err != nil {
		return nil, fmt.Errorf("TME detail request failed: %w", err)
	}

	var apiResp tmeDetailResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode TME detail response: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("no product found with symbol %s", providerID)
	}

	product := apiResp.Data[0]

	// Fetch pricing
	prices, _ := p.fetchPrices(ctx, providerID)

	// Fetch files (datasheets, images)
	files, _ := p.fetchFiles(ctx, providerID)

	return p.productToDetail(product, prices, files), nil
}

func (p *Provider) fetchPrices(ctx context.Context, symbol string) ([]tmePrice, error) {
	params := map[string]string{
		"SymbolList": symbol,
		"Country":    "US",
		"Language":   "EN",
		"Currency":   "USD",
	}

	body, err := p.doRequest(ctx, pricesURL, params)
	if err != nil {
		return nil, err
	}

	var apiResp tmePricesResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}

	if len(apiResp.Data) > 0 {
		return apiResp.Data[0].Prices, nil
	}
	return nil, nil
}

func (p *Provider) fetchFiles(ctx context.Context, symbol string) ([]tmeFile, error) {
	params := map[string]string{
		"SymbolList": symbol,
		"Country":    "US",
		"Language":   "EN",
	}

	body, err := p.doRequest(ctx, filesURL, params)
	if err != nil {
		return nil, err
	}

	var apiResp tmeFilesResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}

	if len(apiResp.Data) > 0 {
		return apiResp.Data[0].Files, nil
	}
	return nil, nil
}

func (p *Provider) doRequest(ctx context.Context, url string, params map[string]string) ([]byte, error) {
	params["Token"] = p.apiKey
	params["Timestamp"] = time.Now().UTC().Format("2006-01-02T15:04:05Z")

	// Generate HMAC signature
	queryString := p.buildQueryString(params)
	mac := hmac.New(sha256.New, []byte(p.apiSecret))
	mac.Write([]byte(queryString))
	signature := hex.EncodeToString(mac.Sum(nil))
	params["Signature"] = signature

	// Build URL with query params
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(p.buildQueryString(params)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("TME API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}

func (p *Provider) buildQueryString(params map[string]string) string {
	var parts []string
	for k, v := range params {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "&")
}

func (p *Provider) partsToSearchResults(parts []tmeProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		imgURL := ""
		if len(part.Images) > 0 {
			imgURL = part.Images[0]
		}

		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:         "tme",
			ProviderID:          part.Symbol,
			Name:                part.Description,
			Description:         part.Description,
			Category:            part.CategoryName,
			Manufacturer:        part.ProducerFullName,
			MPN:                 part.Symbol,
			PreviewImageURL:     imgURL,
			ManufacturingStatus: part.Status,
			ProviderURL:         fmt.Sprintf("https://www.tme.eu/en/details/%s/", part.Symbol),
			Footprint:           "",
		})
	}
	return results
}

func (p *Provider) productToDetail(product tmeProduct, prices []tmePrice, files []tmeFile) *suppliers.PartDetailDTO {
	imgURL := ""
	if len(product.Images) > 0 {
		imgURL = product.Images[0]
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "tme",
			ProviderID:      product.Symbol,
			Name:            product.Description,
			Description:     product.Description,
			Category:        product.CategoryName,
			Manufacturer:    product.ProducerFullName,
			MPN:             product.Symbol,
			PreviewImageURL: imgURL,
			ProviderURL:     fmt.Sprintf("https://www.tme.eu/en/details/%s/", product.Symbol),
			Footprint:       "",
		},
	}

	if imgURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  imgURL,
			Name: product.Symbol + ".jpg",
		})
	}

	// Files (datasheets, etc.)
	for _, file := range files {
		if strings.Contains(file.Type, "Datasheet") || strings.Contains(file.Type, "PDF") {
			detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
				URL:  file.URL,
				Name: file.Name,
			})
		} else if strings.Contains(file.Type, "Image") || strings.Contains(file.Type, "Photo") {
			detail.Images = append(detail.Images, suppliers.FileDTO{
				URL:  file.URL,
				Name: file.Name,
			})
		}
	}

	// Pricing
	if len(prices) > 0 {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "TME",
			OrderNumber:     product.Symbol,
			ProductURL:      fmt.Sprintf("https://www.tme.eu/en/details/%s/", product.Symbol),
		}
		for _, price := range prices {
			vi.Prices = append(vi.Prices, suppliers.PriceDTO{
				MinQuantity:          price.Amount,
				Price:                fmt.Sprintf("%.4f", price.PriceValue),
				Currency:             price.Currency,
				IncludesTax:          false,
				PriceRelatedQuantity: 1,
			})
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
	if product.Datasheet != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Datasheet",
			ValueText: product.Datasheet,
			Group:     "Documentation",
		})
	}

	return detail
}

// TME API types

type tmeSearchResponse struct {
	Data []tmeProduct `json:"Data"`
}

type tmeDetailResponse struct {
	Data []tmeProduct `json:"Data"`
}

type tmePricesResponse struct {
	Data []tmeProductPrices `json:"Data"`
}

type tmeFilesResponse struct {
	Data []tmeProductFiles `json:"Data"`
}

type tmeProduct struct {
	Symbol            string   `json:"Symbol"`
	Description       string   `json:"Description"`
	CategoryName      string   `json:"CategoryFullName"`
	ProducerFullName  string   `json:"ProducerFullName"`
	Status            string   `json:"ProductionStatus"`
	Images            []string `json:"Images"`
	Package           string   `json:"Package"`
	Datasheet         string   `json:"DatasheetUrl"`
}

type tmeProductPrices struct {
	Prices []tmePrice `json:"Prices"`
}

type tmePrice struct {
	Amount     int     `json:"Amount"`
	PriceValue float64 `json:"PriceValue"`
	Currency   string  `json:"Currency"`
}

type tmeProductFiles struct {
	Files []tmeFile `json:"Files"`
}

type tmeFile struct {
	Name string `json:"FileName"`
	Type string `json:"FileType"`
	URL  string `json:"Url"`
}
