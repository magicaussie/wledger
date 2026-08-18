// Package aliexpress implements the suppliers.Provider interface for AliExpress
// (aliexpress.com), a Chinese e-commerce marketplace.
//
// AliExpress serves an x5sec/anti-bot challenge to plain HTTP clients, so the
// provider shells out to scripts/aliexpress_helper.mjs, which uses Puppeteer
// + puppeteer-extra-plugin-stealth against the system Chromium and reads the
// page's own JSON API responses (the same technique as the well-known
// aliexpress-product-scraper project). Product detail pages are reliable;
// search pages are intermittently gated by a CAPTCHA and are retried a few
// times before returning a clean "blocked" error.
package aliexpress

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const baseURL = "https://www.aliexpress.com"

// Provider implements the suppliers.Provider interface for AliExpress.
type Provider struct {
	nodePath    string
	helperPath  string
	workDir     string
	helperEnv   []string
	searchRetry int
}

func init() {
	suppliers.Register(NewProvider())
}

// NewProvider creates an AliExpress provider using the bundled Node helper.
func NewProvider() *Provider {
	return &Provider{
		nodePath:    "node",
		helperPath:  "/wledger/scripts/aliexpress/aliexpress_helper.mjs",
		workDir:     "/wledger/scripts/aliexpress",
		searchRetry: 4,
	}
}

// NewProviderWithPaths allows overriding the node/helper paths (used by tests).
func NewProviderWithPaths(nodePath, helperPath, workDir string) *Provider {
	p := NewProvider()
	if nodePath != "" {
		p.nodePath = nodePath
	}
	if helperPath != "" {
		p.helperPath = helperPath
	}
	if workDir != "" {
		p.workDir = workDir
	}
	return p
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "aliexpress",
		Name:         "AliExpress",
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
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return domain == "aliexpress.com" || strings.HasSuffix(domain, ".aliexpress.com")
}

func (p *Provider) SearchCacheTTL() time.Duration { return 30 * time.Minute }
func (p *Provider) DetailCacheTTL() time.Duration { return 60 * time.Minute }

// ExtractPartIDFromURL extracts the numeric product id from an AliExpress URL.
// e.g. https://www.aliexpress.com/item/1005002935037572.html -> "1005002935037572"
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if !p.HandlesDomain(u.Host) {
		return "", false
	}
	// /item/<id>.html
	parts := strings.Split(strings.Trim(strings.TrimPrefix(u.Path, "/item/"), "/"), "/")
	if len(parts) == 0 {
		return "", false
	}
	id := strings.TrimSuffix(parts[0], ".html")
	if id == "" || !isNumeric(id) {
		return "", false
	}
	return id, true
}

// SearchByKeyword searches AliExpress via the Node helper (with retries).
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, fmt.Errorf("empty search keyword")
	}

	out, err := p.runHelper(ctx, "search", keyword, "--retries", strconv.Itoa(p.searchRetry))
	if err != nil {
		return nil, err
	}

	var resp helperResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse aliexpress helper response: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("aliexpress: %s", resp.Error)
	}

	var results []suppliers.SearchResultDTO
	for _, r := range resp.Results {
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "aliexpress",
			ProviderID:      r.ID,
			Name:            r.Name,
			Description:     r.priceText(),
			Manufacturer:    "",
			MPN:             "",
			PreviewImageURL: r.Image,
			ProviderURL:     r.URL,
		})
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no AliExpress products found for %q", keyword)
	}
	return results, nil
}

// GetDetails fetches an AliExpress product page via the Node helper.
func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return nil, fmt.Errorf("invalid AliExpress product id %q", providerID)
	}

	out, err := p.runHelper(ctx, "product", providerID, "--retries", "3")
	if err != nil {
		return nil, err
	}

	var resp helperResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse aliexpress helper response: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("aliexpress: %s", resp.Error)
	}

	pr := resp.Product
	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "aliexpress",
			ProviderID:      pr.ID,
			Name:            pr.Title,
			Category:        "",
			PreviewImageURL: firstImage(pr.Images),
			ProviderURL:     pr.URL,
		},
	}

	for _, img := range pr.Images {
		detail.Images = append(detail.Images, suppliers.FileDTO{URL: img, Name: pr.ID + ".jpg"})
	}

	vi := suppliers.PurchaseInfoDTO{
		DistributorName: "AliExpress",
		OrderNumber:     pr.ID,
		ProductURL:      pr.URL,
		Currency:        pr.Currency,
		MinimumOrderQty: "1",
		InStock:         true,
	}
	if pr.SalePrice != nil {
		vi.Price = fmt.Sprintf("%.2f", *pr.SalePrice)
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                fmt.Sprintf("%.2f", *pr.SalePrice),
			Currency:             pr.Currency,
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
	}
	if pr.OriginalPrice != nil && (vi.Price == "" || *pr.OriginalPrice != *pr.SalePrice) {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Was price",
			ValueText: fmt.Sprintf("%.2f", *pr.OriginalPrice),
			Group:     "Pricing",
		})
	}
	detail.VendorInfos = append(detail.VendorInfos, vi)

	if pr.Rating != nil {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Rating",
			ValueText: fmt.Sprintf("%.1f / 5", *pr.Rating),
			Group:     "Ratings",
		})
	}
	if pr.RatingCount != nil {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Rating count",
			ValueText: strconv.Itoa(*pr.RatingCount),
			Group:     "Ratings",
		})
	}
	if pr.StoreName != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Store",
			ValueText: pr.StoreName,
			Group:     "General",
		})
	}
	for _, s := range pr.Specs {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      s.Name,
			ValueText: s.Value,
			Group:     "Specifications",
		})
	}

	return detail, nil
}

// runHelper executes the Node helper and returns its stdout.
func (p *Provider) runHelper(ctx context.Context, args ...string) ([]byte, error) {
	cmdArgs := append([]string{p.helperPath}, args...)
	cmd := exec.CommandContext(ctx, p.nodePath, cmdArgs...)
	if p.workDir != "" {
		cmd.Dir = p.workDir
	}
	if p.helperEnv != nil {
		cmd.Env = append(cmd.Env, p.helperEnv...)
	}
	return cmd.Output()
}

// JSON wire types

type helperResponse struct {
	OK      bool           `json:"ok"`
	Error   string         `json:"error"`
	Results []helperResult `json:"results"`
	Product helperProduct  `json:"product"`
}

type helperResult struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	SalePrice   *float64 `json:"sale_price"`
	SaleText    string  `json:"sale_price_text"`
	Currency    string  `json:"currency"`
	Image       string  `json:"image"`
	URL         string  `json:"url"`
	Rating      *float64 `json:"rating"`
}

func (r helperResult) priceText() string {
	if r.SaleText != "" {
		return r.SaleText
	}
	if r.SalePrice != nil {
		return fmt.Sprintf("%.2f %s", *r.SalePrice, r.Currency)
	}
	return ""
}

type helperProduct struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Images        []string         `json:"images"`
	SalePrice     *float64         `json:"sale_price"`
	OriginalPrice *float64         `json:"original_price"`
	Currency      string           `json:"currency"`
	Rating        *float64         `json:"rating"`
	RatingCount   *int             `json:"rating_count"`
	StoreName     string           `json:"store_name"`
	Specs         []helperSpec     `json:"specs"`
	URL           string           `json:"url"`
}

type helperSpec struct {
	Name  string `json:"attrName"`
	Value string `json:"attrValue"`
}

func firstImage(imgs []string) string {
	if len(imgs) > 0 {
		return imgs[0]
	}
	return ""
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
