package jaycar

import (
	"encoding/json"
	"fmt"
	"strings"
)

// pageResponse mirrors the JSON returned by /bff/page, which carries the same
// structured "page sections" used by the website's __NEXT_DATA__.
type pageResponse struct {
	Data struct {
		Page struct {
			Sections []json.RawMessage `json:"sections"`
		} `json:"page"`
	} `json:"data"`
}

// productMainSection is the ProductMain section of a product page. It holds
// the full product record plus any superseded (replacement) products.
type productMainSection struct {
	Product            productDetail        `json:"product"`
	SupersededProducts []supersededProduct  `json:"supersededProducts"`
}

// productDetail is the full product record from the ProductMain section.
type productDetail struct {
	Title         string       `json:"title"`
	URL           string       `json:"url"`
	Sku           string       `json:"sku"`
	CatNo         string       `json:"catNo"`
	Mpn           string       `json:"mpn"`
	BrandName     string       `json:"brandName"`
	InStock       bool         `json:"inStock"`
	ExternalURL   string       `json:"externalUrl"`
	ItemStatusTags string      `json:"item_status_tags"`
	Category1Name string       `json:"category1Name"`
	Category2Name string       `json:"category2Name"`
	Category3Name string       `json:"category3Name"`
	RegularPrice  jaycarPrice  `json:"regularPrice"`
	FinalPrice    jaycarPrice  `json:"finalPrice"`
	MultiBuyTiers []multiBuyTier `json:"multiBuyTiers"`
	Carousel      struct {
		Slides []carouselSlide `json:"slides"`
	} `json:"carousel"`
}

type multiBuyTier struct {
	MinimumQuantity int          `json:"minimumQuantity"`
	FinalPrice      jaycarPrice  `json:"finalPrice"`
}

// carouselSlide is one image in the product gallery. Src is relative to the
// media CDN (https://media.jaycar.com.au/product/images/).
type carouselSlide struct {
	Type    string `json:"type"`
	Src     string `json:"src"`
	AltText string `json:"altText"`
}

// supersededProduct identifies a product that this one was replaced by (or
// that replaced it), linking replacement part numbers for the notes field.
type supersededProduct struct {
	Sku   string `json:"sku"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// productDetailsSection is the ProductDetails section: specification groups,
// the Contentstack overview rich text and any downloadable documents.
type productDetailsSection struct {
	Specification struct {
		Items []specGroup `json:"items"`
	} `json:"specification"`
	Overview  overviewData `json:"overview"`
	Downloads struct {
		Items []downloadItem `json:"items"`
	} `json:"downloads"`
}

type specGroup struct {
	Title      string `json:"title"`
	Attributes []struct {
		Title string `json:"title"`
		Value string `json:"value"`
	} `json:"attributes"`
}

type downloadItem struct {
	Title string `json:"title"`
	Link  string `json:"link"`
}

type overviewData struct {
	Content contentNode `json:"content"`
}

// contentNode is a node in the Contentstack rich-text document tree. Text and
// links carry the content; paragraph/list/heading nodes organise it.
type contentNode struct {
	Type     string            `json:"type"`
	Text     string            `json:"text"`
	URL      string            `json:"url"`
	Attrs    map[string]interface{} `json:"attrs"`
	Children []contentNode     `json:"children"`
}

// parsePageSections decodes the sections array from a /bff/page response.
func parsePageSections(data []byte) ([]json.RawMessage, error) {
	var pr pageResponse
	if err := json.Unmarshal(data, &pr); err != nil {
		return nil, fmt.Errorf("[JAYCAR] decode page response: %w", err)
	}
	if pr.Data.Page.Sections == nil {
		return nil, fmt.Errorf("[JAYCAR] page response has no sections")
	}
	return pr.Data.Page.Sections, nil
}

// findProductMain returns the product record and superseded products from a
// page's sections, reporting whether the ProductMain section was present.
func findProductMain(sections []json.RawMessage) (productDetail, []supersededProduct, bool) {
	for _, raw := range sections {
		var probe struct {
			Typename string `json:"__typename"`
		}
		if json.Unmarshal(raw, &probe) != nil || probe.Typename != "ProductMain" {
			continue
		}
		var sec productMainSection
		if err := json.Unmarshal(raw, &sec); err != nil {
			continue
		}
		return sec.Product, sec.SupersededProducts, true
	}
	return productDetail{}, nil, false
}

// findProductDetails returns the specification/overview/downloads section,
// reporting whether it was present.
func findProductDetails(sections []json.RawMessage) (productDetailsSection, bool) {
	for _, raw := range sections {
		var probe struct {
			Typename string `json:"__typename"`
		}
		if json.Unmarshal(raw, &probe) != nil || probe.Typename != "ProductDetails" {
			continue
		}
		var sec productDetailsSection
		if err := json.Unmarshal(raw, &sec); err != nil {
			continue
		}
		return sec, true
	}
	return productDetailsSection{}, false
}

// flattenContent renders the Contentstack rich-text tree as readable plain
// text, keeping link URLs inline as "[text](url)".
func flattenContent(n contentNode) string {
	switch n.Type {
	case "text", "":
		// Leaf text nodes carry no "type"; their content is in Text.
		if n.Text != "" {
			return n.Text
		}
		return flattenChildren(n.Children)
	case "a":
		inner := flattenChildren(n.Children)
		href := n.URL
		if href == "" {
			if u, ok := n.Attrs["url"].(string); ok {
				href = u
			}
		}
		if href != "" && inner != "" {
			return fmt.Sprintf("%s (%s)", inner, href)
		}
		return inner
	case "paragraph", "p":
		return flattenChildren(n.Children)
	case "heading":
		return flattenChildren(n.Children)
	case "list", "bulletList", "orderedList":
		return flattenChildren(n.Children)
	case "listItem":
		return "- " + flattenChildren(n.Children)
	case "blockquote":
		return flattenChildren(n.Children)
	default:
		return flattenChildren(n.Children)
	}
}

func flattenChildren(children []contentNode) string {
	var parts []string
	for _, c := range children {
		if s := flattenContent(c); s != "" {
			parts = append(parts, strings.TrimSpace(s))
		}
	}
	return strings.Join(parts, " ")
}

// mediaURL resolves the relative product media references (images and
// downloads) served from the Jaycar media CDN.
func mediaURL(ref string) string {
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if strings.HasPrefix(ref, "//") {
		return "https:" + ref
	}
	return "https://media.jaycar.com.au/product/images/" + strings.TrimPrefix(ref, "/")
}
