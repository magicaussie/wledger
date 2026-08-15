package jaycar

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// testBFF is an in-memory mock of the Storefront Cloud BFF API.
type testBFF struct {
	t           *testing.T
	mu          sync.Mutex
	tokenCalls  int
	blocked     bool
	redirectTo  string
	products    map[string][]jaycarListing // keyed by search q
	sku         map[string]jaycarListing   // keyed by CAT.NO
	page        map[string][]byte          // keyed by canonical slug path
}

func newTestBFF(t *testing.T) *testBFF {
	return &testBFF{
		t:        t,
		products: map[string][]jaycarListing{},
		sku:      map[string]jaycarListing{},
		page:     map[string][]byte{},
	}
}

func (s *testBFF) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bff/auth/accessToken" {
			s.mu.Lock()
			s.tokenCalls++
			s.mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{
				"accessToken": "eyJhbGciOiJIUzI1NiJ9.eyJleHAiOjQxMDI0NDQ4MDB9.sig",
			})
			return
		}
		if s.blocked {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("<html>Please enable JS and disable any ad blocker</html>"))
			return
		}
		switch r.URL.Path {
		case "/bff/products/list":
			q := r.URL.Query()
			if sku := q.Get("sku"); sku != "" {
				p, ok := s.sku[strings.ToUpper(sku)]
				if !ok {
					json.NewEncoder(w).Encode(map[string]interface{}{"totalProducts": 0, "products": []jaycarListing{}})
					return
				}
				json.NewEncoder(w).Encode(map[string]interface{}{"totalProducts": 1, "products": []jaycarListing{p}})
				return
			}
			prods := s.products[q.Get("q")]
			if prods == nil {
				prods = []jaycarListing{}
			}
			json.NewEncoder(w).Encode(map[string]interface{}{"totalProducts": len(prods), "products": prods})
		case "/bff/page":
			slug := r.URL.Query().Get("slug")
			if strings.HasPrefix(slug, "/p/") && s.redirectTo != "" {
				json.NewEncoder(w).Encode(map[string]interface{}{
					"data": map[string]interface{}{
						"redirect": map[string]interface{}{"url": s.redirectTo, "status": "permanent"},
					},
				})
				return
			}
			body, ok := s.page[slug]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"message":"no page"}`))
				return
			}
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

// xc4324Listing is the catalogue record for the BBC micro:bit V2 Go bundle.
func xc4324Listing() jaycarListing {
	return jaycarListing{
		Title:                "BBC micro:bit V2 Go Development Board Bundle",
		URL:                  "/bbc-micro-bit-v2-go-development-board-bundle/p/XC4324",
		Sku:                  "XC4324",
		BrandName:            "BBC",
		InStock:              true,
		AvailableForDelivery: "Available for delivery",
		Category1Name:        "Toys, Hobbies & STEM",
		Category2Name:        "Arduino",
		RegularPrice:         &jaycarPrice{CentAmount: 5695, CurrencyCode: "AUD"},
		FinalPrice:           &jaycarPrice{CentAmount: 5695, CurrencyCode: "AUD"},
		Thumbnail: struct {
			Src string `json:"src"`
		}{Src: "https://media.jaycar.com.au/product/images/XC4324_bbc-micro-bit-v2-go-development-board-bundle_99665.jpg"},
	}
}

// xc4324Page builds the /bff/page payload with ProductMain + ProductDetails.
func xc4324Page() []byte {
	prod := productDetail{
		Title:   "BBC micro:bit V2 Go Development Board Bundle",
		URL:     "/bbc-micro-bit-v2-go-development-board-bundle/p/XC4324",
		Sku:     "XC4324",
		CatNo:   "XC4324",
		BrandName: "BBC",
		InStock: true,
		Category1Name: "Toys, Hobbies & STEM",
		FinalPrice: jaycarPrice{CentAmount: 5695, CurrencyCode: "AUD"},
		MultiBuyTiers: []multiBuyTier{
			{MinimumQuantity: 3, FinalPrice: jaycarPrice{CentAmount: 5095, CurrencyCode: "AUD"}},
		},
	}
	prod.Carousel.Slides = []carouselSlide{
		{Type: "image", Src: "XC4324_bbc-micro-bit-v2-go-development-board-bundle_99665.jpg", AltText: "BBC micro:bit V2 Go Development Board Bundle"},
		{Type: "image", Src: "XC4324_bbc-micro-bit-v2-go-development-board-bundle_99663.jpg", AltText: "BBC micro:bit V2 Go Development Board Bundle"},
	}
	main := map[string]interface{}{
		"__typename":         "ProductMain",
		"product":            prod,
		"supersededProducts": []supersededProduct{},
	}
	details := map[string]interface{}{
		"__typename": "ProductDetails",
		"specification": map[string]interface{}{
			"items": []map[string]interface{}{
				{
					"title": "Product Dimensions",
					"attributes": []map[string]interface{}{
						{"title": "Length", "value": "52.0 mm"},
						{"title": "Width", "value": "43.0 mm"},
						{"title": "Weight", "value": "6.0 g"},
					},
				},
			},
		},
		"overview": map[string]interface{}{
			"content": contentNode{
				Type: "doc",
				Children: []contentNode{
					{
						Type: "paragraph",
						Children: []contentNode{
							{Type: "text", Text: "The micro:bit is an open source system originally designed by the BBC for use in computer education in the UK and is now available in Australia."},
						},
					},
				},
			},
		},
		"downloads": map[string]interface{}{
			"items": []map[string]interface{}{
				{"title": "Manual for XC4324", "link": "XC4324_manual_1.pdf"},
			},
		},
	}
	sections := []json.RawMessage{mustRaw(main), mustRaw(details)}
	return mustRaw(map[string]interface{}{
		"data": map[string]interface{}{
			"page": map[string]interface{}{"sections": sections},
		},
	})
}

func mustRaw(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// newAPITestProvider wires the provider to the mock BFF with an isolated
// catalogue cache so tests never read or write each other's persisted data.
func newAPITestProvider(t *testing.T, bff *testBFF) (*Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(bff.handler())
	t.Cleanup(srv.Close)
	api := newAPIClientForTest(srv.Client(), srv.URL)
	prov := NewProviderWithAPI(NewFileFetcher("testdata"), api)
	prov.catalogue = newCatalogue(filepath.Join(t.TempDir(), "catalogue.json"), testLogger())
	return prov, srv
}

func TestAPISearchExactSKU(t *testing.T) {
	bff := newTestBFF(t)
	bff.products["XC4324"] = []jaycarListing{
		xc4324Listing(),
		{Title: "Acrylic Enclosure for XC4624 RGB LED Cube", Sku: "XC4625", URL: "/acrylic-enclosure-for-xc4624/p/XC4625", Category1Name: "Toys, Hobbies & STEM"},
	}
	prov, _ := newAPITestProvider(t, bff)

	results, err := prov.SearchByKeyword(context.Background(), "XC4324")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].ProviderID != "XC4324" {
		t.Errorf("exact SKU match should be first, got %q", results[0].ProviderID)
	}
	if results[0].Name != "BBC micro:bit V2 Go Development Board Bundle" {
		t.Errorf("unexpected name: %q", results[0].Name)
	}
	if results[0].MPN != "XC4324" {
		t.Errorf("unexpected MPN: %q", results[0].MPN)
	}
	if !strings.HasSuffix(results[0].ProviderURL, "/p/XC4324") {
		t.Errorf("unexpected URL: %q", results[0].ProviderURL)
	}
	if results[0].PreviewImageURL == "" {
		t.Errorf("expected preview image")
	}
}

func TestAPISearchKeywords(t *testing.T) {
	bff := newTestBFF(t)
	bff.products["fuse"] = []jaycarListing{
		{Title: "Automotive Fuse Assortment", Sku: "SF2142", URL: "/automotive-fuse-assortment/p/SF2142", Category1Name: "Fuses & Relays"},
		{Title: "30A 32VDC Water Resistant Inline Standard Blade Fuse Holder", Sku: "SZ2042", URL: "/inline-standard-blade-fuse-holder/p/SZ2042", Category1Name: "Fuses & Relays"},
		{Title: "200A 32V MIDI Inline Bolt Down Fuse Holder", Sku: "SZ2079", URL: "/midi-inline-bolt-down-fuse-holder/p/SZ2079", Category1Name: "Fuses & Relays"},
		{Title: "50A Red MIDI Fuse", Sku: "SF2032", URL: "/50a-red-midi-fuse/p/SF2032", Category1Name: "Fuses & Relays"},
	}
	prov, _ := newAPITestProvider(t, bff)

	results, err := prov.SearchByKeyword(context.Background(), "fuse")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) < 3 {
		t.Fatalf("expected multiple fuse results, got %d", len(results))
	}
}

func TestAPISearchInvalidSKU(t *testing.T) {
	bff := newTestBFF(t)
	bff.products["ZZ99999999"] = []jaycarListing{}
	prov, _ := newAPITestProvider(t, bff)

	results, err := prov.SearchByKeyword(context.Background(), "ZZ99999999")
	if err != nil {
		t.Fatalf("invalid SKU should be a valid empty search, got error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func TestAPIClientSearchBlocked(t *testing.T) {
	bff := newTestBFF(t)
	bff.blocked = true
	srv := httptest.NewServer(bff.handler())
	t.Cleanup(srv.Close)
	api := newAPIClientForTest(srv.Client(), srv.URL)

	_, err := api.searchProducts(context.Background(), "XC4324")
	if err == nil {
		t.Fatal("expected error when BFF returns a challenge")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "bot protection") {
		t.Errorf("expected bot protection error, got: %v", err)
	}
}

func TestSearchByKeywordBlockedReturnsError(t *testing.T) {
	bff := newTestBFF(t)
	bff.blocked = true
	prov, _ := newAPITestProvider(t, bff)

	// API blocked + empty cache + no usable HTML page must yield an error,
	// never a fabricated result set.
	results, err := prov.SearchByKeyword(context.Background(), "XC4324")
	if err == nil {
		t.Fatalf("expected error, got %d results", len(results))
	}
	if len(results) != 0 {
		t.Fatalf("expected no results, got %d", len(results))
	}
}

func TestAPISearchTokenCached(t *testing.T) {
	bff := newTestBFF(t)
	bff.products["fuse"] = []jaycarListing{{Title: "Automotive Fuse Assortment", Sku: "SF2142", URL: "/automotive-fuse-assortment/p/SF2142"}}
	prov, _ := newAPITestProvider(t, bff)

	for i := 0; i < 3; i++ {
		if _, err := prov.SearchByKeyword(context.Background(), "fuse"); err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
	}
	bff.mu.Lock()
	calls := bff.tokenCalls
	bff.mu.Unlock()
	if calls != 1 {
		t.Errorf("expected 1 token fetch, got %d", calls)
	}
}

func TestAPIGetDetailsXC4324(t *testing.T) {
	bff := newTestBFF(t)
	bff.sku["XC4324"] = xc4324Listing()
	canonical := "/bbc-micro-bit-v2-go-development-board-bundle/p/XC4324"
	bff.page[canonical] = xc4324Page()
	prov, _ := newAPITestProvider(t, bff)

	detail, err := prov.GetDetails(context.Background(), "XC4324")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}

	if detail.ProviderID != "XC4324" {
		t.Errorf("expected CAT.NO XC4324, got %q", detail.ProviderID)
	}
	if detail.Name != "BBC micro:bit V2 Go Development Board Bundle" {
		t.Errorf("unexpected name: %q", detail.Name)
	}
	if detail.ProviderURL != "https://www.jaycar.com.au/bbc-micro-bit-v2-go-development-board-bundle/p/XC4324" {
		t.Errorf("unexpected URL: %q", detail.ProviderURL)
	}

	if len(detail.VendorInfos) != 1 {
		t.Fatalf("expected 1 vendor info, got %d", len(detail.VendorInfos))
	}
	vi := detail.VendorInfos[0]
	if vi.Price != "$56.95" {
		t.Errorf("expected price $56.95, got %q", vi.Price)
	}
	if !vi.InStock {
		t.Errorf("expected in stock")
	}
	if len(vi.Prices) != 2 {
		t.Errorf("expected 2 price breaks (unit + multibuy), got %+v", vi.Prices)
	}

	if !strings.Contains(detail.Description, "open source system originally designed by the BBC") {
		t.Errorf("expected overview text, got %q", detail.Description)
	}

	found := map[string]bool{}
	for _, p := range detail.Parameters {
		if p.Group == "Product Dimensions" {
			found[p.Name] = p.ValueText == "52.0 mm"
		}
	}
	if !found["Length"] {
		t.Errorf("expected spec Length in Product Dimensions group, got %+v", detail.Parameters)
	}

	if len(detail.Datasheets) != 1 || !strings.Contains(detail.Datasheets[0].URL, "XC4324_manual_1.pdf") {
		t.Errorf("expected datasheet, got %+v", detail.Datasheets)
	}
	if len(detail.Images) < 2 {
		t.Errorf("expected gallery images from carousel, got %+v", detail.Images)
	}
	if detail.Images[0].URL != "https://media.jaycar.com.au/product/images/XC4324_bbc-micro-bit-v2-go-development-board-bundle_99665.jpg" {
		t.Errorf("unexpected image URL: %q", detail.Images[0].URL)
	}
}

func TestAPIGetDetailsNoSuchSKU(t *testing.T) {
	bff := newTestBFF(t)
	bff.products["ZZ99999999"] = []jaycarListing{}
	prov, _ := newAPITestProvider(t, bff)

	_, err := prov.GetDetails(context.Background(), "ZZ99999999")
	if err == nil {
		t.Fatal("expected error for unknown CAT.NO")
	}
	if !strings.Contains(err.Error(), "no product found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAPIGetDetailsBlockedFallsBackToHTML(t *testing.T) {
	bff := newTestBFF(t)
	bff.blocked = true
	prov, _ := newAPITestProvider(t, bff)

	// The BFF is blocked, so GetDetails must fall back to the HTML fetcher
	// (FileFetcher rooted at testdata) and still return the parsed product.
	detail, err := prov.GetDetails(context.Background(), "XC0416")
	if err != nil {
		t.Fatalf("GetDetails fallback: %v", err)
	}
	if detail.ProviderID != "XC0416" {
		t.Errorf("expected XC0416 from HTML fallback, got %q", detail.ProviderID)
	}
}

func TestAPIPageRedirectFollowed(t *testing.T) {
	bff := newTestBFF(t)
	bff.sku["XC4324"] = xc4324Listing()
	canonical := "/bbc-micro-bit-v2-go-development-board-bundle/p/XC4324"
	bff.redirectTo = canonical
	bff.page[canonical] = xc4324Page()
	prov, _ := newAPITestProvider(t, bff)

	detail, err := prov.GetDetails(context.Background(), "XC4324")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if detail.Name != "BBC micro:bit V2 Go Development Board Bundle" {
		t.Errorf("expected detail after following redirect, got %q", detail.Name)
	}
}

func TestCataloguePersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jaycar_catalogue.json")

	c1 := newCatalogue(path, testLogger())
	c1.merge([]jaycarListing{xc4324Listing()})

	hits := c1.search("XC4324")
	if len(hits) != 1 || hits[0].SKU != "XC4324" {
		t.Fatalf("expected cached XC4324, got %+v", hits)
	}

	// A fresh instance loads the persisted catalogue.
	c2 := newCatalogue(path, testLogger())
	hits = c2.search("microbit")
	if len(hits) == 0 || hits[0].SKU != "XC4324" {
		t.Errorf("expected persisted entry to be searchable, got %+v", hits)
	}
}

func TestCatalogueSearchRanking(t *testing.T) {
	c := newCatalogue(filepath.Join(t.TempDir(), "catalogue.json"), testLogger())
	c.merge([]jaycarListing{
		{Title: "Spare Thermometer Sensor to Suit XC0322", Sku: "XC0324", URL: "/spare-thermometer/xc0324"},
		{Title: "BBC micro:bit V2 Go Development Board Bundle", Sku: "XC4324", URL: "/bbc-micro-bit/p/XC4324"},
		{Title: "Acrylic Enclosure for XC4624 RGB LED Cube", Sku: "XC4625", URL: "/enclosure/xc4625"},
	})

	hits := c.search("XC4324")
	if len(hits) == 0 || hits[0].SKU != "XC4324" {
		t.Fatalf("exact SKU should rank first, got %+v", hits)
	}
}

func TestRankListingExactFirst(t *testing.T) {
	products := []jaycarListing{
		{Title: "Spare Thermometer Sensor to Suit XC0322", Sku: "XC0324"},
		{Title: "BBC micro:bit V2 Go Development Board Bundle", Sku: "XC4324"},
	}
	rankListing("XC4324", products)
	if products[0].Sku != "XC4324" {
		t.Errorf("expected exact SKU first, got %q", products[0].Sku)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
