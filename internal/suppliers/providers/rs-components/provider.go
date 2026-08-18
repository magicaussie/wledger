// Package rs implements the suppliers.Provider interface for RS Australia
// (au.rs-online.com), the Australian RS Components storefront.
//
// The catalogue pages are behind Akamai Bot Manager: GET requests to /web/c/
// and /web/p/ are rejected with HTTP 403 "Access Denied", but POST requests
// with an empty body are served normally. Each response embeds a complete
// __NEXT_DATA__ JSON payload (schema.org Product, search records, article
// details, pricing and stock) that this provider parses. No API key or
// browser automation is required.
package rs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const (
	baseURL    = "https://au.rs-online.com"
	searchPath = "/web/c/"
	pagePath   = "/web/p/"
)

var (
	nextDataRE = regexp.MustCompile(`<script id="__NEXT_DATA__"[^>]*>(.*?)</script>`)
	pageIDRE   = regexp.MustCompile(`/web/p/[a-z0-9-]+/([0-9]+)`)
	stockNoRE  = regexp.MustCompile(`([0-9]{3})-([0-9]{3,})`)
)

// blockSignals are substrings that indicate a challenge/block page.
var blockSignals = []string{
	"access denied",
	"captcha",
	"verify you are human",
	"challenge",
	"bot protection",
}

// Provider implements the suppliers.Provider interface for RS Australia.
type Provider struct {
	httpClient *http.Client
}

func init() {
	suppliers.Register(NewProvider())
}

// NewProvider creates an RS Australia provider.
func NewProvider() *Provider {
	return &Provider{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewProviderWithClient creates an RS Australia provider using the given HTTP
// client (used by tests to inject a fake transport).
func NewProviderWithClient(client *http.Client) *Provider {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Provider{httpClient: client}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "rs-components",
		Name:         "RS Components Australia",
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
		suppliers.CapDatasheet,
		suppliers.CapPrice,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return domain == "au.rs-online.com" || strings.HasSuffix(domain, ".rs-online.com")
}

func (p *Provider) SearchCacheTTL() time.Duration { return 15 * time.Minute }
func (p *Provider) DetailCacheTTL() time.Duration { return 45 * time.Minute }

// ExtractPartIDFromURL extracts the RS page/product ID from a product URL.
// e.g. https://au.rs-online.com/web/p/contactors/0187920 -> "0187920"
func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if !p.HandlesDomain(u.Host) {
		return "", false
	}
	m := pageIDRE.FindStringSubmatch(u.Path)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// SearchByKeyword searches RS Australia via the catalog page. The catalog is
// served only to POST requests (Akamai blocks GET), so an empty-body POST to
// /web/c/?searchTerm=<query> is used and the embedded __NEXT_DATA__ is parsed.
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	next, err := p.fetchNextData(ctx, searchPath, keyword)
	if err != nil {
		return nil, err
	}

	if next.pageProps().NoResults {
		return nil, fmt.Errorf("no RS products found for %q", keyword)
	}

	// A query that matches a single product (exact Mfr Part No. or RS Stock
	// No.) is served as a product detail page: productListData is populated.
	if pld := next.pageProps().ProductListData; pld != nil && pld.SKU != "" {
		r := p.productListToResult(*pld)
		if r.ProviderID != "" {
			return []suppliers.SearchResultDTO{r}, nil
		}
	}

	results := p.recordsToResults(next.pageProps().DiscoverData.Records)
	if len(results) == 0 {
		return nil, fmt.Errorf("no RS products found for %q", keyword)
	}
	return results, nil
}

// GetDetails fetches an RS product page and parses the rich article data.
func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if providerID == "" {
		return nil, fmt.Errorf("invalid RS product id %q", providerID)
	}

	productURL, err := p.resolveProductURL(ctx, providerID)
	if err != nil {
		return nil, err
	}

	next, err := p.fetchPage(ctx, productURL)
	if err != nil {
		return nil, err
	}

	return p.detailFromNext(next, providerID, productURL)
}

