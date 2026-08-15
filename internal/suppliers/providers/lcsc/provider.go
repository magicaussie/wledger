package lcsc

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
	baseURL       = "https://wmsc.lcsc.com"
	searchURL     = baseURL + "/ftps/wm/product/query/list"
	detailURL     = baseURL + "/ftps/wm/product/detail"
	homepageURL   = "https://www.lcsc.com"
)

// Provider implements the suppliers.Provider interface for LCSC Electronics.
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
		Key:          "lcsc",
		Name:         "LCSC Electronics",
		BaseURL:      homepageURL,
		SupportsAuth: false,
		AuthType:     "none",
	}
}

func (p *Provider) IsActive() bool {
	return true
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
	return domain == "lcsc.com" || domain == "www.lcsc.com"
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	// URL format: https://www.lcsc.com/product-detail/XXXXX.html
	// or: https://www.lcsc.com/bom/XXXXX.html
	// Extract the product code from the URL path
	parts := strings.Split(rawURL, "/")
	for _, part := range parts {
		part = strings.TrimPrefix(part, "product-detail/")
		part = strings.TrimSuffix(part, ".html")
		if strings.HasPrefix(part, "C") && len(part) > 1 {
			return part, true
		}
	}
	// Try query parameter approach
	if idx := strings.Index(rawURL, "productCode="); idx != -1 {
		code := rawURL[idx+12:]
		if ampIdx := strings.Index(code, "&"); ampIdx != -1 {
			code = code[:ampIdx]
		}
		if code != "" {
			return code, true
		}
	}
	return "", false
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	body := lcscSearchRequest{
		Keyword:     keyword,
		CurrentPage: 1,
		PageSize:    50,
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
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.lcsc.com/")
	req.Header.Set("Origin", "https://www.lcsc.com")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LCSC search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LCSC API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp lcscSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode LCSC search response: %w", err)
	}

	return p.partsToSearchResults(apiResp.Result.DataList), nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	url := fmt.Sprintf("%s?productCode=%s", detailURL, providerID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.lcsc.com/")
	req.Header.Set("Origin", "https://www.lcsc.com")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("LCSC detail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LCSC API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var apiResp lcscDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("failed to decode LCSC detail response: %w", err)
	}

	if apiResp.Result == nil {
		return nil, fmt.Errorf("no product found with code %s", providerID)
	}

	return p.productToDetail(apiResp.Result), nil
}

func (p *Provider) partsToSearchResults(parts []lcscProduct) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(parts))
	for _, part := range parts {
		imgURL := part.ProductImageURL
		if imgURL != "" && !strings.HasPrefix(imgURL, "http") {
			imgURL = "https:" + imgURL
		}

		desc := part.ProductDescEn
		if desc == "" {
			desc = part.ProductIntroEn
		}

		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:         "lcsc",
			ProviderID:          part.ProductCode,
			Name:                part.ProductNameEn,
			Description:         desc,
			Category:            part.WmCatalogNameEn,
			Manufacturer:        part.BrandNameEn,
			MPN:                 part.ProductModel,
			PreviewImageURL:     imgURL,
			ManufacturingStatus: "",
			ProviderURL:         fmt.Sprintf("%s/product-detail/%s.html", homepageURL, part.ProductCode),
			Footprint:           part.EncapStandard,
		})
	}
	return results
}

