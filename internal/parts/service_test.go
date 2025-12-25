package parts

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
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
	docSvc := documents.NewService(s, logger)
	svc := NewService(database, s, logger, tagSvc, docSvc)

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
