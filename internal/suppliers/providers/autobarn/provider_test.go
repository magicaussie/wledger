package autobarn

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const searchHTML = `<!DOCTYPE html><html><head><title>Search</title></head><body>
<div class="listing-product-tile md:w-1/4 lg:p-3" data-insights-object-id="EL05408" data-insights-position="1">
  <a href="/ab/Autobarn-Category/.../p/EL05408?queryID=abc123&amp;indexUsed=autobarnProductIndex" data-discover="true">
    <div class="listing-product-tile-image"><img src="https://medias.autobarn.com.au/medias/300Wx300H-EL05408-1.png" alt="SuperCharge Gold Plus SMF CAL 12V 640CCA 62Ah Car Battery"/></div>
  </a>
  <div class="pb-2 text-base font-bold"><a href="/ab/Autobarn-Category/.../p/EL05408"><span class="line-clamp-2">SuperCharge Gold Plus SMF CAL 12V 640CCA 62Ah Car Battery</span></a></div>
  <div class="mb-4"><div class="font-sans text-lg font-bold leading-[22px] text-sale undefined">$<!-- -->198.74</div></div>
  <div class="font-sans font-bold text-base text-gray-500 line-through">$399.99</div>
</div>
<div class="listing-product-tile" data-insights-object-id="148260">
  <a href="/ab/.../Nulon-APEX%2B-5W-40/p/148260"><span class="line-clamp-2">Nulon APEX+ 5W-40 Oil 5L</span></a>
  <div class="font-sans text-lg font-bold text-brand-primary-black">$95.99</div>
</div>
</body></html>`

const pdpHTML = `<!DOCTYPE html><html><head><title>SuperCharge Gold Plus SMF CAL 12V 640CCA 62Ah Car Battery</title></head><body>
<script type="application/ld+json">{"@context":"https://schema.org","@type":"Product","sku":"EL05408","name":"SuperCharge Gold Plus SMF CAL 12V 640CCA 62Ah Car Battery","description":"High retention battery.","mpn":"MF55R","image":"https://medias.autobarn.com.au/300Wx300H-EL05408.png","brand":{"@type":"Brand","name":"SUPERCHARGE"},"url":"https://autobarn.com.au/ab/.../p/EL05408","offers":{"@type":"Offer","availability":"https://schema.org/InStock","price":198.99,"priceCurrency":"AUD"}}</script>
<div class="line-through">$399.99</div>
</body></html>`

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
		{"https://autobarn.com.au/ab/Autobarn-Category/.../Car-Battery/p/EL05408", "EL05408", true},
		{"https://autobarn.com.au/ab/.../p/148260", "148260", true},
		{"https://autobarn.com.au/ab/search?text=battery", "", false},
		{"https://other.com/p/x", "", false},
	}
	for _, c := range cases {
		id, ok := p.ExtractPartIDFromURL(c.url)
		if id != c.id || ok != c.ok {
			t.Errorf("ExtractPartIDFromURL(%q) = (%q, %v), want (%q, %v)", c.url, id, ok, c.id, c.ok)
		}
	}
}

func TestCleanProductURL(t *testing.T) {
	raw := "/ab/Autobarn-Category/.../p/EL05408?queryID=abc123&amp;indexUsed=autobarnProductIndex"
	want := "https://autobarn.com.au/ab/Autobarn-Category/.../p/EL05408"
	if got := cleanProductURL(raw); got != want {
		t.Errorf("cleanProductURL got %q want %q", got, want)
	}
}

func TestSearchByKeyword(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://autobarn.com.au/ab/search?text=battery": searchHTML,
	})
	p := NewProviderWithClient(client)
	if !strings.Contains(p.ua, "Mozilla") {
		t.Fatal("missing UA")
	}
	ctx := context.Background()
	results, err := p.SearchByKeyword(ctx, "battery")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	first := results[0]
	if first.ProviderID != "EL05408" {
		t.Errorf("expected EL05408, got %q", first.ProviderID)
	}
	if !strings.Contains(first.Description, "198.74") {
		t.Errorf("expected price in description, got %q", first.Description)
	}
	if !strings.Contains(first.Description, "399.99") {
		t.Errorf("expected was price in description, got %q", first.Description)
	}
	if !strings.Contains(first.PreviewImageURL, "medias.autobarn") {
		t.Errorf("expected image URL, got %q", first.PreviewImageURL)
	}
}

func TestSearchByKeywordBlocked(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://autobarn.com.au/ab/search?text=blocked": "<html><body>verify you are human</body></html>",
	})
	p := NewProviderWithClient(client)
	if _, err := p.SearchByKeyword(context.Background(), "blocked"); err == nil {
		t.Fatal("expected challenge error")
	}
}

func TestGetDetails(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://autobarn.com.au/ab/search?text=EL05408":  searchHTML,
		"https://autobarn.com.au/ab/Autobarn-Category/.../p/EL05408": pdpHTML,
	})
	p := NewProviderWithClient(client)
	d, err := p.GetDetails(context.Background(), "EL05408")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if d.ProviderID != "EL05408" {
		t.Errorf("expected EL05408, got %q", d.ProviderID)
	}
	if d.Manufacturer != "SUPERCHARGE" {
		t.Errorf("expected SUPERCHARGE brand, got %q", d.Manufacturer)
	}
	if len(d.VendorInfos) == 0 || d.VendorInfos[0].Price != "198.99" {
		t.Errorf("expected price 198.99, got %+v", d.VendorInfos)
	}
	if d.VendorInfos[0].Currency != "AUD" {
		t.Errorf("expected AUD currency, got %q", d.VendorInfos[0].Currency)
	}
}