package spotlight

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const baseURL = "https://www.spotlightstores.com"

type Provider struct {
	helperPath string
	pythonPath string
	workDir    string
}

func init() {
	suppliers.Register(NewProvider())
}

func NewProvider() *Provider {
	return NewProviderWithPaths("", "")
}

func NewProviderWithPaths(helperPath, pythonPath string) *Provider {
	if helperPath == "" {
		helperPath = "/wledger/scripts/spotlight_helper.py"
	}
	if pythonPath == "" {
		pythonPath = "python3"
	}
	return &Provider{
		helperPath: helperPath,
		pythonPath: pythonPath,
		workDir:    "/wledger",
	}
}

// WithWorkDir sets the working directory for the helper process (used in tests).
func (p *Provider) WithWorkDir(dir string) *Provider {
	p.workDir = dir
	return p
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "spotlight",
		Name:         "Spotlight Stores",
		BaseURL:      baseURL,
		SupportsAuth: false,
		AuthType:     "scraping",
	}
}

func (p *Provider) IsActive() bool {
	return true
}

func (p *Provider) GetCapabilities() []suppliers.Capability {
	return []suppliers.Capability{
		suppliers.CapBasic,
		suppliers.CapPicture,
		suppliers.CapPrice,
		suppliers.CapDistributor,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return domain == "spotlightstores.com" || domain == "www.spotlightstores.com"
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if u.Host != "spotlightstores.com" && u.Host != "www.spotlightstores.com" {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for _, part := range parts {
		// Product codes start with BP followed by digits, may have -color suffix
		upper := strings.ToUpper(part)
		if strings.HasPrefix(upper, "BP") {
			// Strip color suffix (e.g., "BP80558787-SAGE" -> "BP80558787")
			if idx := strings.Index(upper, "-"); idx > 0 {
				return upper[:idx], true
			}
			return upper, true
		}
	}
	return "", false
}

func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, fmt.Errorf("empty search keyword")
	}

	results, err := p.runHelper(ctx, "search", keyword, "--pages", "1")
	if err != nil {
		return nil, err
	}

	var helperResp helperSearchResponse
	if err := json.Unmarshal(results, &helperResp); err != nil {
		return nil, fmt.Errorf("failed to parse helper response: %w", err)
	}
	if !helperResp.OK {
		return nil, fmt.Errorf("spotlight: %s", helperResp.Error)
	}

	var searchResults []suppliers.SearchResultDTO
	for _, item := range helperResp.Results {
		if item.Name == "" || item.URL == "" {
			continue
		}
		searchResults = append(searchResults, suppliers.SearchResultDTO{
			ProviderKey:     "spotlight",
			ProviderID:      item.ID,
			Name:            item.Name,
			Description:     item.PriceText,
			Category:        item.Category,
			PreviewImageURL: item.Image,
		})
	}

	if len(searchResults) == 0 {
		return nil, fmt.Errorf("no Spotlight products found for %q", keyword)
	}
	return searchResults, nil
}

func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	// Try to construct the full product URL with variant slug
	// Search results include the full URL with variant, but we only have the base product ID
	// Use the base URL pattern; the helper will follow redirects
	productURL := baseURL + "/en-au/p/" + providerID
	results, err := p.runHelper(ctx, "product", productURL)
	if err != nil {
		return nil, err
	}

	var helperResp helperProductResponse
	if err := json.Unmarshal(results, &helperResp); err != nil {
		return nil, fmt.Errorf("failed to parse helper response: %w", err)
	}
	if !helperResp.OK {
		return nil, fmt.Errorf("spotlight: %s", helperResp.Error)
	}

	item := helperResp.Product
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "spotlight",
			ProviderID:      item.SKU,
			Name:            item.Name,
			Description:     item.Description,
			Category:        item.Category,
			Manufacturer:    item.Brand,
			MPN:             item.SKU,
			PreviewImageURL: firstImage(item.Images),
			ProviderURL:     item.URL,
		},
		Notes: item.Description,
	}

	for _, imgURL := range item.Images {
		detail.Images = append(detail.Images, suppliers.FileDTO{
			URL:  imgURL,
			Name: item.SKU + ".jpg",
		})
	}

	// Determine stock status - for metered products, stockLevel > 0 means in stock
	inStock := isInStock(item.Availability, item.Unit)

	vi := suppliers.PurchaseInfoDTO{
		DistributorName: "Spotlight Stores",
		OrderNumber:     item.SKU,
		ProductURL:      item.URL,
		Currency:        item.Currency,
		MinimumOrderQty: "1",
		InStock:         inStock,
	}

	priceStr := formatPrice(item.FullPrice, item.SalePrice, item.VIPPrice, item.Unit)
	if priceStr != "" {
		vi.Price = priceStr
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                priceStr,
			Currency:             item.Currency,
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
	}

	if item.VIPPrice != nil {
		vipStr := formatPrice(item.VIPPrice, nil, nil, item.Unit)
		if vipStr != "" {
			vi.Prices = append(vi.Prices, suppliers.PriceDTO{
				MinQuantity:          1,
				Price:                vipStr,
				Currency:             item.Currency,
				IncludesTax:          true,
				PriceRelatedQuantity: 1,
			})
		}
	}

	detail.VendorInfos = append(detail.VendorInfos, vi)

	if item.EAN != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Barcode (GTIN)",
			ValueText: item.EAN,
			Group:     "General",
		})
	}

	for name, value := range item.Specifications {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      name,
			ValueText: value,
			Group:     "Specifications",
		})
	}

	return detail, nil
}

