package supercheap

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

const searchPage = `<!DOCTYPE html>
<html><head><title>Search</title></head><body>
<div class="search-result-content">
  <ul id="search-result-items" class="search-result-items">
    <li class="grid-tile">
      <div class="product-tile">
        <div class="product-image">
          <a class="thumb-link" href="/p/century-century-hi-performance-car-battery-55d23l-mf/602508.html">
            <img src="data:image/gif;base64,R0lGODlhAQAB" data-src="https://www.supercheapauto.com.au/dw/image/v2/BBRV_PRD/on/demandware.static/-/Sites-srg-internal-master-catalog/default/dw69982cfd/images/602508/SCA_602508_hi-res.jpg?sw=233&amp;sh=233&amp;sm=fit&amp;q=70" alt="Century"/>
          </a>
        </div>
        <h4 class="brand-name">Century</h4>
        <div class="product-name">
          <a class="name-link" href="/p/century-century-hi-performance-car-battery-55d23l-mf/602508.html" title="Go to Product: Century Hi Performance Car Battery 55D23L MF">Century Hi Performance Car Battery 55D23L MF</a>
        </div>
        <div class="product-pricing">
          <span class="product-sales-price"><span class="sp-nowrap"><span class="the-price">$234.99</span></span></span>
          <span class="product-standard-price"><span class="sp-nowrap"><span class="the-price">$279.99</span></span></span>
        </div>
        <div class="product-bv-rating">
          <div class="bv-item BVRRInlineRating">
            <span class="bv-rating-stars-on bv-rating-stars" style="width: 93%;"></span>
            <span class="bv-rating-ratio-count">(540)</span>
          </div>
        </div>
        <div class="delivery-availabilty">
          <div class="no-home-delivery unavailable"><i></i><span>Not available for delivery</span></div>
          <div class="customer-order-only available"><i></i><span>Pick up today</span></div>
        </div>
        <input class="gtm-product-impression" type="hidden" value="{&quot;item_id&quot;:&quot;602508&quot;,&quot;item_name&quot;:&quot;Century Hi Performance Car Battery 55D23L MF&quot;,&quot;item_brand&quot;:&quot;Century&quot;,&quot;price&quot;:234.99}"/>
      </div>
    </li>
    <li class="grid-tile">
      <div class="product-tile">
        <div class="product-name">
          <a class="name-link" href="/p/sca-sca-performance-car-battery-55d23l-smf/555035.html" title="Go to Product: SCA Performance Car Battery 55D23L SMF">SCA Performance Car Battery 55D23L SMF</a>
        </div>
        <h4 class="brand-name">SCA</h4>
        <div class="product-pricing">
          <span class="product-sales-price"><span class="sp-nowrap"><span class="the-price">$174.99</span></span></span>
        </div>
        <input class="gtm-product-impression" type="hidden" value="{&quot;item_id&quot;:&quot;555035&quot;,&quot;item_brand&quot;:&quot;SCA&quot;,&quot;price&quot;:174.99}"/>
      </div>
    </li>
  </ul>
</div>
</body></html>`

const productPage = `<!DOCTYPE html>
<html><head><title>Century Hi Performance Car Battery 55D23L MF | Supercheap Auto</title></head><body>
<script type="application/ld+json">{"@context":"https://schema.org/","@type":"Product","name":"Century Hi Performance Car Battery 55D23L MF","image":["https://www.supercheapauto.com.au/dw/image/v2/BBRV_PRD/on/demandware.static/-/Sites-srg-internal-master-catalog/default/dw69982cfd/images/602508/SCA_602508_hi-res.jpg?sw=558&amp;sh=558&amp;sm=fit&amp;q=60"],"description":"If you need a new battery.","brand":{"@type":"Brand","name":"Century"},"offers":{"@type":"Offer","availability":"https://schema.org/InStock","priceSpecification":[{"@type":"UnitPriceSpecification","price":"234.99","priceCurrency":"AUD"}]},"url":"https://www.supercheapauto.com.au/p/century-century-hi-performance-car-battery-55d23l-mf/602508.html","category":"Batteries &amp; Electrical","aggregateRating":{"@type":"AggregateRating","ratingValue":4.7,"reviewCount":540}}</script>
<div class="subtext product-number">Item No. <span data-masterid="602508">602508</span></div>
<div class="product-detail">
  <div id="product-features"><h3 class="tab-product-features">Features</h3><ul><li>Reliable starting power</li><li>Maintenance-free design</li></ul></div>
</div>
<table class="table-scroll"><tbody>
  <tr><th scope="row">Dimensions</th><td>232mm (L) x 173mm (W) x 202mm (H) x 225mm (TH)</td></tr>
  <tr><th scope="row">Voltage</th><td>12V</td></tr>
  <tr><th scope="row">Cold cranking amps</th><td>540CCA</td></tr>
  <tr><th scope="row">Warranty</th><td>30 months</td></tr>
</tbody></table>
</div>
</body></html>`

