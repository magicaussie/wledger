package productdetails

import (
	"database/sql"
	"errors"
	"testing"
)

func TestExtendedDetailsUnitsAndValidation(t *testing.T) {
	details := ExtendedDetails{
		SKU: "  SKU-1  ", Brand: " Acme ", LengthMM: sql.NullFloat64{Float64: 25.4, Valid: true},
		WeightG: sql.NullFloat64{Float64: 0, Valid: true}, PurchaseDate: sql.NullString{String: "2026-03-10", Valid: true},
		PurchasePrice: sql.NullFloat64{Float64: 1.25, Valid: true}, ProductURL: "https://example.com/item",
	}
	if err := details.NormalizeAndValidate(); err != nil {
		t.Fatal(err)
	}
	if details.SKU != "SKU-1" || details.Brand != "Acme" || details.Currency != "AUD" || details.DimensionUnit != "mm" || details.WeightUnit != "g" {
		t.Fatalf("normalized details = %+v", details)
	}

	invalidCases := []ExtendedDetails{
		{LengthMM: sql.NullFloat64{Float64: 0, Valid: true}},
		{WeightG: sql.NullFloat64{Float64: -0.1, Valid: true}},
		{PurchasePrice: sql.NullFloat64{Float64: -1, Valid: true}},
		{PurchaseDate: sql.NullString{String: "10/03/2026", Valid: true}},
		{Currency: "US"},
		{DimensionUnit: "yd"},
		{WeightUnit: "stone"},
		{ProductURL: "javascript:alert(1)"},
	}
	for i, candidate := range invalidCases {
		if err := candidate.NormalizeAndValidate(); !errors.Is(err, ErrInvalidDetails) {
			t.Errorf("invalid case %d error=%v, want ErrInvalidDetails", i, err)
		}
	}
}
