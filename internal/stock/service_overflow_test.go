package stock

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

// TestAdjustStockRejectsOverflow verifies that a positive adjustment which would
// overflow int64 is rejected instead of wrapping negative and being clamped to
// zero, which previously destroyed a large recorded quantity.
func TestAdjustStockRejectsOverflow(t *testing.T) {
	_, s, cleanup := setupTestDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Overflow Controller", IpAddress: "1.2.3.4"})
	cont, _ := s.CreateContainer(ctx, db.CreateContainerParams{Name: "Overflow Container", ControllerID: ctrl.ID, SegmentID: 0})
	bin, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: sql.NullInt64{Int64: cont, Valid: true}})
	part, _ := s.CreatePart(ctx, db.CreatePartParams{Name: "Overflow Part"})
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   part,
		BinID:    sql.NullInt64{Int64: bin, Valid: true},
		Quantity: math.MaxInt64,
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	assignment, err := s.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: part,
		BinID:  sql.NullInt64{Int64: bin, Valid: true},
	})
	if err != nil {
		t.Fatalf("look up assignment: %v", err)
	}

	err = svc.AdjustStock(ctx, AdjustStockRequest{PartID: part, AssignmentID: assignment, Delta: 1})
	if err == nil {
		t.Fatal("overflowing adjustment succeeded, want rejection")
	}
	if !errors.Is(err, ErrInvalidStockRequest) {
		t.Fatalf("overflow error = %v, want ErrInvalidStockRequest", err)
	}

	after, err := s.GetAssignment(ctx, assignment)
	if err != nil {
		t.Fatalf("get assignment: %v", err)
	}
	if after.Quantity != math.MaxInt64 {
		t.Fatalf("quantity after rejected overflow = %d, want %d", after.Quantity, int64(math.MaxInt64))
	}
}
