package littlebird

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
	if strings.Contains(req.URL.String(), "/search") {
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
	results, err := newTestProvider().SearchByKeyword(context.Background(), "filament")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) != 20 {
		t.Fatalf("expected 20 results, got %d", len(results))
	}
	var pink *struct {
		ProviderID string
		Name       string
	}
	for _, r := range results {
		if r.ProviderID == "LB-FIL-0154" {
			rr := r
			pink = &struct {
				ProviderID string
				Name       string
			}{rr.ProviderID, rr.Name}
			break
		}
	}
	if pink == nil {
		t.Fatal("expected LB-FIL-0154 in results")
	}
	if !strings.Contains(pink.Name, "Silk-Like PLA") {
		t.Errorf("unexpected name %q", pink.Name)
	}
}

func TestGetDetails(t *testing.T) {
	detail, err := newTestProvider().GetDetails(context.Background(), "LB-FIL-0154")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if detail.ProviderID != "LB-FIL-0154" {
		t.Errorf("expected ProviderID LB-FIL-0154, got %q", detail.ProviderID)
	}
	if !strings.Contains(detail.Name, "Silk-Like PLA Filament") {
		t.Errorf("unexpected name %q", detail.Name)
	}
	if len(detail.VendorInfos) == 0 {
		t.Fatal("expected vendor info")
	}
	vi := detail.VendorInfos[0]
	if vi.Currency != "AUD" {
		t.Errorf("expected AUD currency, got %q", vi.Currency)
	}
	if vi.Price == "" || vi.Price == "0.00" {
		t.Errorf("unexpected price %q", vi.Price)
	}
	if !vi.InStock {
		t.Errorf("expected product in stock")
	}
	if len(detail.Images) == 0 {
		t.Errorf("expected at least one image")
	}
	if detail.Manufacturer == "" {
		t.Errorf("expected manufacturer")
	}
}

func TestExtractPartIDFromURL(t *testing.T) {
	p := newTestProvider()
	handle, ok := p.ExtractPartIDFromURL("https://littlebird.com.au/products/silk-like-pla-filament-1-75mm-1kg-roll-pink")
	if !ok || handle != "silk-like-pla-filament-1-75mm-1kg-roll-pink" {
		t.Errorf("ExtractPartIDFromURL = %q, %v", handle, ok)
	}
	if _, ok := p.ExtractPartIDFromURL("https://littlebird.com.au/collections/all"); ok {
		t.Errorf("expected no match for collection URL")
	}
}
