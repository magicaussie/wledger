// Package autobarn implements the suppliers.Provider interface for
// Autobarn (autobarn.com.au), an Australian automotive accessory retailer.
//
// The storefront is a React Router (Remix) SPA behind Imperva/Incapsula. Its
// product grid is rendered server-side from Algolia (index autobarnProductIndex)
// for both category and search pages, so the catalogue can be parsed from
// ordinary HTML with no browser automation.
package autobarn

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	baseURL      = "https://autobarn.com.au"
	searchPath   = "/ab/search"
	defaultIndex = "autobarnProductIndex"
)

var (
	// productIDRE matches the trailing Product ID after /p/ in a product path.
	productIDRE = regexp.MustCompile(`/p/([A-Za-z0-9]+)(?:[?#]|$)`)
	// ? if the ID chars, e.g. EL05408 or 148260.
	// blockSignals are substrings that indicate an Imperva/Incapsula challenge.
	blockSignals = []string{
		"incapsula",
		"imperva",
		"verify you are human",
		"access denied",
		"attention required",
		"pardon our interruption",
	}
)

// Provider implements the suppliers.Provider interface for Autobarn.
type Provider struct {
	httpClient *http.Client
	appID      string
	searchKey  string
	indexName  string
	ua         string
}

func init() {
	suppliers.Register(NewProvider())
}

// NewProvider creates an Autobarn provider. Algolia credentials are optional:
// the catalogue HTML already contains all needed product data.
func NewProvider() *Provider {
	return NewProviderWithConfig(Config{})
}

// Config holds optional Algolia configuration for Autobarn.
type Config struct {
	AlgoliaAppID     string
	AlgoliaSearchKey string
	AlgoliaIndex     string
}

// NewProviderWithConfig creates an Autobarn provider with the given config.
// Empty Algolia fields are tolerated; the provider reads the catalogue HTML.
func NewProviderWithConfig(cfg Config) *Provider {
	return newProvider(nil, cfg)
}

// NewProviderWithClient creates an Autobarn provider using the given HTTP
// client (used by tests to inject a fake transport).
func NewProviderWithClient(client *http.Client) *Provider {
	return newProvider(client, Config{})
}

func newProvider(client *http.Client, cfg Config) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.AlgoliaIndex == "" {
		cfg.AlgoliaIndex = defaultIndex
	}
	return &Provider{
		httpClient: client,
		appID:      cfg.AlgoliaAppID,
		searchKey:  cfg.AlgoliaSearchKey,
		indexName:  cfg.AlgoliaIndex,
		ua:         "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36",
	}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "autobarn",
		Name:         "Autobarn",
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
	return domain == "autobarn.com.au" || domain == "www.autobarn.com.au"
}

func (p *Provider) SearchCacheTTL() time.Duration { return 15 * time.Minute }
func (p *Provider) DetailCacheTTL() time.Duration { return 45 * time.Minute }

// ExtractPartIDFromURL extracts the Product ID from an Autobarn URL.
// e.g. /ab/Autobarn-Category/Supercharge/...-Car-Battery/p/EL05408 -> "EL05408".
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if u.Host != "autobarn.com.au" && u.Host != "www.autobarn.com.au" {
		return "", false
	}
	m := productIDRE.FindStringSubmatch(u.Path)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// SearchByKeyword searches Autobarn. If Algolia credentials are configured and
// work, direct Algolia search is used; otherwise the server-rendered search
// page HTML is parsed.
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	if p.appID != "" && p.searchKey != "" {
		if hits, err := p.algoliaSearch(ctx, keyword, 20); err == nil {
			results := p.hitsToResults(hits)
			if len(results) > 0 {
				return results, nil
			}
		}
	}

	u, _ := url.Parse(baseURL + searchPath)
	q := u.Query()
	q.Set("text", keyword)
	u.RawQuery = q.Encode()

	doc, _, err := p.fetchDoc(ctx, u.String())
	if err != nil {
		return nil, err
	}
	if err := detectBlock(doc); err != nil {
		return nil, err
	}

	results := parseGrid(doc)
	if len(results) == 0 {
		return nil, fmt.Errorf("no Autobarn products found for %q", keyword)
	}
	return results, nil
}

// GetDetails fetches an Autobarn product page and parses the JSON-LD plus
// specification table.
func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if providerID == "" {
		return nil, fmt.Errorf("invalid Autobarn product ID %q", providerID)
	}

	productURL, err := p.resolveProductURL(ctx, providerID)
	if err != nil {
		return nil, err
	}

	doc, rawBody, err := p.fetchDoc(ctx, productURL)
	if err != nil {
		return nil, err
	}
	if err := detectBlock(doc); err != nil {
		return nil, err
	}

	return p.parseProduct(doc, rawBody, productURL, providerID)
}

