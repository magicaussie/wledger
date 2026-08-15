package jaycar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

const baseURL = "https://www.jaycar.com.au"

var (
	productRE = regexp.MustCompile(`/p/([A-Za-z0-9]+)$`)
	skuRE     = regexp.MustCompile(`/p/([A-Za-z0-9_-]+)`)
)

// Provider implements the suppliers.Provider interface for Jaycar Australia.
//
// Acquisition happens primarily through the Storefront Cloud BFF API (api.go),
// which serves the full catalogue and product pages without a DataDome
// challenge. The page HTML fetcher is retained as a fallback for environments
// with a working acquisition path (a real browser page store), and parsing is
// deliberately separated from acquisition so a swap never touches the parser.
type Provider struct {
	fetcher   PageFetcher
	api       *apiClient
	catalogue *catalogue
	log       *slog.Logger
}

// NewProvider creates a Jaycar provider using the live BFF API plus the given
// HTML fetcher as a fallback (defaults to an HTTPFetcher when fetcher is nil).
func NewProvider(fetcher PageFetcher) *Provider {
	return NewProviderWithAPI(fetcher, newAPIClient())
}

// NewProviderWithAPI creates a provider with an explicit API client. Passing a
// nil api disables the API path (used by tests to exercise the HTML fetcher
// fallback deterministically).
func NewProviderWithAPI(fetcher PageFetcher, api *apiClient) *Provider {
	if fetcher == nil {
		fetcher = NewHTTPFetcher(nil, "")
	}
	return &Provider{
		fetcher:   fetcher,
		api:       api,
		catalogue: newCatalogue(os.Getenv("JAYCAR_CATALOGUE_FILE"), slog.Default()),
		log:       slog.Default(),
	}
}

func init() {
	suppliers.Register(NewProvider(nil))
}

// SetFetcher swaps the HTML acquisition mechanism at runtime (e.g. to a
// browser-supplied page store).
func (p *Provider) SetFetcher(f PageFetcher) {
	if f != nil {
		p.fetcher = f
	}
}

func (p *Provider) GetProviderInfo() suppliers.ProviderInfo {
	return suppliers.ProviderInfo{
		Key:          "jaycar",
		Name:         "Jaycar Electronics",
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
		suppliers.CapDatasheet,
	}
}

func (p *Provider) HandlesDomain(domain string) bool {
	return domain == "jaycar.com.au" || domain == "www.jaycar.com.au"
}

func (p *Provider) ExtractPartIDFromURL(rawURL string) (string, bool) {
	m := productRE.FindStringSubmatch(strings.Split(rawURL, "?")[0])
	if m == nil {
		return "", false
	}
	return m[1], true
}

// SearchByKeyword searches Jaycar. It uses the ranked BFF catalogue search,
// falls back to the locally cached catalogue, and finally to the HTML search
// page when both acquisition paths are unavailable.
func (p *Provider) SearchByKeyword(ctx context.Context, keyword string) ([]suppliers.SearchResultDTO, error) {
	if p.api != nil {
		products, err := p.api.searchProducts(ctx, keyword)
		if err == nil {
			if len(products) == 0 {
				// The API was reached and simply has no products for this
				// keyword; an empty result is valid (not a challenge).
				return []suppliers.SearchResultDTO{}, nil
			}
			rankListing(keyword, products)
			p.catalogue.merge(products)
			return productsToSearchResults(products), nil
		}
		p.log.Warn("[JAYCAR] BFF search failed; trying cache", "keyword", keyword, "error", err)
	}

	if hits := p.catalogue.search(keyword); len(hits) > 0 {
		p.log.Debug("[JAYCAR] served search from catalogue cache", "keyword", keyword, "count", len(hits))
		return catalogueToSearchResults(hits), nil
	}

	searchURL := fmt.Sprintf("%s/search?q=%s", baseURL, url.QueryEscape(keyword))
	html, err := p.fetcher.Fetch(ctx, searchURL)
	if err != nil {
		return nil, fmt.Errorf("jaycar fetch search page: %w", err)
	}
	products, err := parseListingProducts(html)
	if err != nil {
		return nil, fmt.Errorf("jaycar parse search page: %w", err)
	}
	rankListing(keyword, products)
	return productsToSearchResults(products), nil
}

// GetDetails fetches the full product record for a CAT.NO. It reads the BFF
// product listing and page data (overview, specifications, downloads, gallery
// images and replacement products), falling back to the product page HTML
// parser when the API path is unavailable.
func (p *Provider) GetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	if p.api != nil {
		detail, err := p.bffGetDetails(ctx, providerID)
		if err == nil {
			return detail, nil
		}
		if errors.Is(err, errProductNotFound) {
			// The API authoritatively reports the CAT.NO does not exist; do
			// not mask that with an HTML fallback.
			return nil, err
		}
		p.log.Warn("[JAYCAR] BFF detail failed; trying HTML page", "sku", providerID, "error", err)
	}

	productURL := fmt.Sprintf("%s/p/%s", baseURL, providerID)
	html, err := p.fetcher.Fetch(ctx, productURL)
	if err != nil {
		return nil, fmt.Errorf("jaycar fetch product page: %w", err)
	}
	return ParseProductPage(productURL, bytes.NewReader(html))
}

