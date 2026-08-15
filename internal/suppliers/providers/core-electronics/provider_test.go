package coreelectronics

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureTransport serves saved pages from testdata so tests never touch the
// network. Anything that looks like a search returns search.html; anything
// else returns product.html.
type fixtureTransport struct{}

func (fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var file string
	if strings.Contains(req.URL.String(), "/search/") {
		file = "search.html"
	} else {
		file = "product.html"
	}
	body, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: 200,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}, nil
}

func newTestProvider() *Provider {
	return NewProviderWithClient(&http.Client{Transport: fixtureTransport{}})
}

func TestSearchByKeyword(t *testing.T) {
	results, err := newTestProvider().SearchByKeyword(context.Background(), "arduino")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	for _, r := range results {
		if r.ProviderID == "" || !strings.Contains(r.Name, "Bambu") && r.Name == "" {
			t.Errorf("bad result: id=%q name=%q", r.ProviderID, r.Name)
		}
	}
	// The autocomplete suggestions from the fixture include the Bambu Lab AMS.
	found := false
	for _, r := range results {
		if strings.Contains(r.Name, "Bambu Lab") && r.ProviderID == "bambu-lab-h2c-ams-combo" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Bambu Lab AMS in results, got %d results", len(results))
	}
}

func TestGetDetails(t *testing.T) {
	detail, err := newTestProvider().GetDetails(context.Background(), "bambu-lab-h2c-ams-combo")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if detail.ProviderID != "BL-H2C-COMBO" {
		t.Errorf("expected ProviderID BL-H2C-COMBO, got %q", detail.ProviderID)
	}
	if !strings.Contains(detail.Name, "Bambu Lab") {
		t.Errorf("unexpected name %q", detail.Name)
	}
	if len(detail.VendorInfos) == 0 {
		t.Fatal("expected vendor info")
	}
	vi := detail.VendorInfos[0]
	if vi.Currency != "AUD" || vi.Price == "" {
		t.Errorf("unexpected price: %q %q", vi.Price, vi.Currency)
	}
	if !vi.InStock {
		t.Errorf("expected product in stock")
	}
	if len(detail.Parameters) == 0 {
		t.Errorf("expected spec parameters from description tables")
	}
	if detail.PreviewImageURL == "" {
		t.Errorf("expected a preview image URL")
	}
}

func TestExtractPartIDFromURL(t *testing.T) {
	p := newTestProvider()
	slug, ok := p.ExtractPartIDFromURL("https://core-electronics.com.au/bambu-lab-h2c-ams-combo.html")
	if !ok || slug != "bambu-lab-h2c-ams-combo" {
		t.Errorf("ExtractPartIDFromURL = %q, %v", slug, ok)
	}
	if _, ok := p.ExtractPartIDFromURL("https://core-electronics.com.au/search/?q=arduino"); ok {
		t.Errorf("expected no match for search URL")
	}
}
