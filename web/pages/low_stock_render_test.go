package pages

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
)

func TestLowStockRenders(t *testing.T) {
	parts := []LowStockPartView{
		{
			ID:                1,
			Name:              "Resistor 10K",
			PartNumber:        sql.NullString{String: "R-10K", Valid: true},
			Supplier:          sql.NullString{String: "DigiKey", Valid: true},
			TotalStock:        3,
			ReorderLevel:      10,
			MinStockThreshold: 5,
			SuggestedOrder:    7,
		},
		{
			ID:             2,
			Name:           "Capacitor 100uF",
			TotalStock:     0,
			ReorderLevel:   0,
			SuggestedOrder: 1,
		},
	}

	var buf bytes.Buffer
	if err := LowStock(auth.User{}, parts).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Resistor 10K") {
		t.Errorf("expected part name in output")
	}
	// The critical part (stock <= min threshold) should be flagged critical.
	if !strings.Contains(out, "Critical") {
		t.Errorf("expected a Critical badge in output")
	}
	// The Find Supplier deep-link should carry the part number.
	if !strings.Contains(out, "/suppliers?q=R-10K") {
		t.Errorf("expected supplier deep-link with part number")
	}
}

func TestLowStockRendersEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := LowStock(auth.User{}, nil).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected non-empty output for empty state")
	}
}
