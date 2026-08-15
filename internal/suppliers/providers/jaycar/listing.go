package jaycar

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

// nextDataRE extracts the __NEXT_DATA__ JSON embedded in Jaycar pages.
var nextDataRE = regexp.MustCompile(`<script[^>]*id="__NEXT_DATA__"[^>]*>(.*?)</script>`)

// jaycarListing is a product row from the __NEXT_DATA__ ProductListing section
// on search/category pages.
type jaycarListing struct {
	Title                string       `json:"title"`
	URL                  string       `json:"url"`
	Thumbnail            struct {
		Src string `json:"src"`
	} `json:"thumbnail"`
	RegularPrice         *jaycarPrice `json:"regularPrice"`
	FinalPrice           *jaycarPrice `json:"finalPrice"`
	Sku                  string       `json:"sku"`
	BrandName            string       `json:"brandName"`
	InStock              bool         `json:"inStock"`
	InStore              bool         `json:"inStore"`
	AvailableForDelivery string       `json:"availableForDelivery"`
	Rating               float64      `json:"rating"`
	ReviewCount          string       `json:"reviewCount"`
	Category1Name        string       `json:"category1Name"`
	Category2Name        string       `json:"category2Name"`
	ItemStatusTags       string       `json:"item_status_tags"`
}

type jaycarPrice struct {
	CentAmount   int    `json:"centAmount"`
	CurrencyCode string `json:"currencyCode"`
	Amount       int    `json:"amount"`
	Type         string `json:"type"`
	IsNet        bool   `json:"isNet"`
}

// nextData is the root of the __NEXT_DATA__ JSON object.
type nextData struct {
	Props struct {
		PageProps struct {
			Page struct {
				Sections []json.RawMessage `json:"sections"`
			} `json:"page"`
		} `json:"pageProps"`
	} `json:"props"`
}

// productListingSection is the ProductListing section that holds search/category results.
type productListingSection struct {
	TotalProducts int             `json:"totalProducts"`
	TotalPages    int             `json:"totalPages"`
	CurrentPage   int             `json:"currentPage"`
	Products      []jaycarListing `json:"products"`
	CategoryID    string          `json:"categoryId"`
}

// parseListingProducts extracts the products from the __NEXT_DATA__ JSON in a
// page's raw HTML. It is the pure parse used by SearchByKeyword regardless of
// how the HTML was acquired.
func parseListingProducts(html []byte) ([]jaycarListing, error) {
	m := nextDataRE.FindSubmatch(html)
	if m == nil {
		return nil, fmt.Errorf("no __NEXT_DATA__ block found in page")
	}

	var nd nextData
	if err := json.Unmarshal(m[1], &nd); err != nil {
		return nil, fmt.Errorf("decode __NEXT_DATA__: %w", err)
	}

	for _, raw := range nd.Props.PageProps.Page.Sections {
		var probe struct {
			Typename string `json:"__typename"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			continue
		}
		if probe.Typename != "ProductListing" {
			continue
		}
		var sec productListingSection
		if err := json.Unmarshal(raw, &sec); err != nil {
			continue
		}
		if len(sec.Products) == 0 {
			continue
		}
		return sec.Products, nil
	}

	return nil, fmt.Errorf("no ProductListing found in Jaycar page data")
}

func productsToSearchResults(products []jaycarListing) []suppliers.SearchResultDTO {
	results := make([]suppliers.SearchResultDTO, 0, len(products))
	for _, prod := range products {
		name := prod.Title
		if name == "" {
			name = prod.Sku
		}
		results = append(results, suppliers.SearchResultDTO{
			ProviderKey:     "jaycar",
			ProviderID:      prod.Sku,
			Name:            name,
			Category:        listingCategory(prod),
			Manufacturer:    prod.BrandName,
			MPN:             prod.Sku,
			PreviewImageURL: prod.Thumbnail.Src,
			ProviderURL:     listingURL(prod),
		})
	}
	return results
}

func listingCategory(prod jaycarListing) string {
	if prod.Category1Name != "" {
		return prod.Category1Name
	}
	if prod.Category2Name != "" {
		return prod.Category2Name
	}
	return ""
}

func listingURL(prod jaycarListing) string {
	if prod.URL != "" {
		return baseURL + prod.URL
	}
	if prod.Sku != "" {
		return fmt.Sprintf("%s/search?q=%s", baseURL, url.QueryEscape(prod.Sku))
	}
	return baseURL
}

func (p *jaycarPrice) AUD() int {
	if p == nil {
		return 0
	}
	if p.CentAmount > 0 {
		return p.CentAmount
	}
	return p.Amount
}

func (p *jaycarPrice) String() string {
	if p == nil {
		return ""
	}
	return fmt.Sprintf("%.2f", float64(p.AUD())/100.0)
}