// fetchNextData posts to a catalog search path and returns parsed __NEXT_DATA__.
func (p *Provider) fetchNextData(ctx context.Context, path, query string) (*nextData, error) {
	u, _ := url.Parse(baseURL + path)
	q := u.Query()
	q.Set("searchTerm", query)
	u.RawQuery = q.Encode()
	return p.fetchPage(ctx, u.String())
}

// fetchPage performs an empty-body POST (bypasses Akamai GET rejection) and
// parses the __NEXT_DATA__ JSON embedded in the page.
func (p *Provider) fetchPage(ctx context.Context, pageURL string) (*nextData, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", pageURL, strings.NewReader(""))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", baseURL+"/")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rs request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		low := strings.ToLower(string(body))
		if containsAny(low, blockSignals) {
			return nil, fmt.Errorf("rs challenge page detected")
		}
		return nil, fmt.Errorf("rs returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 25*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("rs body read failed: %w", err)
	}

	m := nextDataRE.FindSubmatch(body)
	if m == nil {
		low := strings.ToLower(string(body))
		if containsAny(low, blockSignals) {
			return nil, fmt.Errorf("rs challenge page detected")
		}
		return nil, fmt.Errorf("rs search markup changed: no __NEXT_DATA__ found")
	}

	var nd nextData
	if err := json.Unmarshal(m[1], &nd); err != nil {
		return nil, fmt.Errorf("failed to decode RS __NEXT_DATA__: %w", err)
	}
	return &nd, nil
}

