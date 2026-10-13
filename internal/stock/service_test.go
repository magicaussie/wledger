package stock

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
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
		Name:         "Container1",
		ControllerID: ctrl.ID,
		SegmentID:    0,
	})

	bin1ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: sql.NullInt64{Int64: cont, Valid: true}, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	bin2ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A2", ContainerID: sql.NullInt64{Int64: cont, Valid: true}, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})

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
	err = svc.AdjustStock(ctx, AdjustStockRequest{PartID: partID, AssignmentID: assignmentID, Delta: 5})
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

	if err := svc.AdjustStock(ctx, AdjustStockRequest{PartID: partID, AssignmentID: orphan.ID, Delta: 3}); err != nil {
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

	err := svc.AdjustStock(ctx, AdjustStockRequest{PartID: 1, AssignmentID: 9999, Delta: 1})
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
	bin1, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: sql.NullInt64{Int64: cont, Valid: true}, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	bin2, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A2", ContainerID: sql.NullInt64{Int64: cont, Valid: true}, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})

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
	bin1, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: sql.NullInt64{Int64: cont, Valid: true}, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})

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
	bin1ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ContainerID: sql.NullInt64{Int64: cont, Valid: true}})

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

func TestAdjustStockRejectsPartMismatchAndFloorsQuantityAtZero(t *testing.T) {
	database, s, cleanup := setupTestDB(t)
	defer cleanup()
	svc := NewService(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))
	if _, err := s.CreateUser(context.Background(), db.CreateUserParams{Email: "adjust@test.com", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	partA := createTestPart(t, s, ctx, "Adjust A")
	partB := createTestPart(t, s, ctx, "Adjust B")
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partA, Quantity: 3}); err != nil {
		t.Fatal(err)
	}
	assignments, err := s.GetPartAssignments(ctx, partA)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("list assignments = %d, %v", len(assignments), err)
	}
	assignmentID := assignments[0].ID
	if _, err := database.ExecContext(ctx, "DELETE FROM audit_logs"); err != nil {
		t.Fatal(err)
	}

	if err := svc.AdjustStock(ctx, AdjustStockRequest{PartID: partB, AssignmentID: assignmentID, Delta: 1}); !errors.Is(err, ErrAssignmentOwnership) {
		t.Fatalf("mismatched adjustment error = %v, want ErrAssignmentOwnership", err)
	}
	unchanged, err := s.GetAssignment(ctx, assignmentID)
	if err != nil || unchanged.Quantity != 3 {
		t.Fatalf("mismatched adjustment changed assignment: %+v, %v", unchanged, err)
	}
	logs, err := s.GetAllAuditLogs(ctx)
	if err != nil || len(logs) != 0 {
		t.Fatalf("mismatched adjustment audit rows = %d, %v; want none", len(logs), err)
	}

	if err := svc.AdjustStock(ctx, AdjustStockRequest{PartID: partA, AssignmentID: assignmentID, Delta: -8}); err != nil {
		t.Fatalf("reduce stock below zero: %v", err)
	}
	updated, err := s.GetAssignment(ctx, assignmentID)
	if err != nil || updated.Quantity != 0 {
		t.Fatalf("quantity after reduction = %d, %v; want zero floor", updated.Quantity, err)
	}
}

func TestStockServiceRejectsInvalidIdentifiersAndQuantities(t *testing.T) {
	_, store, cleanup := setupTestDB(t)
	defer cleanup()
	svc := NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	cases := []struct {
		name string
		run  func() error
	}{
		{"zero part", func() error { return svc.AssignStock(ctx, AssignStockRequest{PartID: 0, BinID: 1, Quantity: 1}) }},
		{"zero bin", func() error { return svc.AssignStock(ctx, AssignStockRequest{PartID: 1, BinID: 0, Quantity: 1}) }},
		{"nonpositive assignment quantity", func() error { return svc.AssignStock(ctx, AssignStockRequest{PartID: 1, BinID: 1, Quantity: 0}) }},
		{"invalid move", func() error { return svc.MoveStock(ctx, MoveStockRequest{PartID: 1, AssignmentID: 1, TargetBinID: 0}) }},
		{"invalid remove", func() error { return svc.RemoveStock(ctx, RemoveStockRequest{PartID: 0, AssignmentID: 1}) }},
		{"invalid adjust", func() error { return svc.AdjustStock(ctx, AdjustStockRequest{PartID: 1, AssignmentID: 1, Delta: 0}) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, ErrInvalidStockRequest) {
				t.Fatalf("error = %v, want ErrInvalidStockRequest", err)
			}
		})
	}
}

