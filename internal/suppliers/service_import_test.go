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

// fakeImportProvider returns controlled details for a fixed part ID.
type fakeImportProvider struct {
	imageServer *httptest.Server
}

func (f *fakeImportProvider) GetProviderInfo() ProviderInfo {
	return ProviderInfo{Key: "fake-import", Name: "Fake Import", SupportsAuth: false}
}
func (f *fakeImportProvider) IsActive() bool { return true }
func (f *fakeImportProvider) GetCapabilities() []Capability {
	return []Capability{CapBasic, CapPicture, CapPrice}
}
func (f *fakeImportProvider) SearchByKeyword(ctx context.Context, keyword string) ([]SearchResultDTO, error) {
	return nil, nil
}
func (f *fakeImportProvider) GetDetails(ctx context.Context, providerID string) (*PartDetailDTO, error) {
	return &PartDetailDTO{
		SearchResultDTO: SearchResultDTO{
			ProviderID:      providerID,
			Name:            "Schneider LC1D Contactor",
			Description:     "TeSys Deca contactor with a detailed description.",
			Manufacturer:    "Schneider Electric",
			MPN:             "LC1D09U7",
			ProviderURL:     "https://au.rs-online.com/web/p/contactors/0187920",
			PreviewImageURL: f.imageServer.URL + "/prod.png",
		},
		VendorInfos: []PurchaseInfoDTO{
			{
				DistributorName: "RS Components Australia",
				ProductURL:      "https://au.rs-online.com/web/p/contactors/0187920",
				Price:           "89.16",
				Currency:        "AUD",
				Prices: []PriceDTO{
					{MinQuantity: 1, Price: "89.16", Currency: "AUD", IncludesTax: false},
				},
			},
		},
	}, nil
}

// TestImportFromProvider_PopulatesFields verifies that importing a supplier
// part sets name, description, manufacturer, unit_cost and image on the part.
func TestImportFromProvider_PopulatesFields(t *testing.T) {
	ensureImageDir(t)

	// A fake provider serving a real PNG.
	fake := &fakeImportProvider{}
	fake.imageServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		img.Set(0, 0, color.RGBA{R: 255, A: 255})
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, img)
	}))
	defer fake.imageServer.Close()

	Register(fake)

	conn, err := db.Open("file:supplier_import_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(conn)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cache := NewCache(store, logger)
	svc := NewService(store, cache, logger)

	ctx := context.Background()
	id, err := svc.ImportFromProvider(ctx, ImportRequest{ProviderKey: "fake-import", ProviderID: "LC1D09U7"})
	if err != nil {
		t.Fatalf("ImportFromProvider failed: %v", err)
	}

	part, err := store.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}

	if part.Name != "Schneider LC1D Contactor" {
		t.Errorf("unexpected name %q", part.Name)
	}
	if !part.Description.Valid || part.Description.String != "TeSys Deca contactor with a detailed description." {
		t.Errorf("expected description to be set, got %+v", part.Description)
	}
	if !part.Manufacturer.Valid || part.Manufacturer.String != "Schneider Electric" {
		t.Errorf("expected manufacturer, got %+v", part.Manufacturer)
	}
	if !part.PartNumber.Valid || part.PartNumber.String != "LC1D09U7" {
		t.Errorf("expected MPN to be set, got %+v", part.PartNumber)
	}
	if !part.UnitCost.Valid || part.UnitCost.Float64 != 89.16 {
		t.Errorf("expected unit cost 89.16, got %+v", part.UnitCost)
	}
	if !part.ImagePath.Valid || part.ImagePath.String == "" {
		t.Errorf("expected image path to be set, got %+v", part.ImagePath)
	} else if len(part.ImagePath.String) <= len("/uploads/images/") {
		t.Errorf("unexpected image path %q", part.ImagePath.String)
	}

	// Pricing row should also exist.
	pricing, err := store.GetPartPricing(ctx, id)
	if err != nil {
		t.Fatalf("GetPartPricing failed: %v", err)
	}
	if len(pricing) == 0 {
		t.Fatal("expected part pricing to be stored")
	}
	if pricing[0].Price != 89.16 {
		t.Errorf("expected stored price 89.16, got %f", pricing[0].Price)
	}
}