// resolveProductURL finds the product page URL for an id by searching RS.
// The id may be a page ID (0187920), an RS Stock No (187-920) or a Mfr part
// number (LC1D09U7).
func (p *Provider) resolveProductURL(ctx context.Context, providerID string) (string, error) {
	next, err := p.fetchNextData(ctx, searchPath, providerID)
	if err != nil {
		return "", err
	}

	if pld := next.pageProps().ProductListData; pld != nil && pld.URL != "" {
		return pld.URL, nil
	}

	// Normalise a "187-920" style stock number to the page id "0187920".
	pageID := providerID
	if m := stockNoRE.FindStringSubmatch(providerID); m != nil {
		pageID = "0" + m[1] + m[2]
	}

	for _, rec := range next.pageProps().DiscoverData.Records {
		if rec.ID == pageID || strings.TrimLeft(rec.ID, "0") == strings.TrimLeft(providerID, "0") {
			if len(rec.Article) > 0 {
				daa := rec.Article[0].DiscoverArticleAttributes
				if daa.ProductURL != "" {
					return daa.ProductURL, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no RS product page found for %s", providerID)
}

// recordsToResults maps discoverData.records to search results.
func (p *Provider) recordsToResults(records []record) []suppliers.SearchResultDTO {
	var out []suppliers.SearchResultDTO
	for _, rec := range records {
		if rec.ID == "" || len(rec.Article) == 0 {
			continue
		}
		a := rec.Article[0]
		daa := a.DiscoverArticleAttributes
		if daa.MPN == "" && a.Title == "" {
			continue
		}
		productURL := daa.ProductURL
		if productURL == "" {
			productURL = fmt.Sprintf("%s/web/p/%s/%s", baseURL, slugFromTitle(a.Title), rec.ID)
		}

		price := daa.DisplayPrice
		if price == "" {
			price = "$" + daa.Price
		}

		var desc strings.Builder
		desc.WriteString("Price " + price + " ex GST")
		if daa.StockStatus != "" {
			desc.WriteString(" | " + formatStock(daa.StockStatus))
		}

		out = append(out, suppliers.SearchResultDTO{
			ProviderKey:     "rs-components",
			ProviderID:      rec.ID,
			Name:            a.Title,
			Description:     strings.TrimSpace(desc.String()),
			Manufacturer:    daa.Brand,
			MPN:             daa.MPN,
			PreviewImageURL: daa.Image,
			ProviderURL:     productURL,
		})
	}
	return out
}

// productListToResult maps the single-product productListData to a search result.
func (p *Provider) productListToResult(pld productListData) suppliers.SearchResultDTO {
	price := ""
	if v, ok := offerPrice(pld.Offers.Price); ok {
		price = fmt.Sprintf("$%.2f", v)
	}

	var desc strings.Builder
	if price != "" {
		desc.WriteString("Price " + price + " ex GST")
	}
	if pld.SKU != "" {
		desc.WriteString(" | RS Stock No. " + pld.SKU)
	}

	providerID := pld.SKU
	if providerID == "" {
		providerID = extractPageID(pld.URL)
	}

	return suppliers.SearchResultDTO{
		ProviderKey:     "rs-components",
		ProviderID:      providerID,
		Name:            pld.Name,
		Description:     strings.TrimSpace(desc.String()),
		Manufacturer:    pld.Brand.Name,
		MPN:             pld.MPN,
		PreviewImageURL: pld.Image,
		ProviderURL:     pld.URL,
	}
}

// detailFromNext builds a PartDetailDTO from a product page's __NEXT_DATA__.
func (p *Provider) detailFromNext(next *nextData, providerID, productURL string) (*suppliers.PartDetailDTO, error) {
	article := next.pageProps().ArticleResult.Data.Article
	pld := next.pageProps().ProductListData
	avail := next.pageProps().ProductAvailabilityResult.Data

	if article.RsStockNumber == "" && pld.Name == "" {
		return nil, fmt.Errorf("rs product not found")
	}

	id := providerID
	if id == "" {
		id = article.RsStockNumber
	}
	if id == "" {
		id = pld.SKU
	}
	if id == "" {
		id = article.ManufacturerPartNumber
	}
	if id == "" {
		id = extractPageID(productURL)
	}

	name := article.ShortDescription()
	if name == "" {
		name = pld.Name
	}
	brand := article.Brand
	if brand == "" {
		brand = pld.Brand.Name
	}
	mpn := article.ManufacturerPartNumber
	if mpn == "" {
		mpn = pld.MPN
	}

	detail := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "rs-components",
			ProviderID:      id,
			Name:            name,
			Description:     article.LongDescription,
			Category:        article.SeoCategoryName,
			Manufacturer:    brand,
			MPN:             mpn,
			ProviderURL:     absURL(article.ProductURL),
			PreviewImageURL: article.ThumbnailImageURL,
		},
	}
	if detail.ProviderURL == "" || !strings.HasPrefix(detail.ProviderURL, "http") {
		detail.ProviderURL = absURL(pld.URL)
	}
	if detail.ProviderURL == "" {
		detail.ProviderURL = productURL
	}
	if detail.PreviewImageURL == "" {
		detail.PreviewImageURL = pld.Image
	}

	// Images
	seen := make(map[string]bool)
	for _, img := range article.Images {
		u := mediaURL(img)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		detail.Images = append(detail.Images, suppliers.FileDTO{URL: u, Name: id + ".jpg"})
		if detail.PreviewImageURL == "" {
			detail.PreviewImageURL = u
		}
	}

	// Documents (datasheets, certificates, catalogues)
	seenDocs := make(map[string]bool)
	for _, doc := range article.Documents {
		u := doc.URL
		if u == "" || seenDocs[u] {
			continue
		}
		seenDocs[u] = true
		fd := suppliers.FileDTO{URL: u, Name: doc.Title + ".pdf"}
		switch doc.Type {
		case "data_sheet", "datasheet":
			detail.Datasheets = append(detail.Datasheets, fd)
		case "general", "environmental_doc", "statement_of_conformity":
			detail.Datasheets = append(detail.Datasheets, fd)
		}
	}

	// Pricing (ex GST and inc GST)
	priceEx := article.PriceExGST()
	priceInc := article.PriceIncGST()
	currency := "AUD"
	if len(article.Prices.PriceBreaks) > 0 {
		currency = article.Prices.CurrencyCode
		if priceEx == "" {
			priceEx = article.Prices.PriceBreaks[0].RoundedPrice
		}
		if priceInc == "" {
			priceInc = article.Prices.PriceBreaks[0].RoundedVatIncPrice
		}
	}
	if v, ok := offerPrice(pld.Offers.Price); ok {
		priceEx = fmt.Sprintf("%.2f", v)
	}

	vi := suppliers.PurchaseInfoDTO{
		DistributorName: "RS Components Australia",
		OrderNumber:     id,
		ProductURL:      detail.ProviderURL,
		Currency:        currency,
		Price:           priceEx,
		MinimumOrderQty: strconv.Itoa(article.MinimumOrderQty),
		InStock:         avail.IsInStock(),
		Prices:          []suppliers.PriceDTO{},
	}
	if priceEx != "" {
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                priceEx,
			Currency:             currency,
			IncludesTax:          false,
			PriceRelatedQuantity: 1,
		})
	}
	if priceInc != "" {
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                priceInc,
			Currency:             currency,
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
	}
	detail.VendorInfos = append(detail.VendorInfos, vi)

	// Stock
	if avail.Status != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Stock Status",
			ValueText: avail.Status,
			Group:     "Stock",
		})
	}
	if avail.Quantity > 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Stock Quantity",
			ValueText: strconv.Itoa(avail.Quantity),
			Group:     "Stock",
		})
	}
	if avail.TotalAvailable > 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Total Available",
			ValueText: strconv.Itoa(avail.TotalAvailable),
			Group:     "Stock",
		})
	}

	// Pricing detail
	if priceEx != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Price ex GST",
			ValueText: "$" + priceEx,
			Group:     "Pricing",
		})
	}
	if priceInc != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Price inc GST",
			ValueText: "$" + priceInc,
			Group:     "Pricing",
		})
	}
	if article.Prices.WasPrice != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Was price",
			ValueText: article.Prices.WasPrice,
			Group:     "Pricing",
		})
	}

	// Quantity breaks
	for i, pb := range article.PriceBreaks {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      fmt.Sprintf("Qty break %d", i+1),
			ValueText: fmt.Sprintf("%s qty @ $%s", pb.Quantity, pb.Price),
			Group:     "Pricing",
		})
	}

	// Article-level facts
	if article.CountryOfOrigin != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Country of Origin",
			ValueText: article.CountryOfOrigin,
			Group:     "General",
		})
	}
	if article.PackSize != 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "Pack Size",
			ValueText: strconv.Itoa(article.PackSize),
			Group:     "General",
		})
	}
	if article.RoHSStatus != "" {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "RoHS Status",
			ValueText: article.RoHSStatus,
			Group:     "Compliance",
		})
	}
	if article.TaxRatePercentage != 0 {
		detail.Parameters = append(detail.Parameters, suppliers.ParameterDTO{
			Name:      "GST Rate",
			ValueText: fmt.Sprintf("%.0f%%", article.TaxRatePercentage),
			Group:     "Pricing",
		})
	}

	// Technical specifications
	specs := article.Specifications()
	if len(specs) == 0 {
		specs = pld.Specifications()
	}
	for _, s := range specs {
		detail.Parameters = append(detail.Parameters, s)
	}

	return detail, nil
}

