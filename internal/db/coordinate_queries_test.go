package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// TestGetControllerByIPAndGetBinByContainerAndLed verifies that the two queries
// added for drawer-relative CSV location lookups are generated and behave
// correctly, including their not-found behaviour.
func TestGetControllerByIPAndGetBinByContainerAndLed(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	ctrl, err := q.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "10.0.0.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	got, err := q.GetControllerByIP(ctx, "10.0.0.1")
	if err != nil {
		t.Fatalf("GetControllerByIP: %v", err)
	}
	if got.ID != ctrl.ID {
		t.Errorf("GetControllerByIP id = %d, want %d", got.ID, ctrl.ID)
	}
	if _, err := q.GetControllerByIP(ctx, "10.0.0.2"); err == nil {
		t.Error("expected an error for an unknown controller IP")
	}

	cont, err := q.CreateContainer(ctx, db.CreateContainerParams{
		Name: "A", ControllerID: ctrl.ID, SegmentID: 0, LedStart: 10, LedCount: 5,
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	bin, err := q.CreateBin(ctx, db.CreateBinParams{
		Name: "b1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 3, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin: %v", err)
	}
	gotBin, err := q.GetBinByContainerAndLed(ctx, db.GetBinByContainerAndLedParams{
		ContainerID: cont, LedIndex: sql.NullInt64{Int64: 3, Valid: true},
	})
	if err != nil {
		t.Fatalf("GetBinByContainerAndLed: %v", err)
	}
	if gotBin != bin {
		t.Errorf("GetBinByContainerAndLed id = %d, want %d", gotBin, bin)
	}
	if _, err := q.GetBinByContainerAndLed(ctx, db.GetBinByContainerAndLedParams{
		ContainerID: cont, LedIndex: sql.NullInt64{Int64: 99, Valid: true},
	}); err == nil {
		t.Error("expected an error for an unknown bin location")
	}
}
