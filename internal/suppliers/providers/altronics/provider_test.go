package altronics

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/suppliers"
)

// fixtureTransport serves saved pages from testdata so tests never touch the
// network. Anything that looks like a search returns search.html; anything
// else returns product.html.
type fixtureTransport struct{}

func (fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var file string
	if strings.Contains(req.URL.String(), "/search") || strings.Contains(req.URL.RawQuery, "q=") {
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
	results, err := newTestProvider().SearchByKeyword(context.Background(), "resistor")
	if err != nil {
		t.Fatalf("SearchByKeyword: %v", err)
	}
	if len(results) < 10 {
		t.Fatalf("expected many results, got %d", len(results))
	}

	// The fixture is sorted with Z1621A (LDR) among the hits.
	var ldr *suppliers.SearchResultDTO
	for _, r := range results {
		if r.ProviderID == "Z1621A" {
			rr := r
			ldr = &rr
			break
		}
	}
	if ldr == nil {
		t.Fatalf("expected Z1621A in results")
	}
	if !strings.Contains(ldr.Name, "Light Dependent Resistor") {
		t.Errorf("unexpected name %q", ldr.Name)
	}
	if !strings.HasPrefix(ldr.ProviderURL, baseURL+"/product/") {
		t.Errorf("unexpected provider URL %q", ldr.ProviderURL)
	}
	if ldr.PreviewImageURL == "" {
		t.Errorf("expected a preview image URL")
	}
}

func TestGetDetails(t *testing.T) {
	detail, err := newTestProvider().GetDetails(context.Background(), "Z1621A")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if detail.ProviderID != "Z1621A" {
		t.Errorf("expected ProviderID Z1621A, got %q", detail.ProviderID)
	}
	if !strings.Contains(detail.Name, "Light Dependent Resistor") {
		t.Errorf("unexpected name %q", detail.Name)
	}
	if detail.Category != "Light Dependent Resistors" {
		t.Errorf("expected category 'Light Dependent Resistors', got %q", detail.Category)
	}
	if len(detail.VendorInfos) == 0 {
		t.Fatal("expected vendor info")
	}
	vi := detail.VendorInfos[0]
	if vi.Currency != "AUD" || vi.Price != "2.90" {
		t.Errorf("unexpected price: %s %s", vi.Price, vi.Currency)
	}
	if len(detail.Parameters) < 3 {
		t.Errorf("expected spec parameters, got %d", len(detail.Parameters))
	}
	if len(detail.Images) == 0 {
		t.Errorf("expected at least one image")
	}
}

func TestExtractPartIDFromURL(t *testing.T) {
	p := newTestProvider()
	sku, ok := p.ExtractPartIDFromURL("https://www.altronics.com.au/product/z1621a-5k-10k-light-dependent-resistor-ldr")
	if !ok || sku != "Z1621A" {
		t.Errorf("ExtractPartIDFromURL = %q, %v", sku, ok)
	}
	if _, ok := p.ExtractPartIDFromURL("https://www.altronics.com.au/category/electronic-components"); ok {
		t.Errorf("expected no match for category URL")
	}
}