// Specification helpers

func (a articleData) Specifications() []suppliers.ParameterDTO {
	var out []suppliers.ParameterDTO
	for _, s := range a.SpecificationAttributes {
		if s.Key == "" || s.Value == "" {
			continue
		}
		out = append(out, suppliers.ParameterDTO{
			Name:      s.Key,
			ValueText: s.Value,
			Group:     "Specifications",
		})
	}
	return out
}

func (p *productListData) Specifications() []suppliers.ParameterDTO {
	var out []suppliers.ParameterDTO
	for _, s := range p.AdditionalProperty {
		if s.Name == "" || s.Value == "" {
			continue
		}
		out = append(out, suppliers.ParameterDTO{
			Name:      s.Name,
			ValueText: s.Value,
			Group:     "Specifications",
		})
	}
	return out
}

func (a articleData) ShortDescription() string {
	if dc := a.DescriptiveContent; dc != nil {
		if dc.UniqueName != "" {
			return dc.UniqueName
		}
	}
	return ""
}

func (a articleData) PriceExGST() string {
	for _, pb := range a.Prices.PriceBreaks {
		if pb.RoundedPrice != "" {
			return pb.RoundedPrice
		}
	}
	return ""
}

func (a articleData) PriceIncGST() string {
	for _, pb := range a.Prices.PriceBreaks {
		if pb.RoundedVatIncPrice != "" {
			return pb.RoundedVatIncPrice
		}
	}
	return ""
}

