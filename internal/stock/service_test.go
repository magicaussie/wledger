package stock

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

// setupTestDB creates an in memory DB and applies the schema using db.Migrate
func setupTestDB(t *testing.T) (*sql.DB, db.Store, func()) {
	// Open in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Apply migrations automatically
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	// Create store helper
	s := db.NewStore(conn)

	// return cleanup function
	return conn, s, func() {
		conn.Close()
	}
}

func TestService(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Setup: User, Controller, Bins, Part
	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl1", IpAddress: "1.2.3.4"})

	cont, _ := s.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Container1",
		ControllerID: ctrl.ID,
		SegmentID: 0,
	})

	bin1ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	bin2ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A2", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})

	// Create Part directly via DB store to avoid dependency on parts service
	partID, err := s.CreatePart(ctx, db.CreatePartParams{
		Name: "Stock Part",
	})
	if err != nil {
		t.Fatalf("failed to create part: %v", err)
	}

	// Clear logs from setup
	database.ExecContext(ctx, "DELETE FROM audit_logs")

	// Assign Stock (Create Assignment)
	err = svc.AssignStock(ctx, AssignStockRequest{PartID: partID, BinID: bin1ID, Quantity: 10})
	if err != nil {
		t.Fatalf("AssignStock failed: %v", err)
	}

	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(logs))
	}
	assignLog := logs[0]
	var assignNew map[string]any
	json.Unmarshal(assignLog.NewValue, &assignNew)
	if assignNew["quantity"] != float64(10) || assignNew["bin_id"] != float64(bin1ID) {
		t.Errorf("expected assignment details in new_value, got: %s", string(assignLog.NewValue))
	}

	// Adjust Stock
	assignments, _ := s.GetPartAssignments(ctx, partID)
	assignmentID := assignments[0].ID
	err = svc.AdjustStock(ctx, assignmentID, 5)
	if err != nil {
		t.Fatalf("AdjustStock failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 2 {
		t.Fatalf("expected 2 audit logs, got %d", len(logs))
	}
	adjustLog := logs[1]
	var adjustOld, adjustNew map[string]any
	json.Unmarshal(adjustLog.OldValue, &adjustOld)
	json.Unmarshal(adjustLog.NewValue, &adjustNew)

	if adjustOld["quantity"] != float64(10) {
		t.Errorf("expected old qty 10, got %v", adjustOld["quantity"])
	}
	if adjustNew["quantity"] != float64(15) {
		t.Errorf("expected new qty 15, got %v", adjustNew["quantity"])
	}

	// Move Stock
	err = svc.MoveStock(ctx, MoveStockRequest{PartID: partID, AssignmentID: assignmentID, TargetBinID: bin2ID})
	if err != nil {
		t.Fatalf("MoveStock failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 3 {
		t.Fatalf("expected 3 audit logs, got %d", len(logs))
	}
	moveLog := logs[2]
	var moveOld, moveNew map[string]any
	json.Unmarshal(moveLog.OldValue, &moveOld)
	json.Unmarshal(moveLog.NewValue, &moveNew)

	if moveOld["bin_id"] != float64(bin1ID) {
		t.Errorf("expected old bin %d, got %v", bin1ID, moveOld["bin_id"])
	}
	if moveNew["bin_id"] != float64(bin2ID) {
		t.Errorf("expected new bin %d, got %v", bin2ID, moveNew["bin_id"])
	}

	// Remove Stock
	assignments, _ = s.GetPartAssignments(ctx, partID)
	newAssignmentID := assignments[0].ID

	err = svc.RemoveStock(ctx, RemoveStockRequest{PartID: partID, AssignmentID: newAssignmentID})
	if err != nil {
		t.Fatalf("RemoveStock failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 4 {
		t.Fatalf("expected 4 audit logs, got %d", len(logs))
	}
	removeLog := logs[3]
	var removeOld map[string]any
	json.Unmarshal(removeLog.OldValue, &removeOld)
	if removeOld["quantity"] != float64(15) {
		t.Errorf("expected removed qty 15 in old_value, got %v", removeOld["quantity"])
	}
}

// createTestPart is a small helper for the stock regression tests.
func createTestPart(t *testing.T, s db.Store, ctx context.Context, name string) int64 {
	t.Helper()
	id, err := s.CreatePart(ctx, db.CreatePartParams{Name: name})
	if err != nil {
		t.Fatalf("failed to create part %q: %v", name, err)
	}
	return id
}

// TestAdjustStock_OrphanedAssignment verifies that an assignment with a NULL
// bin_id (orphaned stock) can be adjusted. The previous implementation updated
// by (part_id, bin_id), and `bin_id = NULL` never matches, so the quantity was
// left unchanged while an audit entry still claimed a change.
func TestAdjustStock_OrphanedAssignment(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	partID := createTestPart(t, s, ctx, "Orphan Part")

	// Orphaned assignment: bin_id IS NULL.
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   partID,
		BinID:    sql.NullInt64{Valid: false},
		Quantity: 5,
	}); err != nil {
		t.Fatalf("failed to create orphaned assignment: %v", err)
	}

	assignments, err := s.GetAllPartAssignments(ctx)
	if err != nil {
		t.Fatalf("failed to list assignments: %v", err)
	}
	var orphan db.PartAssignment
	for _, a := range assignments {
		if a.PartID == partID {
			orphan = a
		}
	}
	if orphan.ID == 0 || orphan.BinID.Valid {
		t.Fatalf("expected an orphaned assignment, got %+v", orphan)
	}

	database.ExecContext(ctx, "DELETE FROM audit_logs")

	if err := svc.AdjustStock(ctx, orphan.ID, 3); err != nil {
		t.Fatalf("AdjustStock on orphaned assignment failed: %v", err)
	}

	// The quantity must actually change.
	updated, err := s.GetAssignment(ctx, orphan.ID)
	if err != nil {
		t.Fatalf("failed to reload assignment: %v", err)
	}
	if updated.Quantity != 8 {
		t.Errorf("expected quantity 8 after +3, got %d", updated.Quantity)
	}

	// The audit entry must reflect the real change.
	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(logs))
	}
	var newVal map[string]any
	json.Unmarshal(logs[0].NewValue, &newVal)
	if newVal["quantity"] != float64(8) {
		t.Errorf("expected audit new quantity 8, got %v", newVal["quantity"])
	}
}

