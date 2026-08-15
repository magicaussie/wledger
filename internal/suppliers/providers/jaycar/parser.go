package jaycar

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/tuxedocurly/wledger/internal/suppliers"
)

var (
	catNoRE = regexp.MustCompile(`(?i)CAT\.?\s*NO\.?\s*:?\s*([A-Z]{1,5}[0-9]{3,8})`)
	priceRE = regexp.MustCompile(`\$[0-9]+(?:\.[0-9]{2})?`)
)

// productPage is the structured data parsed from a Jaycar product page.
type productPage struct {
	URL         string
	CatalogueNo string
	Name        string
	Price       string
	Overview    string
	Image       string
	Datasheet   string
	Specs       map[string]string
	found       bool // true when any real product content was identified
}

// ParseProductPage parses a Jaycar product page from the raw HTML in r.
//
// Parsing is purely local: r can be fetched by any acquisition path (HTTP,
// a saved file, or a browser-supplied page) and ParseProductPage never
// touches the network. Callers get a fully populated PartDetailDTO —
// name, CAT.NO, price, overview, image, datasheet and spec table.
func ParseProductPage(productURL string, r io.Reader) (*suppliers.PartDetailDTO, error) {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return nil, fmt.Errorf("jaycar parse product page: %w", err)
	}

	p := &productPage{URL: productURL, Specs: make(map[string]string)}

	parseName(doc, p)
	parseCatalogueNumber(doc, p)
	parseJSONLD(doc, p)
	parsePrice(doc, p)
	parseImage(doc, p)
	parseDatasheet(doc, p)
	parseOverview(doc, p)
	parseSpecifications(doc, p)

	if !p.found {
		return nil, fmt.Errorf("jaycar: no product data found on page (bot challenge page?)")
	}

	return p.toDTO(), nil
}

func parseName(doc *goquery.Document, p *productPage) {
	p.Name = clean(doc.Find("h1").First().Text())
	if p.Name != "" {
		p.found = true
	}
}

func parseCatalogueNumber(doc *goquery.Document, p *productPage) {
	pageText := clean(doc.Text())
	if m := catNoRE.FindStringSubmatch(pageText); len(m) == 2 {
		p.CatalogueNo = m[1]
		p.found = true
	}
	if p.CatalogueNo == "" {
		if m := skuRE.FindStringSubmatch(p.URL); len(m) == 2 {
			p.CatalogueNo = m[1]
		}
	}
}

// parseJSONLD reads the application/ld+json blocks for the Product record.
func parseJSONLD(doc *goquery.Document, p *productPage) {
	doc.Find(`script[type="application/ld+json"]`).Each(func(i int, s *goquery.Selection) {
		var data interface{}
		if err := json.Unmarshal([]byte(s.Text()), &data); err != nil {
			return
		}
		walkProductJSON(data, p)
	})
}

func walkProductJSON(v interface{}, p *productPage) {
	switch x := v.(type) {
	case map[string]interface{}:
		typ, _ := x["@type"].(string)
		if strings.EqualFold(typ, "Product") {
			p.found = true
			if name, ok := x["name"].(string); ok && p.Name == "" {
				p.Name = clean(name)
			}
			switch image := x["image"].(type) {
			case string:
				if p.Image == "" {
					p.Image = image
				}
			case []interface{}:
				if len(image) > 0 {
					if s, ok := image[0].(string); ok && p.Image == "" {
						p.Image = s
					}
				}
			}
			if offers, ok := x["offers"].(map[string]interface{}); ok {
				if price, ok := offers["price"]; ok && p.Price == "" {
					p.Price = fmt.Sprintf("$%v", price)
				}
			}
			if sku, ok := x["sku"].(string); ok && p.CatalogueNo == "" {
				p.CatalogueNo = sku
			}
		}
		for _, child := range x {
			walkProductJSON(child, p)
		}
	case []interface{}:
		for _, child := range x {
			walkProductJSON(child, p)
		}
	}
}

func parsePrice(doc *goquery.Document, p *productPage) {
	if p.Price != "" {
		return
	}
	if m := priceRE.FindString(clean(doc.Text())); m != "" {
		p.Price = m
	}
}

func parseImage(doc *goquery.Document, p *productPage) {
	if p.Image != "" {
		return
	}
	doc.Find("img").EachWithBreak(func(i int, s *goquery.Selection) bool {
		alt, _ := s.Attr("alt")
		src, _ := s.Attr("src")
		if src != "" && p.Name != "" &&
			strings.Contains(strings.ToLower(alt), strings.ToLower(firstWords(p.Name, 3))) {
			p.Image = absoluteURL(src)
			return false
		}
		return true
	})
}