// __NEXT_DATA__ structures

type nextData struct {
	Props struct {
		PageProps pageProps `json:"pageProps"`
	} `json:"props"`
}

type pageProps struct {
	NoResults                   bool                      `json:"noResults"`
	ProductListData             *productListData          `json:"productListData"`
	DiscoverData                discoverData              `json:"discoverData"`
	ArticleResult               articleResult             `json:"articleResult"`
	ProductAvailabilityResult   productAvailabilityResult `json:"productAvailabilityResult"`
}

type discoverData struct {
	Pagination struct {
		Limit    string `json:"limit"`
		Page     int    `json:"page"`
		Total    int    `json:"total"`
		LastPage int    `json:"lastPage"`
	} `json:"pagination"`
	Records []record `json:"records"`
}

type record struct {
	ID      string     `json:"id"`
	Article []article1 `json:"article"`
}

type article1 struct {
	Title                     string                    `json:"title"`
	DiscoverArticleAttributes discoverArticleAttributes `json:"discoverArticleAttributes"`
}

type discoverArticleAttributes struct {
	DisplayPrice       string `json:"displayPrice"`
	Price              string `json:"price"`
	Brand              string `json:"brand"`
	Image              string `json:"image"`
	LeafCategoryName   string `json:"leafCategoryName"`
	MPN                string `json:"mpn"`
	ProductURL         string `json:"productURL"`
	StockStatus        string `json:"stockStatus"`
	UnitPriced         bool   `json:"unitPriced"`
	PackSize           string `json:"packSize"`
	SalesUnitOfMeasure string `json:"salesUnitOfMeasure"`
}

