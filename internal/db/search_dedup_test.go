package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// TestSearchPartsReturnsEachMatchOnce verifies that a part matching both
// full-text search and extended details appears exactly once.
//
// The previous query unioned (rowid, rank) pairs and grouped the outer query by
// both id and rank, so a part carrying an FTS rank and a details rank of 0.0 was
// returned as two independent rows, consuming extra pagination slots.
func TestSearchPartsReturnsEachMatchOnce(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	part, err := q.CreatePart(ctx, db.CreatePartParams{Name: "Widget Resistor"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if err := q.CreatePartDetails(ctx, db.CreatePartDetailsParams{
		PartID:        part,
		Notes:         "Widget resistor extended notes",
		DimensionUnit: "mm",
		WeightUnit:    "g",
		Currency:      "AUD",
	}); err != nil {
		t.Fatalf("create details: %v", err)
	}

	rows, err := q.SearchParts(ctx, db.SearchPartsParams{
		BinID:  nil,
		Offset: 0,
		Limit:  50,
		Query:  sql.NullString{String: "Widget", Valid: true},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	count := 0
	for _, r := range rows {
		if r.ID == part {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("part matched %d times, want exactly 1 (rows=%d)", count, len(rows))
	}
}