// bffGetDetails resolves a SKU to its canonical product, then fetches the
// structured product page to build a full PartDetailDTO.
func (p *Provider) bffGetDetails(ctx context.Context, providerID string) (*suppliers.PartDetailDTO, error) {
	listing, err := p.api.getProductBySku(ctx, providerID)
	if err != nil {
		return nil, err
	}

	// The listing alone carries price, stock, categories and the canonical
	// URL; the page data adds overview, specs, downloads and gallery. When the
	// page fetch fails, degrade gracefully to the listing-only detail.
	detail := listingToDetail(listing, nil)

	if listing.URL == "" {
		return detail, nil
	}
	pageBytes, err := p.api.getPage(ctx, listing.URL)
	if err != nil {
		p.log.Debug("[JAYCAR] page data unavailable; using listing detail", "sku", providerID, "error", err)
		return detail, nil
	}
	sections, err := parsePageSections(pageBytes)
	if err != nil {
		return detail, nil
	}
	prod, superseded, ok := findProductMain(sections)
	if ok {
		detail = listingToDetail(listing, &prod)
	}
	if dets, ok := findProductDetails(sections); ok {
		applyDetailsSection(detail, dets)
	}
	if len(superseded) > 0 {
		detail.Notes = appendReplacementNote(detail.Notes, superseded)
	}
	return detail, nil
}

// listingToDetail builds a PartDetailDTO from the catalogue listing record,
// optionally enriching it with the full product record from the page data.
func listingToDetail(listing *jaycarListing, prod *productDetail) *suppliers.PartDetailDTO {
	name := listing.Title
	sku := listing.Sku
	url := listingURL(*listing)

	dto := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "jaycar",
			ProviderID:      sku,
			Name:            name,
			Manufacturer:    listing.BrandName,
			MPN:             sku,
			PreviewImageURL: listing.Thumbnail.Src,
			ProviderURL:     url,
		},
	}

	if listing.Category1Name != "" {
		dto.Category = listing.Category1Name
	} else if listing.Category2Name != "" {
		dto.Category = listing.Category2Name
	}

	// Price + availability from the listing record.
	if listing.FinalPrice != nil || listing.RegularPrice != nil {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Jaycar Electronics",
			OrderNumber:     sku,
			ProductURL:      url,
			Currency:        "AUD",
			MinimumOrderQty: "1",
			InStock:         listing.InStock,
		}
		price := priceString(listing.FinalPrice, listing.RegularPrice)
		vi.Price = price
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                price,
			Currency:             "AUD",
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
		dto.VendorInfos = append(dto.VendorInfos, vi)
	}

	// Gallery images from the full product record, falling back to the
	// listing thumbnail.
	var slides []carouselSlide
	if prod != nil {
		slides = prod.Carousel.Slides
		for _, tier := range prod.MultiBuyTiers {
			if tier.MinimumQuantity > 1 {
				if tierPrice := priceString(&tier.FinalPrice, nil); len(dto.VendorInfos) > 0 && tierPrice != "" {
					dto.VendorInfos[0].Prices = append(dto.VendorInfos[0].Prices, suppliers.PriceDTO{
						MinQuantity:          tier.MinimumQuantity,
						Price:                tierPrice,
						Currency:             "AUD",
						IncludesTax:          true,
						PriceRelatedQuantity: tier.MinimumQuantity,
					})
				}
			}
		}
		if dto.Manufacturer == "" {
			dto.Manufacturer = prod.BrandName
		}
		if prod.ItemStatusTags != "" {
			dto.Notes = appendNote(dto.Notes, "Status: "+prod.ItemStatusTags)
		}
	}
	added := make(map[string]bool)
	for i, s := range slides {
		u := mediaURL(s.Src)
		if u == "" || added[u] {
			continue
		}
		added[u] = true
		dto.Images = append(dto.Images, suppliers.FileDTO{URL: u, Name: fmt.Sprintf("%s-image%d.jpg", sku, i)})
	}
	if len(dto.Images) == 0 && dto.PreviewImageURL != "" {
		dto.Images = append(dto.Images, suppliers.FileDTO{URL: dto.PreviewImageURL, Name: sku + ".jpg"})
	}

	return dto
}

// applyDetailsSection fills overview, specifications and datasheets onto a
// detail built from the listing.
func applyDetailsSection(dto *suppliers.PartDetailDTO, dets productDetailsSection) {
	if overview := flattenContent(dets.Overview.Content); overview != "" {
		dto.Description = overview
		dto.Notes = appendNote(dto.Notes, overview)
	}

	for _, group := range dets.Specification.Items {
		for _, attr := range group.Attributes {
			if strings.TrimSpace(attr.Title) == "" {
				continue
			}
			dto.Parameters = append(dto.Parameters, suppliers.ParameterDTO{
				Name:      clean(attr.Title),
				ValueText: clean(attr.Value),
				Group:     group.Title,
			})
		}
	}

	for _, d := range dets.Downloads.Items {
		u := mediaURL(d.Link)
		if u == "" {
			continue
		}
		dto.Datasheets = append(dto.Datasheets, suppliers.FileDTO{
			URL:  u,
			Name: dto.ProviderID + "-" + cleanDownloadName(d.Title),
		})
	}
}

func appendReplacementNote(notes string, superseded []supersededProduct) string {
	var names []string
	for _, s := range superseded {
		if s.Sku != "" {
			names = append(names, fmt.Sprintf("%s (%s)", s.Title, s.Sku))
		}
	}
	if len(names) == 0 {
		return notes
	}
	return appendNote(notes, "Superseded by: "+strings.Join(names, ", "))
}

func appendNote(notes, add string) string {
	if add == "" {
		return notes
	}
	if notes == "" {
		return add
	}
	return notes + " " + add
}

func priceString(final, regular *jaycarPrice) string {
	if final != nil && final.AUD() > 0 {
		return "$" + final.String()
	}
	if regular != nil && regular.AUD() > 0 {
		return "$" + regular.String()
	}
	return ""
}

func cleanDownloadName(title string) string {
	name := strings.ToLower(strings.TrimSpace(title))
	if name == "" {
		name = "document"
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r == ' ':
			return '-'
		default:
			return -1
		}
	}, name)
	return strings.Trim(name, "-")
}
