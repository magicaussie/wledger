// Package amazon implements the suppliers.Provider interface for
// amazon.com.au. Amazon does not offer a publicly accessible product search
// API to this application, so results are scraped from the regular product
// pages using the amzpy Python package (curl_cffi browser impersonation).
//
// The Go provider shells out to a small Python helper script that handles the
// HTTP fetching and HTML parsing. The helper is required at runtime; its
// location is controlled by the AMAZON_HELPER_PATH environment variable and
// defaults to /wledger/scripts/amazon_helper.py. An optional AMAZON_PROXY_URL
// environment variable is forwarded to the helper as a proxy for outgoing
// requests.
package amazon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	key     = "amazon"
	baseURL = "https://www.amazon.com.au"
	country = "com.au"

	defaultHelperPath = "/wledger/scripts/amazon_helper.py"

	searchCacheTTL = 15 * time.Minute
	detailCacheTTL = 30 * time.Minute
	execTimeout    = 120 * time.Second
)

var (
	asinRe      = regexp.MustCompile(`^[A-Z0-9]{10}$`)
	asinInURLRe = regexp.MustCompile(`/(?:dp|gp/product|gp/aw/d|aw/d|product)/([A-Z0-9]{10})(?:[/?#]|$)|[?&]asin=([A-Z0-9]{10})`)
)

// Provider implements the suppliers.Provider interface for Amazon.com.au.
type Provider struct {
	helperPath string
	pythonPath string
	proxyURL   string

	mu           sync.Mutex
	searchCache  map[string]cacheEntry
	detailCache  map[string]cacheEntry
}

type cacheEntry struct {
	data      []byte
	expiresAt time.Time
}

func init() {
	suppliers.Register(NewProvider())
}

// NewProvider creates an Amazon provider, reading configuration from the
// environment.
func NewProvider() *Provider {
	return NewProviderWithPaths(os.Getenv("AMAZON_HELPER_PATH"), os.Getenv("AMAZON_PYTHON"))
}

// NewProviderWithPaths creates an Amazon provider with explicit helper and
// Python binary paths (used by tests). Empty helperPath falls back to the
// default; empty pythonPath defaults to "python3".
func NewProviderWithPaths(helperPath, pythonPath string) *Provider {
	if helperPath == "" {
		helperPath = defaultHelperPath
	}
	if pythonPath == "" {
		pythonPath = "python3"
	}
	return &Provider{
		helperPath:  helperPath,
		pythonPath:  pythonPath,
		proxyURL:    os.Getenv("AMAZON_PROXY_URL"),
		searchCache: make(map[string]cacheEntry),
		detailCache: make(map[string]cacheEntry),
	}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          key,
		Name:         "Amazon",
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

// SearchCacheTTL keeps Amazon search results fresh for 15 minutes so that
// repeated searches don't hammer Amazon with traffic.
func (p *Provider) SearchCacheTTL() time.Duration {
	return searchCacheTTL
}

// DetailCacheTTL keeps Amazon product details fresh for 30 minutes.
func (p *Provider) DetailCacheTTL() time.Duration {
	return detailCacheTTL
}

// SearchByKeyword searches Amazon.com.au. A keyword that is already a valid
// ASIN is looked up directly as a product instead of being sent through search.
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, fmt.Errorf("[AMAZON] empty search keyword")
	}

	if asinRe.MatchString(keyword) {
		detail, err := p.GetDetails(ctx, keyword)
		if err != nil {
			return nil, err
		}
		return []suppliers.SearchResultDTO{detail.SearchResultDTO}, nil
	}

	if cached := p.getCachedSearch(keyword); cached != nil {
		return cached, nil
	}

	resp, err := p.run(ctx, "search", keyword)
	if err != nil {
		return nil, err
	}

	results := make([]suppliers.SearchResultDTO, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.ASIN == "" || r.Title == "" {
			continue
		}
		result := suppliers.SearchResultDTO{
			ProviderKey:     key,
			ProviderID:      r.ASIN,
			Name:            r.Title,
			PreviewImageURL: r.ImgURL,
			ProviderURL:     r.URL,
		}
		if r.Brand != "" {
			result.Manufacturer = r.Brand
		}
		results = append(results, result)
	}

	p.setCachedSearch(keyword, results)
	return results, nil
}

