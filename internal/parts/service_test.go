package parts

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/tags"
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

func TestPartAuditLogging(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tagSvc := tags.NewService(database, s)
	svc := NewService(database, s, logger, tagSvc)

	// Create user to satisfy FK
	_, err := s.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Test CreatePart Audit
	req := CreatePartRequest{
		Name:        "Audit Part",
		Description: "Audit Description",
		PartNumber:  "PN-123",
	}
	newID, err := svc.CreatePart(ctx, req)
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	logs, err := s.GetAllAuditLogs(ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 audit log, got %d", len(logs))
	}
	createLog := logs[0]
	if createLog.ActionType != "CREATE" || createLog.EntityType != "PART" {
		t.Errorf("unexpected log metadata: %+v", createLog)
	}

	// Verify Create Log Body
	var newValue map[string]any
	json.Unmarshal(createLog.NewValue, &newValue)
	if newValue["id"] != float64(newID) || newValue["name"] != "Audit Part" {
		t.Errorf("expected summary in new_value, got: %v", string(createLog.NewValue))
	}

	// Test UpdatePart Audit (Diff)
	updateReq := UpdatePartRequest{
		ID:          newID,
		Name:        "Audit Part Updated",
		Description: "Audit Description",
		PartNumber:  "PN-456",
	}
	err = svc.UpdatePart(ctx, updateReq)
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 2 {
		t.Fatalf("expected 2 audit logs, got %d", len(logs))
	}

	updateLog := logs[1]
	var oldVal, newVal map[string]any
	json.Unmarshal(updateLog.OldValue, &oldVal)
	json.Unmarshal(updateLog.NewValue, &newVal)

	// Verify Old Values (Only changed fields)
	if oldVal["name"] != "Audit Part" || oldVal["part_number"] != "PN-123" {
		t.Errorf("expected old values in diff, got: %s", string(updateLog.OldValue))
	}
	if _, exists := oldVal["description"]; exists {
		t.Error("description should NOT be in diff if it didn't change")
	}

	// Verify New Values (Only changed fields)
	if newVal["name"] != "Audit Part Updated" || newVal["part_number"] != "PN-456" {
		t.Errorf("expected new values in diff, got: %s", string(updateLog.NewValue))
	}

	// Test DeletePart Audit
	err = svc.DeletePart(ctx, newID)
	if err != nil {
		t.Fatalf("DeletePart failed: %v", err)
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 3 {
		t.Fatalf("expected 3 audit logs, got %d", len(logs))
	}

	deleteLog := logs[2]
	var deleteOld map[string]any
	json.Unmarshal(deleteLog.OldValue, &deleteOld)
	if deleteOld["id"] != float64(newID) || deleteOld["name"] != "Audit Part Updated" {
		t.Errorf("expected summary in old_value for delete, got: %s", string(deleteLog.OldValue))
	}
}

func TestStockAuditLogging(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tagSvc := tags.NewService(database, s)
	svc := NewService(database, s, logger, tagSvc)
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Setup: User, Controller, Bins, Part
	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctrl, _ := s.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl1", IpAddress: "1.2.3.4"})
	bin1ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A1", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, LedIndex: sql.NullInt64{Int64: 0, Valid: true}})
	bin2ID, _ := s.CreateBin(ctx, db.CreateBinParams{Name: "A2", ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true}, LedIndex: sql.NullInt64{Int64: 1, Valid: true}})
	partID, _ := svc.CreatePart(ctx, CreatePartRequest{Name: "Stock Part"})

	// Clear logs from setup
	database.ExecContext(ctx, "DELETE FROM audit_logs")

	// Assign Stock (Create Assignment)
	err := svc.AssignStock(ctx, AssignStockRequest{PartID: partID, BinID: bin1ID, Quantity: 10})
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