func (p *Provider) productToDetail(product *lcscProduct) *suppliers.PartDetailDTO {
	imgURL := product.ProductImageURL
	if imgURL != "" && !strings.HasPrefix(imgURL, "http") {
		imgURL = "https:" + imgURL
	}

	desc := product.ProductDescEn
	if desc == "" {
		desc = product.ProductIntroEn
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "lcsc",
			ProviderID:      product.ProductCode,
			Name:            product.ProductNameEn,
			Description:     desc,
			Category:        product.WmCatalogNameEn,
			Manufacturer:    product.BrandNameEn,
			MPN:             product.ProductModel,
			PreviewImageURL: imgURL,
			ProviderURL:     fmt.Sprintf("%s/product-detail/%s.html", homepageURL, product.ProductCode),
			Footprint:       product.EncapStandard,
		},
	}

	if imgURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  imgURL,
			Name: product.ProductCode + ".jpg",
		})
	}

	if product.PdfURL != "" {
		dsURL := product.PdfURL
		if !strings.HasPrefix(dsURL, "http") {
			dsURL = "https:" + dsURL
		}
		detail.Datasheets = append(detail.Datasheets, suppliers.FileDTO{
			URL:  dsURL,
			Name: product.ProductCode + "_datasheet.pdf",
		})
	}

	if len(product.ProductPriceList) > 0 {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "LCSC Electronics",
			OrderNumber:     product.ProductCode,
			ProductURL:      fmt.Sprintf("%s/product-detail/%s.html", homepageURL, product.ProductCode),
		}
		for _, price := range product.ProductPriceList {
			vi.Prices = append(vi.Prices, suppliers.PriceDTO{
				MinQuantity:          price.Ladder,
				Price:                fmt.Sprintf("%.4f", price.UsdPrice),
				Currency:             price.CurrencySymbol,
				IncludesTax:          false,
				PriceRelatedQuantity: 1,
			})
		}
		detail.VendorInfos = append(detail.VendorInfos, vi)
	}

	if product.EncapStandard != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Package",
			ValueText: product.EncapStandard,
			Group:     "Physical",
		})
	}
	if product.ProductWeight != 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Weight",
			ValueText: fmt.Sprintf("%g", product.ProductWeight),
			Group:     "Physical",
		})
	}
	if desc != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Description",
			ValueText: desc,
			Group:     "General",
		})
	}

	for _, param := range product.ParamVOList {
		if param.ParamNameEn != "" {
			detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
				Name:      param.ParamNameEn,
				ValueText: param.ParamValueEn,
				Group:     "Electrical",
			})
		}
	}

	return detail
}

// LCSC API types

type lcscSearchRequest struct {
	Keyword     string `json:"keyword"`
	CurrentPage int    `json:"currentPage"`
	PageSize    int    `json:"pageSize"`
}

type lcscSearchResponse struct {
	Result lcscSearchResult `json:"result"`
}

type lcscSearchResult struct {
	DataList []lcscProduct `json:"dataList"`
	TotalRow int           `json:"totalRow"`
}

type lcscProduct struct {
	ProductId        int          `json:"productId"`
	ProductCode      string       `json:"productCode"`
	ProductNameEn    string       `json:"productNameEn"`
	ProductDescEn    string       `json:"productDescEn"`
	ProductIntroEn   string       `json:"productIntroEn"`
	ProductModel     string       `json:"productModel"`
	BrandNameEn      string       `json:"brandNameEn"`
	WmCatalogNameEn  string       `json:"wmCatalogNameEn"`
	ProductImageURL  string       `json:"productImageUrl"`
	PdfURL           string       `json:"pdfUrl"`
	EncapStandard    string       `json:"encapStandard"`
	ProductWeight    float64      `json:"productWeight"`
	StockNumber      int64        `json:"stockNumber"`
	ProductPriceList []lcscPrice  `json:"productPriceList"`
	ParamVOList      []lcscParam  `json:"paramVOList"`
}

type lcscParam struct {
	ParamNameEn string `json:"paramNameEn"`
	ParamValueEn string `json:"paramValueEn"`
}

type lcscPrice struct {
	Ladder       int     `json:"ladder"`
	ProductPrice string  `json:"productPrice"`
	UsdPrice     float64 `json:"usdPrice"`
	CurrencySymbol string `json:"currencySymbol"`
}

type lcscDetailResponse struct {
	Result *lcscProduct `json:"result"`
}