// GetDetails fetches the product page for a single ASIN.
func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	providerID = strings.TrimSpace(providerID)
	if !asinRe.MatchString(providerID) {
		return nil, fmt.Errorf("[AMAZON] invalid ASIN %q", providerID)
	}

	if cached := p.getCachedDetail(providerID); cached != nil {
		return cached, nil
	}

	resp, err := p.run(ctx, "product", providerID)
	if err != nil {
		return nil, err
	}

	d := resp.Product
	if d.ASIN == "" {
		return nil, fmt.Errorf("[AMAZON] product response missing ASIN")
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     key,
			ProviderID:      d.ASIN,
			Name:            d.Title,
			Manufacturer:    cleanAmazonBrand(d.Brand),
			Description:     strings.Join(d.Bullets, "\n"),
			PreviewImageURL: d.ImgURL,
			ProviderURL:     d.URL,
		},
		Notes: strings.Join(d.Bullets, "\n"),
	}

	if d.ImgURL != "" {
		detail.Images = append(detail.Images, suppliers.FileDTO{URL: d.ImgURL, Name: d.ASIN + ".jpg"})
	}

	for _, s := range d.Specs {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      s.Name,
			ValueText: s.Value,
			Group:     "Specifications",
		})
	}

	price := formatPrice(d.Price)
	currency := d.Currency
	if currency == "" {
		currency = "AUD"
	}
	inStock := d.InStock
	if !inStock {
		if av := strings.ToLower(d.Availability); av != "" &&
			!strings.Contains(av, "out of stock") && !strings.Contains(av, "currently unavailable") {
			inStock = true
		}
	}

	if price != "" {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Amazon",
			OrderNumber:     d.ASIN,
			Prices: []suppliers.PriceDTO{
				{
					MinQuantity:          1,
					Price:                price,
					Currency:             currency,
					IncludesTax:          true,
					PriceRelatedQuantity: 1,
				},
			},
			ProductURL:      d.URL,
			Price:           price,
			Currency:        currency,
			MinimumOrderQty: "1",
			InStock:         inStock,
		}
		detail.VendorInfos = append(detail.VendorInfos, vi)
	}

	p.setCachedDetail(providerID, detail)
	return detail, nil
}

// HandlesDomain reports whether the provider can handle the given Amazon domain.
func (p *Provider) HandlesDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	return domain == "amazon.com.au" || domain == "www.amazon.com.au"
}

// ExtractPartIDFromURL extracts an ASIN from an Amazon product URL. Only known
// Amazon product URL patterns are matched so that arbitrary 10-character
// alphanumeric strings elsewhere in a URL are not mistaken for ASINs.
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	for _, m := range asinInURLRe.FindAllStringSubmatch(rawURL, -1) {
		if m[1] != "" {
			return m[1], true
		}
		if len(m) > 2 && m[2] != "" {
			return m[2], true
		}
	}
	return "", false
}

// run executes the helper with the given command and decodes its JSON response.
// The helper prints exactly one JSON object to stdout; diagnostics go to stderr.
func (p *Provider) run(ctx context.Context, command, arg string) (*helperResponse, error) {
	if _, err := os.Stat(p.helperPath); err != nil {
		return nil, fmt.Errorf("[AMAZON] helper not found at %s (set AMAZON_HELPER_PATH): %w", p.helperPath, err)
	}

	runCtx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	args := []string{p.helperPath, command, arg, "--country", country}
	if p.proxyURL != "" {
		args = append(args, "--proxy-url", p.proxyURL)
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(runCtx, p.pythonPath, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if runCtx.Err() != nil {
			return nil, fmt.Errorf("[AMAZON] helper timed out: %w", runCtx.Err())
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("[AMAZON] helper exited with %d: %s", ee.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("[AMAZON] helper failed: %w", err)
	}

	var resp helperResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("[AMAZON] invalid helper output: %w", err)
	}

	// The helper reports business failures (captcha, empty results, parse
	// failures) as a well-formed JSON object with ok=false.
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "request failed"
		}
		return &resp, fmt.Errorf("[AMAZON] %s", resp.Error)
	}
	return &resp, nil
}

