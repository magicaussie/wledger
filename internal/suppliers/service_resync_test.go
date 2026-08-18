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

	"github.com/tuxedocurly/wledger/internal/db"
)

// fakeResyncProvider returns two different detail payloads depending on the
// "id": an empty-ish one first, then a rich one, so tests can prove resync
// bypasses the cache.
type fakeResyncProvider struct {
	imageServer *httptest.Server
	call        int
}

func (f *fakeResyncProvider) GetProviderInfo() ProviderInfo {
	return ProviderInfo{Key: "fake-resync", Name: "Fake Resync", SupportsAuth: false}
}
func (f *fakeResyncProvider) IsActive() bool { return true }
func (f *fakeResyncProvider) GetCapabilities() []Capability {
	return []Capability{CapBasic, CapPicture, CapPrice}
}
func (f *fakeResyncProvider) SearchByKeyword(ctx context.Context, keyword string) ([]SearchResultDTO, error) {
	return nil, nil
}
func (f *fakeResyncProvider) GetDetails(ctx context.Context, providerID string) (*PartDetailDTO, error) {
	f.call++
	// Return poor data on the first call (simulates a pre-fix provider), rich
	// data on subsequent calls so resync has something to apply.
	if f.call == 1 {
		return &PartDetailDTO{
			SearchResultDTO: SearchResultDTO{ProviderID: providerID, Name: "Widget"},
		}, nil
	}
	return &PartDetailDTO{
		SearchResultDTO: SearchResultDTO{
			ProviderID:      providerID,
			Name:            "Widget",
			Description:     "A rich description.",
			Manufacturer:    "ACME",
			MPN:             "WDG-1",
			PreviewImageURL: f.imageServer.URL + "/p.png",
		},
		VendorInfos: []PurchaseInfoDTO{
			{
				DistributorName: "Fake Resync",
				Price:           "12.50",
				Currency:        "AUD",
				Prices:          []PriceDTO{{MinQuantity: 1, Price: "12.50", Currency: "AUD"}},
			},
		},
	}, nil
}

// TestResyncFromProvider verifies a part imported with sparse data gets its
// missing fields backfilled, even when a stale cached detail exists.
func TestResyncFromProvider(t *testing.T) {
	ensureImageDir(t)

	fake := &fakeResyncProvider{}
	fake.imageServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, img)
	}))
	defer fake.imageServer.Close()

	Register(fake)

	conn, err := db.Open("file:resync_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(conn)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	svc := NewService(store, NewCache(store, logger), logger).(*service)

	ctx := context.Background()
	id, err := svc.ImportFromProvider(ctx, ImportRequest{ProviderKey: "fake-resync", ProviderID: "WDG-1"})
	if err != nil {
		t.Fatalf("first import failed: %v", err)
	}

	// The first import populated sparse data (name only). Now resync.
	if err := svc.ResyncFromProvider(ctx, id); err != nil {
		t.Fatalf("ResyncFromProvider failed: %v", err)
	}

	part, err := store.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if !part.Description.Valid || part.Description.String != "A rich description." {
		t.Errorf("expected description to be backfilled, got %+v", part.Description)
	}
	if !part.Manufacturer.Valid || part.Manufacturer.String != "ACME" {
		t.Errorf("expected manufacturer to be backfilled, got %+v", part.Manufacturer)
	}
	if !part.PartNumber.Valid || part.PartNumber.String != "WDG-1" {
		t.Errorf("expected MPN to be backfilled, got %+v", part.PartNumber)
	}
	if !part.UnitCost.Valid || part.UnitCost.Float64 != 12.50 {
		t.Errorf("expected unit cost 12.50, got %+v", part.UnitCost)
	}
	if !part.ImagePath.Valid || part.ImagePath.String == "" {
		t.Errorf("expected image to be backfilled, got %+v", part.ImagePath)
	}
}