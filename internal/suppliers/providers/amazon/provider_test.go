package amazon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const fakeHelper = `#!/usr/bin/env python3
import json, sys

command = sys.argv[1]
arg = sys.argv[2]

if command == "search":
    if arg == "zzzz-invalid":
        print(json.dumps({"ok": True, "results": []}))
    elif arg == "captcha-trigger":
        print(json.dumps({"ok": False, "error": "amazon captcha detected"}))
    else:
        print(json.dumps({"ok": True, "results": [
            {"asin": "B0C9THDPXP", "title": "Freenove ESP32 Dev Board Kit",
             "url": "https://www.amazon.com.au/dp/B0C9THDPXP",
             "price": 25.95, "currency": "$",
             "img_url": "https://m.media-amazon.com/images/I/71Dt1KDT6wL.jpg",
             "rating": 4.6, "brand": "Freenove"},
            {"asin": "B0966LV5B7", "title": "ESP32 Development Board",
             "url": "https://www.amazon.com.au/dp/B0966LV5B7",
             "price": 36.89, "currency": "$",
             "img_url": "https://m.media-amazon.com/images/I/71hOussWbiS.jpg",
             "rating": 4.6}
        ]}))
elif command == "product":
    if arg == "CAPTCHAASIN":
        print(json.dumps({"ok": False, "error": "amazon captcha detected"}))
    else:
        print(json.dumps({"ok": True, "product": {
            "asin": arg, "title": "Freenove ESP32 Dev Board Kit",
            "url": "https://www.amazon.com.au/dp/%s" % arg,
            "price": 25.95, "currency": "AUD",
            "img_url": "https://m.media-amazon.com/images/I/71Dt1KDT6wL.jpg",
            "brand": "Freenove", "rating": 4.6, "review_count": 491,
            "bullets": ["Bullet one", "Bullet two"],
            "specs": [{"name": "Chipset", "value": "ESP32"}],
            "availability": "In stock", "seller": "Freenove-AU"}}))
`

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	dir := t.TempDir()
	helper := filepath.Join(dir, "fake_helper.py")
	if err := os.WriteFile(helper, []byte(fakeHelper), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	return NewProviderWithPaths(helper, "python3")
}

func TestProviderInfo(t *testing.T) {
	p := NewProvider()
	info := p.GetProviderInfo()
	if info.Key != "amazon" {
		t.Errorf("key = %q, want amazon", info.Key)
	}
	if !p.IsActive() {
		t.Error("provider should be active")
	}
	if !p.HandlesDomain("www.amazon.com.au") || p.HandlesDomain("amazon.com") {
		t.Error("HandlesDomain returned wrong result for amazon.com.au")
	}
}

func TestSearchByKeyword(t *testing.T) {
	p := newTestProvider(t)
	results, err := p.SearchByKeyword(context.Background(), "esp32 board")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	r := results[0]
	if r.ProviderKey != "amazon" || r.ProviderID != "B0C9THDPXP" || r.Name == "" {
		t.Errorf("unexpected result: %+v", r)
	}
	if r.ProviderURL != "https://www.amazon.com.au/dp/B0C9THDPXP" {
		t.Errorf("url = %q", r.ProviderURL)
	}
	if r.Manufacturer != "Freenove" {
		t.Errorf("manufacturer = %q, want Freenove", r.Manufacturer)
	}
}

