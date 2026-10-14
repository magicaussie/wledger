package parts

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/productdetails"
	"github.com/tuxedocurly/wledger/internal/tags"
)

func TestExtendedDetailsValidationPersistenceAndSearch(t *testing.T) {
	conn, store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	partID, err := store.CreatePart(ctx, db.CreatePartParams{Name: "Generic fastener"})
	if err != nil {
		t.Fatal(err)
	}
	details := productdetails.ExtendedDetails{
		PartID: partID, SKU: "FASTENER-008", Brand: "WorkshopBrand", Model: "M8x35",
		Colour: "Silver", Material: "Stainless steel", Finish: "Passivated", Condition: "New",
		LengthMM: sql.NullFloat64{Float64: 35, Valid: true}, DimensionUnit: "cm", WeightG: sql.NullFloat64{Float64: 12.5, Valid: true}, WeightUnit: "kg",
		PurchasedFrom: "Local supplier", SupplierProductCode: "SUP-008", PurchaseDate: sql.NullString{String: "2026-01-03", Valid: true},
		PurchasePrice: sql.NullFloat64{Float64: 0.42, Valid: true}, Currency: "AUD", ProductURL: "https://example.com/fastener", PurchaseNotes: "Invoice A-8",
	}
	if err := productdetails.SaveExtendedDetails(ctx, conn, details); err != nil {
		t.Fatalf("save details: %v", err)
	}
	got, err := productdetails.GetExtendedDetails(ctx, conn, partID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SKU != details.SKU || got.Brand != details.Brand || !got.LengthMM.Valid || got.LengthMM.Float64 != 35 || got.DimensionUnit != "cm" || got.WeightUnit != "kg" || !got.PurchasePrice.Valid || got.PurchasePrice.Float64 != 0.42 || got.Currency != "AUD" {
		t.Fatalf("loaded extended details = %+v", got)
	}
	if err := productdetails.ValidateExtendedDetails(ctx, conn, productdetails.ExtendedDetails{SKU: "fastener-008"}, 0); !errors.Is(err, productdetails.ErrDuplicateSKU) {
		t.Fatalf("duplicate SKU validation = %v, want ErrDuplicateSKU", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tagService := tags.NewService(conn, store)
	docService := documents.NewService(store, logger)
	service := NewService(conn, store, logger, tagService, docService)
	results, err := service.ListParts(ctx, "WorkshopBrand", 1, nil)
	if err != nil {
		t.Fatalf("search by extended product field: %v", err)
	}
	if len(results) != 1 || results[0].ID != partID {
		t.Fatalf("extended-field search results = %+v", results)
	}

	invalid := details
	invalid.LengthMM = sql.NullFloat64{Float64: 0, Valid: true}
	if err := productdetails.ValidateExtendedDetails(ctx, conn, invalid, partID); !errors.Is(err, productdetails.ErrInvalidDetails) {
		t.Fatalf("zero dimension validation = %v, want ErrInvalidDetails", err)
	}
	invalid = details
	invalid.ProductURL = "javascript:alert(1)"
	if err := productdetails.ValidateExtendedDetails(ctx, conn, invalid, partID); !errors.Is(err, productdetails.ErrInvalidDetails) {
		t.Fatalf("unsafe product URL validation = %v, want ErrInvalidDetails", err)
	}
}