func parseDatasheet(doc *goquery.Document, p *productPage) {
	doc.Find("a").EachWithBreak(func(i int, s *goquery.Selection) bool {
		text := strings.ToLower(clean(s.Text()))
		href, exists := s.Attr("href")
		if exists && (strings.Contains(text, "datasheet") || strings.Contains(text, "manual")) {
			p.Datasheet = absoluteURL(href)
			return false
		}
		return true
	})
}

func parseOverview(doc *goquery.Document, p *productPage) {
	doc.Find("h2, h3").Each(func(i int, heading *goquery.Selection) {
		if strings.ToLower(clean(heading.Text())) != "overview" {
			return
		}
		n := heading.Next()
		for n.Length() > 0 {
			if goquery.NodeName(n) == "h2" || goquery.NodeName(n) == "h3" {
				break
			}
			if txt := clean(n.Text()); txt != "" {
				if p.Overview != "" {
					p.Overview += " "
				}
				p.Overview += txt
			}
			n = n.Next()
		}
	})
}

// parseSpecifications scans the content following the "Specifications" heading
// and pairs the label/value cells into p.Specs. Jaycar renders specs as a
// label/value table, e.g.:
//
//	Component Type   | Metal Film Resistor
//	Pack Quantity    | 8.0 pc
//	Resistance level | 100.0 Ω
func parseSpecifications(doc *goquery.Document, p *productPage) {
	var specHeading *goquery.Selection
	doc.Find("h2, h3").EachWithBreak(func(i int, s *goquery.Selection) bool {
		if strings.EqualFold(clean(s.Text()), "Specifications") {
			specHeading = s
			return false
		}
		return true
	})
	if specHeading == nil {
		return
	}

	var values []string
	node := specHeading.Next()
	for node.Length() > 0 {
		if goquery.NodeName(node) == "h2" || goquery.NodeName(node) == "h3" {
			break
		}
		// Leaf elements only, to avoid repeated parent text.
		node.Find("*").Each(func(i int, s *goquery.Selection) {
			if s.Children().Length() == 0 {
				if txt := clean(s.Text()); txt != "" && txt != "Specifications" {
					values = append(values, txt)
				}
			}
		})
		// Direct text containers.
		if node.Children().Length() == 0 {
			if txt := clean(node.Text()); txt != "" {
				values = append(values, txt)
			}
		}
		node = node.Next()
	}

	values = uniqueAdjacent(values)
	for i := 0; i+1 < len(values); i += 2 {
		key, value := clean(values[i]), clean(values[i+1])
		if key != "" && value != "" && key != value {
			p.Specs[key] = value
		}
	}
}

// toDTO maps the parsed product page onto the suppliers PartDetailDTO.
func (p *productPage) toDTO() *suppliers.PartDetailDTO {
	dto := &suppliers.PartDetailDTO{
		SearchResultDTO: suppliers.SearchResultDTO{
			ProviderKey:     "jaycar",
			ProviderID:      p.CatalogueNo,
			Name:            p.Name,
			Description:     p.Overview,
			Manufacturer:    "Jaycar Electronics",
			MPN:             p.CatalogueNo,
			PreviewImageURL: p.Image,
			ProviderURL:     p.URL,
		},
		Notes: p.Overview,
	}

	if p.Image != "" {
		dto.Images = append(dto.Images, suppliers.FileDTO{
			URL:  p.Image,
			Name: p.CatalogueNo + ".jpg",
		})
	}
	if p.Datasheet != "" {
		dto.Datasheets = append(dto.Datasheets, suppliers.FileDTO{
			URL:  p.Datasheet,
			Name: p.CatalogueNo + "-datasheet.pdf",
		})
	}

	if p.Price != "" {
		vi := suppliers.PurchaseInfoDTO{
			DistributorName: "Jaycar Electronics",
			OrderNumber:     p.CatalogueNo,
			ProductURL:      p.URL,
			Price:           p.Price,
			Currency:        "AUD",
			MinimumOrderQty: "1",
			InStock:         true,
		}
		vi.Prices = append(vi.Prices, suppliers.PriceDTO{
			MinQuantity:          1,
			Price:                p.Price,
			Currency:             "AUD",
			IncludesTax:          true,
			PriceRelatedQuantity: 1,
		})
		dto.VendorInfos = append(dto.VendorInfos, vi)
	}

	// Deterministic parameter order.
	for _, key := range sortedKeys(p.Specs) {
		dto.Parameters = append(dto.Parameters, suppliers.ParameterDTO{
			Name:      key,
			ValueText: p.Specs[key],
			Group:     "Technical",
		})
	}

	return dto
}

func clean(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func absoluteURL(u string) string {
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	if strings.HasPrefix(u, "/") {
		return "https://www.jaycar.com.au" + u
	}
	return u
}

func firstWords(s string, n int) string {
	parts := strings.Fields(s)
	if len(parts) <= n {
		return s
	}
	return strings.Join(parts[:n], " ")
}

func uniqueAdjacent(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}