type productListData struct {
	Name               string `json:"name"`
	MPN                string `json:"mpn"`
	SKU                string `json:"sku"`
	URL                string `json:"url"`
	Image              string `json:"image"`
	Brand              struct {
		Name string `json:"name"`
	} `json:"brand"`
	Offers struct {
		Price json.Number `json:"price"`
	} `json:"offers"`
	AdditionalProperty []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"additionalProperty"`
}

type articleResult struct {
	Data struct {
		Article articleData `json:"article"`
	} `json:"data"`
}

type articleData struct {
	RsStockNumber           string            `json:"rsStockNumber"`
	ManufacturerPartNumber  string            `json:"manufacturerPartNumber"`
	Brand                   string            `json:"brand"`
	LongDescription         string            `json:"longDescription"`
	ProductURL              string            `json:"productUrl"`
	SeoCategoryName         string            `json:"seoCategoryName"`
	CountryOfOrigin         string            `json:"countryOfOrigin"`
	RoHSStatus              string            `json:"rohsStatus"`
	MinimumOrderQty         int               `json:"minimumOrderQty"`
	PackSize                int               `json:"packSize"`
	TaxRatePercentage       float64           `json:"taxRatePercentage"`
	ThumbnailImageURL       string            `json:"thumbnailImageURL"`
	Images                  []string          `json:"images"`
	Documents               []document        `json:"documents"`
	Prices                  articlePrices     `json:"prices"`
	PriceBreaks             []priceBreak      `json:"priceBreaks"`
	SpecificationAttributes []specAttribute   `json:"specificationAttributes"`
	DescriptiveContent      *descriptiveContent `json:"descriptiveContent"`
}

type descriptiveContent struct {
	UniqueName string `json:"unique.name"`
}

type document struct {
	Title string `json:"title"`
	Type  string `json:"type"`
	URL   string `json:"url"`
}

type articlePrices struct {
	CurrencyCode string        `json:"currencyCode"`
	PriceBreaks  []articleBreak `json:"priceBreaks"`
	WasPrice     string        `json:"wasPrice"`
	UOMMessage   string        `json:"uomMessage"`
}

type articleBreak struct {
	ID                 string `json:"id"`
	Price              string `json:"price"`
	RoundedPrice       string `json:"roundedPrice"`
	RoundedVatIncPrice string `json:"roundedVatIncPrice"`
	VatInclusivePrice  string `json:"vatInclusivePrice"`
}

type priceBreak struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

type specAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type productAvailabilityResult struct {
	Data availability `json:"data"`
}

type availability struct {
	ArticleID     string `json:"articleId"`
	Status        string `json:"status"`
	StatusCode    string `json:"statusCode"`
	Quantity      int    `json:"quantity"`
	TotalAvailable int   `json:"totalAvailable"`
	AddToCartEnabled bool `json:"addToCartEnabled"`
	BackOrderAllowed bool `json:"backOrderAllowed"`
}

func (a availability) IsInStock() bool {
	return strings.EqualFold(a.StatusCode, "IN_STOCK") || strings.Contains(strings.ToLower(a.Status), "in stock")
}

// pageProps returns the pageProps node from the __NEXT_DATA__ payload.
func (n *nextData) pageProps() pageProps {
	return n.Props.PageProps
}

// helpers

func mediaURL(name string) string {
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "http") {
		return name
	}
	return "https://media.rs-online.com/" + strings.TrimLeft(name, "/")
}

// offerPrice converts a json.Number price to float64.
func offerPrice(n json.Number) (float64, bool) {
	if n == "" {
		return 0, false
	}
	v, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return v, v > 0
}

// absURL resolves a possibly-relative RS URL against the AU base URL. RS
// storefront paths require the /web/ prefix (e.g. /web/p/...).
func absURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.IsAbs() {
		if !strings.HasPrefix(u.Path, "/web/") && strings.HasPrefix(u.Path, "/p/") {
			u.Path = "/web" + u.Path
		}
		return u.String()
	}
	base, _ := url.Parse(baseURL)
	resolved := base.ResolveReference(u)
	if !strings.HasPrefix(resolved.Path, "/web/") && strings.HasPrefix(resolved.Path, "/p/") {
		resolved.Path = "/web" + resolved.Path
	}
	return resolved.String()
}

func extractPageID(rawURL string) string {
	if m := pageIDRE.FindStringSubmatch(rawURL); m != nil {
		return m[1]
	}
	return ""
}

func slugFromTitle(title string) string {
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := re.ReplaceAllString(strings.ToLower(title), "-")
	return strings.Trim(slug, "-")
}

func formatStock(status string) string {
	switch strings.ToUpper(strings.ReplaceAll(status, " ", "_")) {
	case "IN_STOCK":
		return "In Stock"
	case "BACK_ORDER":
		return "Back Order"
	case "PREORDER", "PRE_ORDER":
		return "Preorder"
	case "NOT_AVAILABLE":
		return "Unavailable"
	default:
		return strings.ReplaceAll(strings.Title(strings.ToLower(strings.ReplaceAll(status, "_", " "))), " ", " ")
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
