package rs

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// searchSingleHTML is a single-product (exact MPN/stock no) response.
const searchSingleHTML = `<!DOCTYPE html><html><head><title>LC1D09U7 | Schneider Electric LC</title></head><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"noResults":false,"productListData":{"name":"Schneider Electric LC1D Contactor, 240V ac Coil, 3-Pole","mpn":"LC1D09U7","sku":"187-920","url":"https://au.rs-online.com/web/p/contactors/0187920","image":"https://media.rs-online.com/t_large/R0187920-01.jpg","brand":{"name":"Schneider Electric"},"offers":{"price":"89.16"},"additionalProperty":[{"name":"Coil Voltage","value":"240V ac"},{"name":"Number of Poles","value":"3"}]},"discoverData":{},"articleResult":{},"productAvailabilityResult":{}}}}</script>
</body></html>`

// searchGridHTML is a multi-result search response.
const searchGridHTML = `<!DOCTYPE html><html><head><title>Search</title></head><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"noResults":false,"discoverData":{"pagination":{"total":2,"page":1,"lastPage":1},"records":[{"id":"0187920","article":[{"title":"Schneider Electric LC1D Contactor, 240V ac Coil, 3-Pole","discoverArticleAttributes":{"displayPrice":"$89.16","price":"89.16","brand":"Schneider Electric","image":"https://media.rs-online.com/t_large/R0187920-01.jpg","mpn":"LC1D09U7","productURL":"https://au.rs-online.com/web/p/contactors/0187920","stockStatus":"IN_STOCK"}}]},{"id":"2371600","article":[{"title":"ABB B Contactor, 240V ac Coil","discoverArticleAttributes":{"displayPrice":"$33.66","price":"33.66","brand":"ABB","image":"https://media.rs-online.com/t_large/R2371600-01.jpg","mpn":"GJL1311009R8010","productURL":"https://au.rs-online.com/web/p/contactors/2371600","stockStatus":"IN_STOCK"}}]}]},"productListData":null,"articleResult":{},"productAvailabilityResult":{}}}}</script>
</body></html>`

// detailHTML is a product page response with full article data.
const detailHTML = `<!DOCTYPE html><html><head><title>Schneider Electric LC1D Contactor</title></head><body>
<script id="__NEXT_DATA__" type="application/json">{"props":{"pageProps":{"noResults":false,"productListData":{"name":"Schneider Electric LC1D Contactor, 240V ac Coil, 3-Pole","mpn":"LC1D09U7","sku":"187-920","url":"https://au.rs-online.com/web/p/contactors/0187920","image":"https://media.rs-online.com/R0187920-01.jpg","brand":{"name":"Schneider Electric"},"offers":{"price":"89.16"},"additionalProperty":[]},"discoverData":{},"articleResult":{"data":{"article":{"rsStockNumber":"187-920","manufacturerPartNumber":"LC1D09U7","brand":"Schneider Electric","longDescription":"TeSys Deca contactor.","productUrl":"/p/contactors/0187920","seoCategoryName":"Contactors","countryOfOrigin":"FR","rohsStatus":"E","minimumOrderQty":1,"packSize":1,"taxRatePercentage":10,"thumbnailImageURL":"https://media.rs-online.com/t_thumb/R0187920-01.jpg","images":["R0187920-01.jpg","F0187920-02.jpg"],"documents":[{"title":"Datasheet LC1D09U7","type":"data_sheet","url":"https://docs.rs-online.com/8602/A700000007891159.pdf"}],"prices":{"currencyCode":"AUD","priceBreaks":[{"id":"b1","price":"$89.16","roundedPrice":"89.16","roundedVatIncPrice":"98.08","vatInclusivePrice":"$98.08"}],"wasPrice":""},"priceBreaks":[{"price":"89.16","quantity":"1"}],"specificationAttributes":[{"key":"Coil Voltage","value":"240V ac"},{"key":"Number of Poles","value":"3"}]}}},"productAvailabilityResult":{"data":{"articleId":"187920","status":"In Stock","statusCode":"IN_STOCK","quantity":37,"totalAvailable":244,"addToCartEnabled":true,"backOrderAllowed":true}}}}}</script>
</body></html>`

const blockedHTML = `<html><head><title>Access Denied</title></head><body><h1>Access Denied</h1><p>Reference #18.x</p></body></html>`