func isInStock(availability, unit string) bool {
	availLower := strings.ToLower(availability)
	if strings.Contains(availLower, "instock") || strings.Contains(availLower, "in stock") {
		return true
	}
	if strings.Contains(availLower, "outofstock") || strings.Contains(availLower, "out of stock") {
		return false
	}
	// For metered products (per metre), check if it's purchasable
	if unit == "per metre" || unit == "per meter" {
		return true // Metered products are typically available by the metre
	}
	return true // Default to in stock
}

func (p *Provider) runHelper(ctx context.Context, args ...string) ([]byte, error) {
	cmdArgs := append([]string{p.helperPath}, args...)
	cmd := exec.CommandContext(ctx, p.pythonPath, cmdArgs...)
	cmd.Dir = p.workDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start helper: %w", err)
	}

	stdoutBytes, _ := io.ReadAll(stdout)
	stderrBytes, _ := io.ReadAll(stderr)

	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("helper failed: %w, stderr: %s", err, string(stderrBytes))
	}

	return stdoutBytes, nil
}

type helperSearchResponse struct {
	OK      bool              `json:"ok"`
	Error   string            `json:"error"`
	Results []helperSearchItem `json:"results"`
}

type helperSearchItem struct {
	ID          string   `json:"id"`
	SKU         string   `json:"sku"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Image       string   `json:"image"`
	FullPrice   *float64 `json:"full_price"`
	SalePrice   *float64 `json:"sale_price"`
	VIPPrice    *float64 `json:"vip_price"`
	Currency    string   `json:"currency"`
	PriceText   string   `json:"price_text"`
	Unit        string   `json:"unit"`
	Category    string   `json:"category"`
}

type helperProductResponse struct {
	OK      bool             `json:"ok"`
	Error   string           `json:"error"`
	Product helperProductItem `json:"product"`
}

type helperProductItem struct {
	ID              string            `json:"id"`
	SKU             string            `json:"sku"`
	Name            string            `json:"name"`
	URL             string            `json:"url"`
	Images          []string          `json:"images"`
	FullPrice       *float64          `json:"full_price"`
	SalePrice       *float64          `json:"sale_price"`
	VIPPrice        *float64          `json:"vip_price"`
	Currency        string            `json:"currency"`
	PriceText       string            `json:"price_text"`
	Unit            string            `json:"unit"`
	Availability    string            `json:"availability"`
	Category        string            `json:"category"`
	Description     string            `json:"description"`
	Specifications  map[string]string `json:"specifications"`
	EAN             string            `json:"ean"`
	Rating          *float64          `json:"rating"`
	ReviewCount     *int              `json:"review_count"`
	Brand           string            `json:"brand"`
}

func formatPrice(full, sale, vip *float64, unit string) string {
	if full != nil {
		s := fmt.Sprintf("%.2f", *full)
		if unit != "" && unit != "each" {
			s += " " + unit
		}
		return s
	}
	if sale != nil {
		s := fmt.Sprintf("%.2f", *sale)
		if unit != "" && unit != "each" {
			s += " " + unit
		}
		return s
	}
	if vip != nil {
		s := fmt.Sprintf("%.2f", *vip)
		if unit != "" && unit != "each" {
			s += " " + unit
		}
		return s
	}
	return ""
}

func firstImage(images []string) string {
	if len(images) > 0 {
		return images[0]
	}
	return ""
}