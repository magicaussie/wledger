package ledspace_test

import (
	"context"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

func setupLedspaceDB(t *testing.T) (db.Store, func()) {
	t.Helper()
	conn, err := db.Open("file:" + t.Name() + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db.NewStore(conn), func() { conn.Close() }
}

// TestCurrent_DefaultIsSegment verifies that an absent flag is treated as the
// historical segment-relative default.
func TestCurrent_DefaultIsSegment(t *testing.T) {
	store, cleanup := setupLedspaceDB(t)
	defer cleanup()
	ctx := context.Background()

	space, err := ledspace.Current(ctx, store)
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if space != ledspace.Segment {
		t.Fatalf("space = %q, want %q", space, ledspace.Segment)
	}
}

// TestCurrent_KnownSpaces verifies that every known persisted value round-trips.
func TestCurrent_KnownSpaces(t *testing.T) {
	for _, want := range []string{ledspace.Segment, ledspace.Drawer, ledspace.Unresolved} {
		t.Run(want, func(t *testing.T) {
			store, cleanup := setupLedspaceDB(t)
			defer cleanup()
			ctx := context.Background()

			if err := ledspace.Set(ctx, store, want); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := ledspace.Current(ctx, store)
			if err != nil {
				t.Fatalf("Current: %v", err)
			}
			if got != want {
				t.Fatalf("space = %q, want %q", got, want)
			}
		})
	}
}

// TestCurrent_RejectsUnknownValue verifies that an unrecognised persisted value
// is rejected rather than silently treated as segment.
func TestCurrent_RejectsUnknownValue(t *testing.T) {
	store, cleanup := setupLedspaceDB(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.SetFlag(ctx, db.SetFlagParams{Key: ledspace.FlagKey, Value: "bogus"}); err != nil {
		t.Fatalf("SetFlag: %v", err)
	}

	if _, err := ledspace.Current(ctx, store); err == nil {
		t.Fatal("expected an error for an unknown persisted coordinate space")
	}
}

// TestIsUnresolved_RejectsUnknownValue verifies that the unresolved check does
// not silently treat an unknown value as resolved.
func TestIsUnresolved_RejectsUnknownValue(t *testing.T) {
	store, cleanup := setupLedspaceDB(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.SetFlag(ctx, db.SetFlagParams{Key: ledspace.FlagKey, Value: "bogus"}); err != nil {
		t.Fatalf("SetFlag: %v", err)
	}

	if _, err := ledspace.IsUnresolved(ctx, store); err == nil {
		t.Fatal("expected an error for an unknown persisted coordinate space")
	}
}

// TestIsUnresolved_KnownStates verifies the resolved/unresolved classification.
func TestIsUnresolved_KnownStates(t *testing.T) {
	cases := map[string]bool{
		ledspace.Segment:    false,
		ledspace.Drawer:     false,
		ledspace.Unresolved: true,
	}
	for space, want := range cases {
		t.Run(space, func(t *testing.T) {
			store, cleanup := setupLedspaceDB(t)
			defer cleanup()
			ctx := context.Background()

			if err := ledspace.Set(ctx, store, space); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := ledspace.IsUnresolved(ctx, store)
			if err != nil {
				t.Fatalf("IsUnresolved: %v", err)
			}
			if got != want {
				t.Fatalf("IsUnresolved = %v, want %v", got, want)
			}
		})
	}
}

// TestSet_RejectsUnknown verifies that an unknown space can never be persisted.
func TestSet_RejectsUnknown(t *testing.T) {
	store, cleanup := setupLedspaceDB(t)
	defer cleanup()
	ctx := context.Background()

	if err := ledspace.Set(ctx, store, "bogus"); err == nil {
		t.Fatal("expected Set to reject an unknown coordinate space")
	}
}