// algoliaSearch executes a request against the Autobarn Algolia index.
func (p *Provider) algoliaSearch(ctx context.Context, query string, hitsPerPage int) ([]map[string]any, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("hitsPerPage", fmt.Sprintf("%d", hitsPerPage))
	params.Set("page", "0")

	reqBody := map[string]any{
		"requests": []map[string]any{
			{
				"indexName": p.indexName,
				"params":    params.Encode(),
			},
		},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("https://%s-dsn.algolia.net/1/indexes/*/queries", p.appID)
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Algolia-Application-Id", p.appID)
	req.Header.Set("X-Algolia-API-Key", p.searchKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("autobarn algolia status %d", resp.StatusCode)
	}

	var decoded struct {
		Results []struct {
			Hits []map[string]any `json:"hits"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Results) == 0 {
		return nil, fmt.Errorf("autobarn algolia empty response")
	}
	return decoded.Results[0].Hits, nil
}

// hitsToResults maps Algolia hits to search results.
func (p *Provider) hitsToResults(hits []map[string]any) []suppliers.SearchResultDTO {
	var out []suppliers.SearchResultDTO
	for _, h := range hits {
		id := str(h["objectID"])
		if id == "" {
			id = str(h["productId"])
		}
		name := str(h["name"])
		if name == "" {
			name = str(h["title"])
		}
		if id == "" || name == "" {
			continue
		}
		image := str(h["image"])
		if image == "" {
			image = str(h["imageUrl"])
		}
		brand := str(h["brand"])
		price := fmtPrice(h["price"])

		var desc strings.Builder
		if price != "" {
			desc.WriteString("Price $" + price)
		}
		if was := fmtPrice(h["wasPrice"]); was != "" {
			desc.WriteString(" Was $" + was)
		}
		out = append(out, suppliers.SearchResultDTO{
			ProviderKey:     "autobarn",
			ProviderID:      id,
			Name:            name,
			Description:     strings.TrimSpace(desc.String()),
			Manufacturer:    brand,
			MPN:             str(h["mpn"]),
			PreviewImageURL: image,
			ProviderURL:     autobarnProductURL(id),
		})
	}
	return out
}

// fetchDoc performs a GET request and returns a goquery document plus the
// raw response body (used for streamed JSON extraction).
func (p *Provider) fetchDoc(ctx context.Context, pageURL string) (*goquery.Document, string, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		doc, raw, err := p.fetchDocOnce(ctx, pageURL)
		if err == nil {
			return doc, raw, nil
		}
		lastErr = err
	}
	return nil, "", lastErr
}

func (p *Provider) fetchDocOnce(ctx context.Context, pageURL string) (*goquery.Document, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", p.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-AU,en;q=0.9")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("autobarn request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("autobarn returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 25*1024*1024))
	if err != nil {
		return nil, "", fmt.Errorf("autobarn body read failed: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, "", fmt.Errorf("failed to parse Autobarn page: %w", err)
	}
	return doc, string(body), nil
}

// detectBlock checks for Imperva/Incapsula challenge pages.
func detectBlock(doc *goquery.Document) error {
	var text strings.Builder
	doc.Find("body").Each(func(_ int, s *goquery.Selection) {
		s.Clone().Find("script, style, noscript, template").Remove()
		text.WriteString(s.Text())
	})
	body := strings.ToLower(text.String())
	for _, sig := range blockSignals {
		if strings.Contains(body, sig) {
			return fmt.Errorf("autobarn imperva challenge detected")
		}
	}
	return nil
}

// parseGrid extracts product tiles from a search/category page.
func parseGrid(doc *goquery.Document) []suppliers.SearchResultDTO {
	var results []suppliers.SearchResultDTO
	doc.Find(".listing-product-tile").Each(func(_ int, s *goquery.Selection) {
		id := s.AttrOr("data-insights-object-id", "")
		if id == "" {
			return
		}
		name := strings.TrimSpace(s.Find("span.line-clamp-2").First().Text())
		if name == "" {
			name = strings.TrimSpace(s.Find("a[href*='/p/'] img").First().AttrOr("alt", ""))
		}
		if name == "" {
			return
		}

		href := ""
		if h, ok := s.Find("a[href*='/p/']").First().Attr("href"); ok {
			href = html.UnescapeString(h)
		}
		productURL := cleanProductURL(href)

		img, _ := s.Find("img").First().Attr("src")
		img = html.UnescapeString(img)

		salePrice := priceFromSel(s.Find("div.font-sans.text-lg").First())
		wasPrice := priceFromSel(s.Find("div.line-through").First())

		var desc strings.Builder
		if salePrice != "" {
			desc.WriteString("Price $" + salePrice)
		}
		if wasPrice != "" {
			desc.WriteString(" Was $" + wasPrice)
		}

		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "autobarn",
			ProviderID:      id,
			Name:            name,
			Description:     strings.TrimSpace(desc.String()),
			Manufacturer:    brandFromName(name),
			MPN:             id,
			PreviewImageURL: img,
			ProviderURL:     productURL,
		})
	})
	return results
}

// parseProduct parses a product page into a PartDetailDTO.
func (p *Provider) parseProduct(doc *goquery.Document, rawBody, productURL, providerID string) (*suppliers.PartDetailDTO, error) {
	ld := jsonLdProduct(doc)

	name := ld.Name
	if name == "" {
		name = strings.TrimSpace(doc.Find("h1").First().Text())
	}
	brand := ld.Brand.Name
	if brand == "" {
		brand = strings.TrimSpace(doc.Find("h1").First().Text())
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "autobarn",
			ProviderID:      providerID,
			Name:            name,
			Description:     html.UnescapeString(ld.Description),
			Category:        ld.Category,
			Manufacturer:    brand,
			MPN:             ld.MPN,
			PreviewImageURL: "",
			ProviderURL:     canonicalURL(productURL, ld.URL),
		},
	}

	if ld.Image != "" {
		detail.PreviewImageURL = ld.Image
		detail.Images = append(detail.Images, suppliers.FileDTO{URL: ld.Image, Name: providerID + ".png"})
	}
	if detail.PreviewImageURL == "" {
		if src, ok := doc.Find("img[alt], img").First().Attr("src"); ok {
			detail.PreviewImageURL = html.UnescapeString(src)
			detail.Images = append(detail.Images, suppliers.FileDTO{URL: detail.PreviewImageURL, Name: providerID + ".png"})
		}
	}

	price := "0.00"
	if ld.Offers.Price > 0 {
		price = fmt.Sprintf("%.2f", ld.Offers.Price)
	}
	currency := ld.Offers.PriceCurrency
	if currency == "" {
		currency = "AUD"
	}

	// Was/list price from the visible strike-through element.
	wasPrice := priceFromSel(doc.Find("div.line-through"))
	if wasPrice != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Was price",
			ValueText: "$" + wasPrice,
			Group:     "Pricing",
		})
	}

	vi := suppliers.PurchaseInfoDTO{
		DistributorName: "Autobarn",
		OrderNumber:     providerID,
		ProductURL:      detail.ProviderURL,
		Currency:        currency,
		Price:           price,
		MinimumOrderQty: "1",
		InStock:         strings.Contains(strings.ToLower(ld.Offers.Availability), "instock"),
		Prices: []suppliers.PriceDTO{
			{
				MinQuantity:          1,
				Price:                price,
				Currency:             currency,
				IncludesTax:          true,
				PriceRelatedQuantity: 1,
			},
		},
	}
	detail.VendorInfos = append(detail.VendorInfos, vi)

	// Specifications from the streamed HTML table strings.
	specsText := specTableFromRaw(rawBody)

	// Fall back to any visible table of label/value rows.
	if specsText == "" {
		specsText = specTableFromStream(doc)
	}
	for _, spec := range parseSpecTable(specsText) {
		detail.Parameters = append(detail.Parameters, spec)
	}

	// Features / description rich text appended as parameters.
	desc := html.UnescapeString(ld.Description)
	for _, f := range extractFeatures(desc) {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Feature",
			ValueText: f,
			Group:     "Features",
		})
	}

	return detail, nil
}

// jsonLdProduct extracts the schema.org Product JSON-LD block.
func jsonLdProduct(doc *goquery.Document) ldProduct {
	var info ldProduct
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		if info.Name != "" {
			return
		}
		var data ldProduct
		if err := json.Unmarshal([]byte(strings.TrimSpace(s.Text())), &data); err != nil {
			return
		}
		if data.Name != "" {
			info = data
		}
	})
	return info
}

type ldProduct struct {
	SKU         string `json:"sku"`
	MPN         string `json:"mpn"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Image       string `json:"image"`
	Category    string `json:"category"`
	URL         string `json:"url"`
	Brand       struct {
		Name string `json:"name"`
	} `json:"brand"`
	Offers struct {
		Price         float64 `json:"price"`
		PriceCurrency string  `json:"priceCurrency"`
		Availability  string  `json:"availability"`
	} `json:"offers"`
}

// specTableFromRaw extracts the escaped <table> specification HTML embedded in
// the React Router streamed loader data under the "specifications" key.
func specTableFromRaw(raw string) string {
	re := regexp.MustCompile(`specifications\\",\\"(.+?)\\",`)
	m := re.FindStringSubmatch(raw)
	if len(m) < 2 {
		return ""
	}
	var sb strings.Builder
	s := m[1]
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+5 < len(s) && s[i+1] == 'u' {
			if r, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
				sb.WriteRune(rune(r))
				i += 5
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// specTableFromStream extracts the escaped <table> specification HTML embedded
// in the React Router streamed loader data under the "specifications" key.
func specTableFromStream(doc *goquery.Document) string {
	raw, _ := doc.Html()
	return specTableFromRaw(raw)
}

// parseSpecTable parses a simple <table> of <td> pairs into parameters.
func parseSpecTable(tableHTML string) []suppliers.ParameterDTO {
	var params []suppliers.ParameterDTO
	if tableHTML == "" {
		return params
	}
	tdDoc, err := goquery.NewDocumentFromReader(strings.NewReader(tableHTML))
	if err != nil {
		return params
	}
	tdDoc.Find("tr").Each(func(_ int, tr *goquery.Selection) {
		cells := tr.Find("td")
		if cells.Length() < 2 {
			return
		}
		key := strings.TrimSpace(cells.Eq(0).Text())
		val := strings.TrimSpace(cells.Eq(1).Text())
		if key == "" || val == "" {
			return
		}
		params = append(params, suppliers.ParameterDTO{
			Name:      key,
			ValueText: val,
			Group:     "Specifications",
		})
	})
	return params
}

// extractFeatures pulls feature bullets from a rich description string.
func extractFeatures(desc string) []string {
	var out []string
	featureDoc, err := goquery.NewDocumentFromReader(strings.NewReader(desc))
	if err != nil {
		return out
	}
	featureDoc.Find("li").Each(func(_ int, s *goquery.Selection) {
		t := strings.TrimSpace(s.Text())
		if t != "" {
			out = append(out, t)
		}
	})
	return out
}

// resolveProductURL finds the canonical product page URL for an ID.
func (p *Provider) resolveProductURL(ctx context.Context, providerID string) (string, error) {
	u, _ := url.Parse(baseURL + searchPath)
	q := u.Query()
	q.Set("text", providerID)
	u.RawQuery = q.Encode()

	doc, _, err := p.fetchDoc(ctx, u.String())
	if err != nil {
		return "", err
	}
	if err := detectBlock(doc); err != nil {
		return "", err
	}

	var found string
	doc.Find(".listing-product-tile").EachWithBreak(func(_ int, s *goquery.Selection) bool {
		if s.AttrOr("data-insights-object-id", "") == providerID {
			if h, ok := s.Find("a[href*='/p/']").First().Attr("href"); ok {
				found = cleanProductURL(html.UnescapeString(h))
			}
			return false
		}
		return true
	})
	if found == "" {
		return "", fmt.Errorf("no Autobarn product page found for %s", providerID)
	}
	return found, nil
}

// cleanProductURL strips tracking query params (queryID, indexUsed) from a
// product URL and resolves it to an absolute URL.
func cleanProductURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	if u.IsAbs() {
		return u.String()
	}
	base, _ := url.Parse(baseURL)
	return base.ResolveReference(u).String()
}

func canonicalURL(fallback, ldURL string) string {
	if u := cleanProductURL(ldURL); u != "" {
		return u
	}
	return cleanProductURL(fallback)
}

// autobarnProductURL builds a product URL from an ID (used in the Algolia path).
func autobarnProductURL(id string) string {
	return baseURL + "/ab/" + id
}

// priceFromSel extracts a price ("299.99") from a selection's text like "$ 299.99".
func priceFromSel(s *goquery.Selection) string {
	text := s.Text()
	re := regexp.MustCompile(`\$?\s*([0-9]+[.,][0-9]{2})`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// priceFromText extracts a price number from raw text.
func priceFromText(text string) string {
	re := regexp.MustCompile(`([0-9]+[.,][0-9]{2})`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	return m[1]
}

// fmtPrice formats a price value for display.
func fmtPrice(v any) string {
	switch t := v.(type) {
	case float64:
		return fmt.Sprintf("%.2f", t)
	case string:
		return priceFromText(t)
	default:
		return ""
	}
}

// str flattens an any into a non-empty string using standard JSON types.
func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	default:
		return ""
	}
}

// brandFromName derives a brand from the first token of a product name.
func brandFromName(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}