func (p *Provider) getCachedSearch(keyword string) []suppliers.SearchResultDTO {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.searchCache[keyword]
	if !ok || time.Now().After(e.expiresAt) {
		return nil
	}
	var results []suppliers.SearchResultDTO
	if err := json.Unmarshal(e.data, &results); err != nil {
		return nil
	}
	return results
}

func (p *Provider) setCachedSearch(keyword string, results []suppliers.SearchResultDTO) {
	data, _ := json.Marshal(results)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.searchCache[keyword] = cacheEntry{data: data, expiresAt: time.Now().Add(searchCacheTTL)}
}

func (p *Provider) getCachedDetail(providerID string) *suppliers.PartDetailDTO {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.detailCache[providerID]
	if !ok || time.Now().After(e.expiresAt) {
		return nil
	}
	var detail suppliers.PartDetailDTO
	if err := json.Unmarshal(e.data, &detail); err != nil {
		return nil
	}
	return &detail
}

func (p *Provider) setCachedDetail(providerID string, detail *suppliers.PartDetailDTO) {
	data, _ := json.Marshal(detail)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.detailCache[providerID] = cacheEntry{data: data, expiresAt: time.Now().Add(detailCacheTTL)}
}

// formatPrice renders a float price without trailing zeros (e.g. 25.95, 12.0 -> 12).
func formatPrice(price float64) string {
	if price <= 0 {
		return ""
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", price), "0"), ".")
}

// cleanAmazonBrand normalises the brand extracted from the Amazon byline.
// For books and non-branded listings the #bylineInfo element contains the
// author/format text (e.g. "by Sydney Dumore (Author)... Format: Paperback"),
// which is not a brand. Keep only a genuine brand or return "".
func cleanAmazonBrand(brand string) string {
	brand = strings.TrimSpace(brand)
	if brand == "" {
		return ""
	}
	lower := strings.ToLower(brand)
	if strings.HasPrefix(lower, "by ") {
		return ""
	}
	if strings.Contains(lower, "(author)") || strings.Contains(lower, "format:") {
		return ""
	}
	// Long free-text bylines without a "visit the X store" marker are authors,
	// not brands.
	if len(brand) > 40 && !strings.Contains(lower, "visit the ") {
		return ""
	}
	return brand
}

// helperResponse is the outer JSON envelope produced by amazon_helper.py.
// The "search" command populates Results; the "product" command populates
// Product.
type helperResponse struct {
	OK      bool          `json:"ok"`
	Error   string        `json:"error"`
	Results []amazonItem  `json:"results"`
	Product amazonProduct `json:"product"`
}

type amazonItem struct {
	ASIN    string  `json:"asin"`
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Price   float64 `json:"price"`
	Currency string `json:"currency"`
	ImgURL  string  `json:"img_url"`
	Rating  float64 `json:"rating"`
	Brand   string  `json:"brand"`
}

type amazonProduct struct {
	ASIN         string       `json:"asin"`
	Title        string       `json:"title"`
	URL          string       `json:"url"`
	Price        float64      `json:"price"`
	Currency     string       `json:"currency"`
	ImgURL       string       `json:"img_url"`
	Brand        string       `json:"brand"`
	Rating       float64      `json:"rating"`
	ReviewCount  int          `json:"review_count"`
	Bullets      []string     `json:"bullets"`
	Specs        []amazonSpec `json:"specs"`
	Availability string       `json:"availability"`
	InStock      bool         `json:"in_stock"`
}

type amazonSpec struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
