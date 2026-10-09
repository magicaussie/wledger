package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestListLowStockParts(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// mkPart creates a part with the given thresholds and (optionally) stock.
	mkPart := func(name string, reorder, minStock, stock int64) int64 {
		id, err := q.CreatePart(ctx, db.CreatePartParams{
			Name:              name,
			ReorderLevel:      sql.NullInt64{Int64: reorder, Valid: true},
			MinStockThreshold: sql.NullInt64{Int64: minStock, Valid: true},
		})
		if err != nil {
			t.Fatalf("create part %s: %v", name, err)
		}
		if stock > 0 {
			if err := q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
				PartID:   id,
				BinID:    sql.NullInt64{Valid: false},
				Quantity: stock,
			}); err != nil {
				t.Fatalf("assign stock for %s: %v", name, err)
			}
		}
		return id
	}

	p1 := mkPart("Critical", 10, 5, 3)   // 3 <= 5  -> critical
	p2 := mkPart("Low", 10, 5, 8)        // 8 <= 10 -> low
	p3 := mkPart("OK", 10, 5, 15)        // above reorder -> excluded
	p6 := mkPart("AtReorder", 20, 0, 20) // 20 <= 20 -> low
	p7 := mkPart("MinOnly", 0, 5, 2)     // 2 <= 5  -> critical via min threshold

	// Parts with no configured threshold must be excluded.
	if _, err := q.CreatePart(ctx, db.CreatePartParams{Name: "NoThreshold"}); err != nil {
		t.Fatalf("create no-threshold part: %v", err)
	}
	if _, err := q.CreatePart(ctx, db.CreatePartParams{
		Name:              "ZeroThreshold",
		ReorderLevel:      sql.NullInt64{Int64: 0, Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: 0, Valid: true},
	}); err != nil {
		t.Fatalf("create zero-threshold part: %v", err)
	}

	rows, err := q.ListLowStockParts(ctx)
	if err != nil {
		t.Fatalf("ListLowStockParts failed: %v", err)
	}

	got := make(map[int64]bool, len(rows))
	for _, r := range rows {
		got[r.ID] = true
	}

	want := []int64{p1, p2, p6, p7}
	if len(rows) != len(want) {
		t.Fatalf("expected %d low stock parts, got %d: %+v", len(want), len(rows), rows)
	}
	for _, id := range want {
		if !got[id] {
			t.Errorf("expected part %d in low stock results", id)
		}
	}
	if got[p3] {
		t.Errorf("part %d is above its reorder level and should be excluded", p3)
	}

	// Ordered by largest shortfall first: p1 (7), p2 (2), p6 (0), p7 (-2).
	order := []int64{p1, p2, p6, p7}
	for i, id := range order {
		if rows[i].ID != id {
			t.Errorf("expected row %d to be part %d, got %d", i, id, rows[i].ID)
		}
	}
}
