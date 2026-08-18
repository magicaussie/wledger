package aliexpress

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeHelper writes a tiny shell script that echoes canned JSON based on
// the first arg (search/product), so we can test the Go provider without the
// real Node/Puppeteer stack.
func writeFakeHelper(t *testing.T, searchJSON, productJSON string) (nodePath, helperPath, workDir string) {
	t.Helper()
	dir := t.TempDir()
	helper := filepath.Join(dir, "fake_helper.sh")
	content := `#!/bin/sh
case "$1" in
  search) echo '` + searchJSON + `' ;;
  product) echo '` + productJSON + `' ;;
  *) echo '{"ok":false,"error":"unknown"}' ;;
esac
`
	if err := os.WriteFile(helper, []byte(content), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	return "sh", helper, dir
}

func TestExtractPartIDFromURL(t *testing.T) {
	p := NewProvider()
	cases := []struct {
		url  string
		want string
		ok   bool
	}{
		{"https://www.aliexpress.com/item/1005002935037572.html", "1005002935037572", true},
		{"https://aliexpress.com/item/1234567890", "1234567890", true},
		{"https://www.aliexpress.com/w/wholesale-battery.html", "", false},
		{"https://example.com/item/123", "", false},
	}
	for _, c := range cases {
		got, ok := p.ExtractPartIDFromURL(c.url)
		if got != c.want || ok != c.ok {
			t.Errorf("ExtractPartIDFromURL(%q) = (%q, %v), want (%q, %v)", c.url, got, ok, c.want, c.ok)
		}
	}
}

func TestSearchByKeyword(t *testing.T) {
	searchJSON := `{"ok":true,"results":[
		{"id":"1005002935037572","name":"10PCS FPC Connector","sale_price":2.91,"sale_price_text":"AU $2.91","currency":"AUD","image":"https://ae-pic-a1.aliexpress-media.com/kf/x.jpg","url":"https://www.aliexpress.com/item/1005002935037572.html"},
		{"id":"1005000389320217","name":"21V Battery","sale_price":1.41,"sale_price_text":"AU $1.41","currency":"AUD","image":"https://ae01.alicdn.com/kf/y.jpg","url":"https://www.aliexpress.com/item/1005000389320217.html"}
	]}`
	node, helper, dir := writeFakeHelper(t, searchJSON, "{}")
	p := NewProviderWithPaths(node, helper, dir)

	results, err := p.SearchByKeyword(context.Background(), "battery")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	r := results[0]
	if r.ProviderID != "1005002935037572" || r.Name != "10PCS FPC Connector" {
		t.Errorf("unexpected first result %+v", r)
	}
	if !strings.Contains(r.Description, "2.91") {
		t.Errorf("expected price in description, got %q", r.Description)
	}
}

func TestGetDetails(t *testing.T) {
	productJSON := `{"ok":true,"product":{
		"id":"1005002935037572",
		"title":"10PCS FPC Connector",
		"images":["https://ae-pic-a1.aliexpress-media.com/kf/a.jpg","https://ae-pic-a1.aliexpress-media.com/kf/b.jpg"],
		"sale_price":2.91,"original_price":3.51,"currency":"AUD",
		"rating":4.8,"rating_count":120,"store_name":"Lanrui Repair Store",
		"specs":[{"attrName":"Brand Name","attrValue":"LANRUISI"},{"attrName":"Model Number","attrValue":"A51"}],
		"url":"https://www.aliexpress.com/item/1005002935037572.html"
	}}`
	node, helper, dir := writeFakeHelper(t, "{}", productJSON)
	p := NewProviderWithPaths(node, helper, dir)

	d, err := p.GetDetails(context.Background(), "1005002935037572")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if d.ProviderID != "1005002935037572" || d.Name != "10PCS FPC Connector" {
		t.Errorf("unexpected detail %+v", d)
	}
	if len(d.VendorInfos) == 0 || d.VendorInfos[0].Price != "2.91" {
		t.Errorf("expected price 2.91, got %+v", d.VendorInfos)
	}
	if len(d.Images) != 2 {
		t.Errorf("expected 2 images, got %d", len(d.Images))
	}
	// specs + was price + store present
	groups := map[string]bool{}
	for _, prm := range d.Parameters {
		groups[prm.Group] = true
	}
	for _, g := range []string{"Specifications", "Pricing", "General"} {
		if !groups[g] {
			t.Errorf("missing group %q", g)
		}
	}
}

func TestSearchBlocked(t *testing.T) {
	node, helper, dir := writeFakeHelper(t, `{"ok":false,"error":"aliexpress block/captcha page after retries"}`, "{}")
	p := NewProviderWithPaths(node, helper, dir)
	if _, err := p.SearchByKeyword(context.Background(), "battery"); err == nil {
		t.Fatal("expected block error")
	}
}
