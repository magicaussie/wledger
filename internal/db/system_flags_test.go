package db_test

import (
	"context"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestSystemFlags(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Test SetFlag
	err := q.SetFlag(ctx, db.SetFlagParams{
		Key:   "test_flag",
		Value: "true",
	})
	if err != nil {
		t.Fatalf("failed to set flag: %v", err)
	}

	// Test GetFlag
	val, err := q.GetFlag(ctx, "test_flag")
	if err != nil {
		t.Fatalf("failed to get flag: %v", err)
	}

	if val != "true" {
		t.Errorf("expected flag value 'true', got '%s'", val)
	}

	// Test Get non-existent flag
	_, err = q.GetFlag(ctx, "non_existent")
	if err == nil {
		t.Fatal("expected error for non-existent flag")
	}
}
