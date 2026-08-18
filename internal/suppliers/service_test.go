package suppliers

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/config"
)

func TestFirstUnitCost(t *testing.T) {
	// Prefer the first usable price from a price-break list.
	d := &PartDetailDTO{
		VendorInfos: []PurchaseInfoDTO{
			{
				DistributorName: "RS Components Australia",
				Price:           "89.16",
				Prices: []PriceDTO{
					{MinQuantity: 1, Price: "89.16", Currency: "AUD", IncludesTax: false},
					{MinQuantity: 1, Price: "98.08", Currency: "AUD", IncludesTax: true},
				},
			},
		},
	}
	cost, ok := firstUnitCost(d)
	if !ok {
		t.Fatal("expected a unit cost")
	}
	if cost != 89.16 {
		t.Errorf("expected 89.16, got %f", cost)
	}

	// Fall back to the free-form price string.
	d2 := &PartDetailDTO{
		VendorInfos: []PurchaseInfoDTO{
			{DistributorName: "Mouser", Price: "$12.50"},
		},
	}
	cost2, ok2 := firstUnitCost(d2)
	if !ok2 || cost2 != 12.50 {
		t.Errorf("expected 12.50 from free-form price, got %f ok=%v", cost2, ok2)
	}

	// No prices at all -> not ok.
	d3 := &PartDetailDTO{}
	if _, ok3 := firstUnitCost(d3); ok3 {
		t.Error("expected no unit cost for empty vendor info")
	}
}

func TestParsePriceString(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"$89.16", 89.16, true},
		{"89.16", 89.16, true},
		{"$ 12.50", 12.50, true},
		{"", 0, false},
		{"not-a-price", 0, false},
	}
	for _, c := range cases {
		got, err := parsePriceString(c.in)
		if c.ok && err != nil {
			t.Errorf("parsePriceString(%q) unexpected error: %v", c.in, err)
			continue
		}
		if !c.ok && err == nil {
			t.Errorf("parsePriceString(%q) expected error, got %f", c.in, got)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("parsePriceString(%q) = %f, want %f", c.in, got, c.want)
		}
	}
}

// TestDownloadPartImage verifies a supplier image URL is fetched and stored
// locally as an uploads web path.
func TestDownloadPartImage(t *testing.T) {
	ensureImageDir(t)
	// Create a tiny PNG to serve.
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})

	var servedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		servedURL = r.URL.Path
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, img)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := &service{
		logger:     logger,
		httpClient: srv.Client(),
	}

	detail := &PartDetailDTO{
		SearchResultDTO: SearchResultDTO{
			PreviewImageURL: srv.URL + "/prod.png",
		},
	}

	path := s.downloadPartImage(context.Background(), detail)
	if path == "" {
		t.Fatal("expected a saved image web path")
	}
	if len(path) <= len("/uploads/images/") {
		t.Errorf("unexpected short path %q", path)
	}
	if servedURL != "/prod.png" {
		t.Errorf("expected request to /prod.png, got %q", servedURL)
	}
}

func TestDownloadPartImageFallbackToImages(t *testing.T) {
	ensureImageDir(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, img)
	}))
	defer srv.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s := &service{logger: logger, httpClient: srv.Client()}

	// No PreviewImageURL; should fall back to the first http image.
	detail := &PartDetailDTO{
		Images: []FileDTO{{URL: "not-http"}, {URL: srv.URL + "/alt.png"}},
	}
	path := s.downloadPartImage(context.Background(), detail)
	if path == "" {
		t.Fatal("expected fallback image to be saved")
	}
}

func ensureImageDir(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(config.DirUploadsImages, 0o755); err != nil {
		t.Fatalf("failed to create uploads image dir: %v", err)
	}
}