const challengePage = `<!DOCTYPE html><html><head><title>Attention Required</title></head><body><div>verify you are human</div></body></html>`

func fakeClient(responses map[string]string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			resp := &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(responses[req.URL.String()])),
				Header:     make(http.Header),
				Request:    req,
			}
			// If we can't find the body for the exact request URL but can find
			// the search body, simulate the SFCC redirect to the product page.
			if _, ok := responses[req.URL.String()]; !ok {
				if target, ok := responses["REDIRECT:"+req.URL.String()]; ok {
					resp.StatusCode = 302
					resp.Header.Set("Location", target)
					resp.Body = io.NopCloser(strings.NewReader(""))
					resp.Request = req
					return resp, nil
				}
			}
			return resp, nil
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
		{"https://www.supercheapauto.com.au/p/century-century-hi-performance-car-battery-55d23l-mf/602508.html", "602508", true},
		{"https://www.supercheapauto.com.au/search?q=battery", "", false},
		{"https://othersite.com/p/x/123.html", "", false},
	}
	for _, c := range cases {
		id, ok := p.ExtractPartIDFromURL(c.url)
		if id != c.id || ok != c.ok {
			t.Errorf("ExtractPartIDFromURL(%q) = (%q, %v), want (%q, %v)", c.url, id, ok, c.id, c.ok)
		}
	}
}

func TestSearchByKeyword(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://www.supercheapauto.com.au/search?q=55D23L": searchPage,
	})
	p := NewProviderWithClient(client)
	results, err := p.SearchByKeyword(context.Background(), "55D23L")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	first := results[0]
	if first.ProviderID != "602508" {
		t.Errorf("expected item 602508, got %q", first.ProviderID)
	}
	if first.Name != "Century Hi Performance Car Battery 55D23L MF" {
		t.Errorf("unexpected name %q", first.Name)
	}
	if first.Manufacturer != "Century" {
		t.Errorf("unexpected brand %q", first.Manufacturer)
	}
	if !strings.HasPrefix(first.PreviewImageURL, "https://www.supercheapauto.com.au/dw/image/v2/BBRV_PRD") {
		t.Errorf("image should be resolved from data-src, got %q", first.PreviewImageURL)
	}
	if !strings.Contains(first.Description, "234.99") {
		t.Errorf("description should contain price, got %q", first.Description)
	}
	if !strings.Contains(first.Description, "Was $279.99") {
		t.Errorf("description should contain was price, got %q", first.Description)
	}
}

func TestSearchByKeywordChallenge(t *testing.T) {
	client := fakeClient(map[string]string{
		"https://www.supercheapauto.com.au/search?q=blocked": challengePage,
	})
	p := NewProviderWithClient(client)
	if _, err := p.SearchByKeyword(context.Background(), "blocked"); err == nil {
		t.Fatal("expected challenge error")
	}
}

func TestGetDetails(t *testing.T) {
	productURL := "https://www.supercheapauto.com.au/p/century-century-hi-performance-car-battery-55d23l-mf/602508.html"
	client := fakeClient(map[string]string{
		"REDIRECT:https://www.supercheapauto.com.au/search?q=602508": productURL,
		productURL: productPage,
	})
	p := NewProviderWithClient(client)
	d, err := p.GetDetails(context.Background(), "602508")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if d.ProviderID != "602508" {
		t.Errorf("expected item 602508, got %q", d.ProviderID)
	}
	if d.Name != "Century Hi Performance Car Battery 55D23L MF" {
		t.Errorf("unexpected name %q", d.Name)
	}
	if d.Manufacturer != "Century" {
		t.Errorf("unexpected brand %q", d.Manufacturer)
	}
	if len(d.VendorInfos) == 0 || d.VendorInfos[0].Price != "234.99" {
		t.Errorf("expected price 234.99, got %+v", d.VendorInfos)
	}
	foundVoltage := false
	for _, p := range d.Parameters {
		if p.Name == "Voltage" && p.ValueText == "12V" {
			foundVoltage = true
		}
	}
	if !foundVoltage {
		t.Errorf("expected Voltage spec, got %+v", d.Parameters)
	}
	if !strings.Contains(d.Description, "battery") {
		t.Errorf("expected description from JSON-LD, got %q", d.Description)
	}
}
