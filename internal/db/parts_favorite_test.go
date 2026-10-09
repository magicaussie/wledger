package db_test

import (
	"context"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestTogglePartFavorite(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	id, err := q.CreatePart(ctx, db.CreatePartParams{Name: "Widget"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}

	// New parts default to not favorited.
	part, err := q.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("get part: %v", err)
	}
	if part.IsFavorite.Bool {
		t.Fatalf("expected new part to not be a favorite")
	}

	// First toggle turns it on.
	fav, err := q.TogglePartFavorite(ctx, id)
	if err != nil {
		t.Fatalf("toggle on: %v", err)
	}
	if !fav.Bool {
		t.Errorf("expected favorite true after first toggle")
	}

	// Second toggle turns it back off.
	fav, err = q.TogglePartFavorite(ctx, id)
	if err != nil {
		t.Fatalf("toggle off: %v", err)
	}
	if fav.Bool {
		t.Errorf("expected favorite false after second toggle")
	}

	// Persisted state should match.
	part, err = q.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("get part: %v", err)
	}
	if part.IsFavorite.Bool {
		t.Errorf("expected persisted favorite to be false")
	}
}