func fakeClient(responses map[string]string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := responses[req.URL.String()]
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestExtractPartIDFromURL(t *testing.T) {
	p := NewProvider()
	cases := []struct {
		url string
		id  string
		ok  bool
	}{
		{"https://au.rs-online.com/web/p/contactors/0187920", "0187920", true},
		{"https://uk.rs-online.com/web/p/contactors/0187920", "0187920", true},
		{"https://au.rs-online.com/web/c/?searchTerm=test", "", false},
		{"https://example.com/p/0187920", "", false},
	}
	for _, c := range cases {
		id, ok := p.ExtractPartIDFromURL(c.url)
		if id != c.id || ok != c.ok {
			t.Errorf("ExtractPartIDFromURL(%q) = (%q, %v), want (%q, %v)", c.url, id, ok, c.id, c.ok)
		}
	}
}

func TestSearchExactMPN(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://au.rs-online.com/web/c/?searchTerm=LC1D09U7": searchSingleHTML,
	})
	p := NewProviderWithClient(client)
	results, err := p.SearchByKeyword(context.Background(), "LC1D09U7")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.ProviderID != "187-920" {
		t.Errorf("expected RS stock 187-920, got %q", r.ProviderID)
	}
	if r.MPN != "LC1D09U7" {
		t.Errorf("expected MPN LC1D09U7, got %q", r.MPN)
	}
	if r.Manufacturer != "Schneider Electric" {
		t.Errorf("expected Schneider Electric, got %q", r.Manufacturer)
	}
}

func TestSearchGrid(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://au.rs-online.com/web/c/?searchTerm=240V+contactor": searchGridHTML,
	})
	p := NewProviderWithClient(client)
	results, err := p.SearchByKeyword(context.Background(), "240V contactor")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	first := results[0]
	if first.ProviderID != "0187920" || first.MPN != "LC1D09U7" {
		t.Errorf("unexpected first result %+v", first)
	}
	if !strings.Contains(first.Description, "89.16") {
		t.Errorf("expected price in description, got %q", first.Description)
	}
}

func TestSearchBlocked(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://au.rs-online.com/web/c/?searchTerm=blocked": blockedHTML,
	})
	p := NewProviderWithClient(client)
	if _, err := p.SearchByKeyword(context.Background(), "blocked"); err == nil {
		t.Fatal("expected block error")
	}
}

func TestGetDetails(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://au.rs-online.com/web/c/?searchTerm=187-920": searchSingleHTML,
		"https://au.rs-online.com/web/p/contactors/0187920": detailHTML,
	})
	p := NewProviderWithClient(client)
	d, err := p.GetDetails(context.Background(), "187-920")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if d.ProviderID != "187-920" {
		t.Errorf("expected 187-920, got %q", d.ProviderID)
	}
	if d.MPN != "LC1D09U7" {
		t.Errorf("expected LC1D09U7, got %q", d.MPN)
	}
	if len(d.VendorInfos) == 0 {
		t.Fatal("no vendor info")
	}
	vi := d.VendorInfos[0]
	if vi.Price != "89.16" {
		t.Errorf("expected ex-GST 89.16, got %q", vi.Price)
	}
	if len(vi.Prices) != 2 {
		t.Fatalf("expected 2 price points, got %d", len(vi.Prices))
	}
	if vi.Prices[0].IncludesTax != false || vi.Prices[1].IncludesTax != true {
		t.Errorf("expected ex/inc GST flags, got %+v", vi.Prices)
	}
	if len(d.Datasheets) == 0 || d.Datasheets[0].URL != "https://docs.rs-online.com/8602/A700000007891159.pdf" {
		t.Errorf("unexpected datasheets %+v", d.Datasheets)
	}
	if !vi.InStock {
		t.Error("expected in stock")
	}
	// specs + stock + pricing params present
	groups := map[string]bool{}
	for _, prm := range d.Parameters {
		groups[prm.Group] = true
		if prm.Name == "Coil Voltage" && prm.ValueText != "240V ac" {
			t.Errorf("unexpected coil voltage %q", prm.ValueText)
		}
	}
	for _, g := range []string{"Specifications", "Stock", "Pricing"} {
		if !groups[g] {
			t.Errorf("missing parameter group %q", g)
		}
	}
}