func TestSearchByASIN(t *testing.T) {
	p := newTestProvider(t)
	results, err := p.SearchByKeyword(context.Background(), "B0C9THDPXP")
	if err != nil {
		t.Fatalf("search by asin failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].ProviderID != "B0C9THDPXP" {
		t.Errorf("provider id = %q", results[0].ProviderID)
	}
}

func TestSearchInvalidASINReturnsEmpty(t *testing.T) {
	p := newTestProvider(t)
	results, err := p.SearchByKeyword(context.Background(), "zzzz-invalid")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSearchEmptyKeyword(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.SearchByKeyword(context.Background(), "   "); err == nil {
		t.Error("expected error for empty keyword")
	}
}

func TestSearchCaptcha(t *testing.T) {
	p := newTestProvider(t)
	_, err := p.SearchByKeyword(context.Background(), "captcha-trigger")
	if err == nil {
		t.Fatal("expected captcha error")
	}
}

func TestGetDetails(t *testing.T) {
	p := newTestProvider(t)
	d, err := p.GetDetails(context.Background(), "B0C9THDPXP")
	if err != nil {
		t.Fatalf("details failed: %v", err)
	}
	if d.Name == "" || d.ProviderID != "B0C9THDPXP" {
		t.Errorf("bad detail: %+v", d.SearchResultDTO)
	}
	if len(d.VendorInfos) != 1 {
		t.Fatalf("got %d vendor infos, want 1", len(d.VendorInfos))
	}
	vi := d.VendorInfos[0]
	if vi.Price != "25.95" || vi.Currency != "AUD" || vi.InStock != true {
		t.Errorf("bad vendor info: %+v", vi)
	}
	if len(d.Images) != 1 {
		t.Errorf("got %d images, want 1", len(d.Images))
	}
	if len(d.Parameters) != 1 || d.Parameters[0].Name != "Chipset" {
		t.Errorf("bad parameters: %+v", d.Parameters)
	}
	if d.Notes != "Bullet one\nBullet two" {
		t.Errorf("notes = %q", d.Notes)
	}
}

func TestGetDetailsInvalidASIN(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.GetDetails(context.Background(), "not-an-asin"); err == nil {
		t.Error("expected error for invalid asin")
	}
}

func TestGetDetailsCaptcha(t *testing.T) {
	p := newTestProvider(t)
	if _, err := p.GetDetails(context.Background(), "CAPTCHAASIN"); err == nil {
		t.Error("expected captcha error")
	}
}

func TestSearchCache(t *testing.T) {
	p := newTestProvider(t)
	_, err := p.SearchByKeyword(context.Background(), "esp32 board")
	if err != nil {
		t.Fatalf("first search failed: %v", err)
	}
	// Second call should hit cache. To verify, break the helper and confirm
	// the cached result still comes back.
	if err := os.Chmod(filepath.Join(filepath.Dir(p.helperPath), "fake_helper.py"), 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	results, err := p.SearchByKeyword(context.Background(), "esp32 board")
	if err != nil {
		t.Fatalf("cached search failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d cached results, want 2", len(results))
	}
}

func TestFormatPrice(t *testing.T) {
	cases := []struct{ in float64; want string }{
		{25.95, "25.95"},
		{12.0, "12"},
		{0, ""},
		{19.99, "19.99"},
	}
	for _, c := range cases {
		if got := formatPrice(c.in); got != c.want {
			t.Errorf("formatPrice(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHelperJSONContract(t *testing.T) {
	var resp helperResponse
	if err := json.Unmarshal([]byte(`{"ok":true,"results":[{"asin":"B0C9THDPXP","title":"X","url":"u","price":1.5,"currency":"$","img_url":"i","rating":4,"brand":"B"}]}`), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].ASIN != "B0C9THDPXP" {
		t.Errorf("bad results parse: %+v", resp.Results)
	}
	var prod helperResponse
	if err := json.Unmarshal([]byte(`{"ok":true,"product":{"asin":"B0C9THDPXP","title":"X","url":"u","price":25.95,"currency":"AUD","img_url":"i","brand":"B","rating":4.6,"review_count":491,"bullets":["b"],"specs":[{"name":"n","value":"v"}],"availability":"In stock","seller":"s"}}`), &prod); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if prod.Product.ASIN != "B0C9THDPXP" || len(prod.Product.Specs) != 1 {
		t.Errorf("bad product parse: %+v", prod.Product)
	}
}

func TestExtractPartIDFromURL(t *testing.T) {
	p := newTestProvider(t)
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"https://www.amazon.com.au/dp/B0C9THDPXP", "B0C9THDPXP", true},
		{"https://www.amazon.com.au/dp/B0C9THDPXP/ref=xx", "B0C9THDPXP", true},
		{"https://www.amazon.com.au/gp/product/B0966LV5B7", "B0966LV5B7", true},
		{"https://www.amazon.com.au/gp/aw/d/B0DNSMVXRM", "B0DNSMVXRM", true},
		{"https://www.amazon.com.au/aw/d/B0CS6YK82K", "B0CS6YK82K", true},
		{"https://www.amazon.com.au/s?k=esp32&asin=B0C9THDPXP", "B0C9THDPXP", true},
		{"https://www.amazon.com.au/s?k=esp32+development+board", "", false},
		{"https://www.amazon.com.au/stores/page/ABCDEF1234", "", false},
		{"not an amazon url", "", false},
	}
	for _, c := range cases {
		got, ok := p.ExtractPartIDFromURL(c.url)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("ExtractPartIDFromURL(%q) = (%q, %v), want (%q, %v)", c.url, got, ok, c.want, c.ok)
		}
	}
}

func TestCacheTTLProvider(t *testing.T) {
	p := newTestProvider(t)
	if got := p.SearchCacheTTL(); got != searchCacheTTL {
		t.Errorf("SearchCacheTTL = %v, want %v", got, searchCacheTTL)
	}
	if got := p.DetailCacheTTL(); got != detailCacheTTL {
		t.Errorf("DetailCacheTTL = %v, want %v", got, detailCacheTTL)
	}
}

func TestCleanAmazonBrand(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"by Sydney Dumore (Author), Hannah Kelly (Illustrator) Format: Paperback", ""},
		{"Visit the SAMSUNG Store", "Visit the SAMSUNG Store"},
		{"Samsung", "Samsung"},
		{"visit the SONY store", "visit the SONY store"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := cleanAmazonBrand(c.in); got != c.want {
			t.Errorf("cleanAmazonBrand(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