type assignmentLookupErrorStore struct {
	db.Store
	err error
}

func (s assignmentLookupErrorStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	return s.Store.ExecImmediateTx(ctx, func(q db.Querier) error {
		return fn(assignmentLookupErrorQuerier{Querier: q, err: s.err})
	})
}

type assignmentLookupErrorQuerier struct {
	db.Querier
	err error
}

func (q assignmentLookupErrorQuerier) GetAssignmentID(context.Context, db.GetAssignmentIDParams) (int64, error) {
	return 0, q.err
}

func TestAssignStockUnexpectedLookupFailureIsNotTreatedAsMissing(t *testing.T) {
	database, store, cleanup := setupTestDB(t)
	defer cleanup()
	svc := NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, "ALTER TABLE part_assignments RENAME TO part_assignments_unavailable"); err != nil {
		t.Fatalf("inject query failure: %v", err)
	}
	err := svc.AssignStock(ctx, AssignStockRequest{PartID: 1, BinID: 1, Quantity: 1})
	if err == nil || errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrAssignmentNotFound) {
		t.Fatalf("unexpected lookup error was misclassified: %v", err)
	}
}

func TestMoveStockUnexpectedTargetLookupFailureIsPropagated(t *testing.T) {
	_, store, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))
	if _, err := store.CreateUser(context.Background(), db.CreateUserParams{Email: "move-error@test.com", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "192.0.2.12"})
	if err != nil {
		t.Fatal(err)
	}
	container, err := store.CreateContainer(ctx, db.CreateContainerParams{Name: "S", ControllerID: ctrl.ID})
	if err != nil {
		t.Fatal(err)
	}
	sourceBin, err := store.CreateBin(ctx, db.CreateBinParams{Name: "Source", ContainerID: sql.NullInt64{Int64: container, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	targetBin, err := store.CreateBin(ctx, db.CreateBinParams{Name: "Target", ContainerID: sql.NullInt64{Int64: container, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	partID := createTestPart(t, store, ctx, "Move error")
	normalService := NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := normalService.AssignStock(ctx, AssignStockRequest{PartID: partID, BinID: sourceBin, Quantity: 4}); err != nil {
		t.Fatal(err)
	}
	assignments, err := store.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("assignments = %d, %v", len(assignments), err)
	}
	lookupErr := errors.New("injected target lookup failure")
	failingService := NewService(assignmentLookupErrorStore{Store: store, err: lookupErr}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err = failingService.MoveStock(ctx, MoveStockRequest{PartID: partID, AssignmentID: assignments[0].ID, TargetBinID: targetBin})
	if !errors.Is(err, lookupErr) || errors.Is(err, ErrAssignmentNotFound) {
		t.Fatalf("MoveStock error = %v; want unexpected lookup error, not missing assignment", err)
	}
	unchanged, err := store.GetAssignment(ctx, assignments[0].ID)
	if err != nil || unchanged.BinID.Int64 != sourceBin || unchanged.Quantity != 4 {
		t.Fatalf("unexpected lookup error mutated source assignment: %+v, %v", unchanged, err)
	}
}

func TestMoveStockMergeRollsBackWhenSourceDeleteFails(t *testing.T) {
	database, store, cleanup := setupTestDB(t)
	defer cleanup()
	svc := NewService(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))
	if _, err := store.CreateUser(context.Background(), db.CreateUserParams{Email: "move@test.com", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	container, err := store.CreateContainer(ctx, db.CreateContainerParams{Name: "S", ControllerID: ctrl.ID})
	if err != nil {
		t.Fatal(err)
	}
	binA, err := store.CreateBin(ctx, db.CreateBinParams{Name: "A", ContainerID: sql.NullInt64{Int64: container, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	binB, err := store.CreateBin(ctx, db.CreateBinParams{Name: "B", ContainerID: sql.NullInt64{Int64: container, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	partID := createTestPart(t, store, ctx, "Merge")
	for _, assignment := range []AssignStockRequest{{PartID: partID, BinID: binA, Quantity: 7}, {PartID: partID, BinID: binB, Quantity: 11}} {
		if err := svc.AssignStock(ctx, assignment); err != nil {
			t.Fatal(err)
		}
	}
	assignments, err := store.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 2 {
		t.Fatalf("assignments = %d, %v", len(assignments), err)
	}
	var sourceID int64
	for _, assignment := range assignments {
		if assignment.BinID.Int64 == binA {
			sourceID = assignment.ID
		}
	}
	if sourceID == 0 {
		t.Fatal("source assignment not found")
	}
	if _, err := database.ExecContext(ctx, `CREATE TRIGGER fail_stock_delete BEFORE DELETE ON part_assignments BEGIN SELECT RAISE(ABORT, 'injected delete failure'); END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if err := svc.MoveStock(ctx, MoveStockRequest{PartID: partID, AssignmentID: sourceID, TargetBinID: binB}); err == nil {
		t.Fatal("expected injected source deletion failure")
	}
	assignments, err = store.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 2 {
		t.Fatalf("assignments after rollback = %d, %v; want both rows", len(assignments), err)
	}
	quantities := map[int64]int64{}
	for _, assignment := range assignments {
		quantities[assignment.BinID.Int64] = assignment.Quantity
	}
	if quantities[binA] != 7 || quantities[binB] != 11 {
		t.Fatalf("stock changed despite rollback: bins quantities = %#v", quantities)
	}
}

func TestAssignStockConcurrentAddsAreNotLost(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "stock-concurrency.sqlite")
	databaseA, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer databaseA.Close()
	if err := db.Migrate(databaseA); err != nil {
		t.Fatal(err)
	}
	databaseB, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer databaseB.Close()
	databaseA.SetMaxOpenConns(1)
	databaseB.SetMaxOpenConns(1)
	storeA := db.NewStore(databaseA)
	storeB := db.NewStore(databaseB)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svcA := NewService(storeA, logger)
	svcB := NewService(storeB, logger)
	ctx := context.Background()
	if _, err := storeA.CreateUser(ctx, db.CreateUserParams{Email: "concurrent@test.com", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	ctrl, err := storeA.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "192.0.2.11"})
	if err != nil {
		t.Fatal(err)
	}
	container, err := storeA.CreateContainer(ctx, db.CreateContainerParams{Name: "S", ControllerID: ctrl.ID})
	if err != nil {
		t.Fatal(err)
	}
	binID, err := storeA.CreateBin(ctx, db.CreateBinParams{Name: "A", ContainerID: sql.NullInt64{Int64: container, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	partID := createTestPart(t, storeA, ctx, "Concurrent")
	if err := svcA.AssignStock(ctx, AssignStockRequest{PartID: partID, BinID: binID, Quantity: 1}); err != nil {
		t.Fatal(err)
	}

	const workers = 12
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := range workers {
		service := svcA
		if i%2 != 0 {
			service = svcB
		}
		wg.Add(1)
		go func(service Service) {
			defer wg.Done()
			errCh <- service.AssignStock(ctx, AssignStockRequest{PartID: partID, BinID: binID, Quantity: 1})
		}(service)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent AssignStock: %v", err)
		}
	}
	assignments, err := storeA.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 1 || assignments[0].Quantity != workers+1 {
		t.Fatalf("concurrent stock result = %+v, %v; want one assignment with quantity %d", assignments, err, workers+1)
	}
}