// TestAdjustStock_NonexistentAssignment verifies a clear error is returned for
// an assignment id that does not exist.
func TestAdjustStock_NonexistentAssignment(t *testing.T) {
	_, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	svc := NewService(s, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	err := svc.AdjustStock(ctx, 9999, 1)
	if !errors.Is(err, ErrAssignmentNotFound) {
		t.Fatalf("expected ErrAssignmentNotFound, got %v", err)
	}
}

// TestMoveStock_PartAssignmentMismatch verifies that a move whose URL part id
// does not own the referenced assignment is rejected without mutating any
// stock or writing an audit entry.
func TestMoveStock_PartAssignmentMismatch(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	svc := NewService(s, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl1", IpAddress: "1.2.3.4"})
	cont, _ := s.CreateContainer(ctx, db.CreateContainerParams{Name: "Container1", ControllerID: ctrl.ID})
	bin1, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	bin2, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A2", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})

	partA := createTestPart(t, s, ctx, "Part A")
	partB := createTestPart(t, s, ctx, "Part B")

	if err := svc.AssignStock(ctx, AssignStockRequest{PartID: partA, BinID: bin1, Quantity: 10}); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	if err := svc.AssignStock(ctx, AssignStockRequest{PartID: partB, BinID: bin2, Quantity: 5}); err != nil {
		t.Fatalf("assign B: %v", err)
	}

	assignmentsA, _ := s.GetPartAssignments(ctx, partA)
	assignA := assignmentsA[0].ID

	database.ExecContext(ctx, "DELETE FROM audit_logs")

	// Move part A's assignment while claiming it belongs to part B.
	err := svc.MoveStock(ctx, MoveStockRequest{PartID: partB, AssignmentID: assignA, TargetBinID: bin2})
	if !errors.Is(err, ErrAssignmentOwnership) {
		t.Fatalf("expected ErrAssignmentOwnership, got %v", err)
	}

	afterA, _ := s.GetPartAssignments(ctx, partA)
	if len(afterA) != 1 || afterA[0].Quantity != 10 || afterA[0].BinID.Int64 != bin1 {
		t.Errorf("part A stock was modified: %+v", afterA)
	}
	afterB, _ := s.GetPartAssignments(ctx, partB)
	if len(afterB) != 1 || afterB[0].Quantity != 5 {
		t.Errorf("part B stock was modified: %+v", afterB)
	}

	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 0 {
		t.Errorf("expected no audit logs for rejected move, got %d", len(logs))
	}
}

// TestRemoveStock_NonexistentAssignment verifies a clear error is returned and
// no audit entry is written for a missing assignment.
func TestRemoveStock_NonexistentAssignment(t *testing.T) {
	_, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	svc := NewService(s, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	err := svc.RemoveStock(ctx, RemoveStockRequest{PartID: 1, AssignmentID: 9999})
	if !errors.Is(err, ErrAssignmentNotFound) {
		t.Fatalf("expected ErrAssignmentNotFound, got %v", err)
	}

	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 0 {
		t.Errorf("expected no audit logs for missing assignment, got %d", len(logs))
	}
}

// TestRemoveStock_PartMismatch verifies that a removal whose URL part id does
// not own the referenced assignment is rejected without deleting stock or
// writing an audit entry.
func TestRemoveStock_PartMismatch(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	svc := NewService(s, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl1", IpAddress: "1.2.3.4"})
	cont, _ := s.CreateContainer(ctx, db.CreateContainerParams{Name: "Container1", ControllerID: ctrl.ID})
	bin1, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})

	partA := createTestPart(t, s, ctx, "Part A")
	partB := createTestPart(t, s, ctx, "Part B")

	if err := svc.AssignStock(ctx, AssignStockRequest{PartID: partA, BinID: bin1, Quantity: 10}); err != nil {
		t.Fatalf("assign A: %v", err)
	}
	assignmentsA, _ := s.GetPartAssignments(ctx, partA)
	assignA := assignmentsA[0].ID

	database.ExecContext(ctx, "DELETE FROM audit_logs")

	err := svc.RemoveStock(ctx, RemoveStockRequest{PartID: partB, AssignmentID: assignA})
	if !errors.Is(err, ErrAssignmentOwnership) {
		t.Fatalf("expected ErrAssignmentOwnership, got %v", err)
	}

	afterA, _ := s.GetPartAssignments(ctx, partA)
	if len(afterA) != 1 || afterA[0].Quantity != 10 {
		t.Errorf("part A stock was modified: %+v", afterA)
	}

	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 0 {
		t.Errorf("expected no audit logs for rejected removal, got %d", len(logs))
	}
}

func TestMoveStock_SameBin(t *testing.T) {
	_, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Setup
	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl1", IpAddress: "1.2.3.4"})
	cont, _ := s.CreateContainer(ctx, db.CreateContainerParams{Name: "Container1", ControllerID: ctrl.ID})
	bin1ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: cont})

	partID, _ := s.CreatePart(ctx, db.CreatePartParams{Name: "Stock Part"})

	// Assign Stock
	_ = svc.AssignStock(ctx, AssignStockRequest{PartID: partID, BinID: bin1ID, Quantity: 10})

	assignments, _ := s.GetPartAssignments(ctx, partID)
	assignmentID := assignments[0].ID

	// Action: Move Stock to SAME Bin
	err := svc.MoveStock(ctx, MoveStockRequest{PartID: partID, AssignmentID: assignmentID, TargetBinID: bin1ID})
	if err != nil {
		t.Fatalf("MoveStock failed: %v", err)
	}

	// Verify: Stock should still be there, unchanged
	assignments, _ = s.GetPartAssignments(ctx, partID)
	if len(assignments) != 1 {
		t.Fatalf("Expected 1 assignment, got %d. Stock was likely removed!", len(assignments))
	}

	if assignments[0].Quantity != 10 {
		t.Errorf("Expected quantity 10, got %d", assignments[0].Quantity)
	}
}
