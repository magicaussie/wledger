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

func TestUpdatePartStockThresholdsToZero(t *testing.T) {
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

	// 1. Create part with non-zero thresholds
	req := CreatePartRequest{
		Name:              "Stock Part",
		UnitCost:          10.50,
		ReorderLevel:      5,
		MinStockThreshold: 2,
	}
	id, err := svc.CreatePart(ctx, req)
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// Verify initial state
	p, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if p.ReorderLevel.Int64 != 5 {
		t.Errorf("expected reorder level 5, got %d", p.ReorderLevel.Int64)
	}
	if p.MinStockThreshold.Int64 != 2 {
		t.Errorf("expected min stock 2, got %d", p.MinStockThreshold.Int64)
	}
	if p.UnitCost.Float64 != 10.50 {
		t.Errorf("expected unit cost 10.50, got %f", p.UnitCost.Float64)
	}

	// 2. Update to Zero
	updateReq := UpdatePartRequest{
		ID:                id,
		Name:              "Stock Part Updated",
		ReorderLevel:      0,
		MinStockThreshold: 0,
		UnitCost:          0,
	}
	err = svc.UpdatePart(ctx, updateReq)
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	// 3. Verify update
	pUpdated, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}

	if pUpdated.ReorderLevel.Int64 != 0 {
		t.Errorf("expected reorder level 0, got %d", pUpdated.ReorderLevel.Int64)
	}
	if pUpdated.MinStockThreshold.Int64 != 0 {
		t.Errorf("expected min stock 0, got %d", pUpdated.MinStockThreshold.Int64)
	}
	if pUpdated.UnitCost.Float64 != 0 {
		t.Errorf("expected unit cost 0, got %f", pUpdated.UnitCost.Float64)
	}
}

func TestUpdatePartClearFields(t *testing.T) {
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

	// 1. Create part with full details
	req := CreatePartRequest{
		Name:         "Full Part",
		Description:  "Has Description",
		PartNumber:   "PN-123",
		Manufacturer: "Has Manufacturer",
		Supplier:     "Has Supplier",
		BarcodeData:  "BC-123",
	}
	id, err := svc.CreatePart(ctx, req)
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	// 2. Update to clear some fields
	updateReq := UpdatePartRequest{
		ID:           id,
		Name:         "Full Part Updated",
		Description:  "", // Clear
		PartNumber:   "", // Clear
		Manufacturer: "Has Manufacturer", // Keep
		Supplier:     "", // Clear
		BarcodeData:  "", // Clear
	}
	err = svc.UpdatePart(ctx, updateReq)
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	// 3. Verify
	p, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}

	if p.Description.String != "" || p.Description.Valid {
		t.Errorf("expected description to be cleared, got %q (Valid: %v)", p.Description.String, p.Description.Valid)
	}
	if p.PartNumber.String != "" || p.PartNumber.Valid {
		t.Errorf("expected part number to be cleared, got %q (Valid: %v)", p.PartNumber.String, p.PartNumber.Valid)
	}
	if p.Supplier.String != "" || p.Supplier.Valid {
		t.Errorf("expected supplier to be cleared, got %q (Valid: %v)", p.Supplier.String, p.Supplier.Valid)
	}
	if p.BarcodeData.String != "" || p.BarcodeData.Valid {
		t.Errorf("expected barcode to be cleared, got %q (Valid: %v)", p.BarcodeData.String, p.BarcodeData.Valid)
	}
	if p.Manufacturer.String != "Has Manufacturer" {
		t.Errorf("expected manufacturer to remain, got %q", p.Manufacturer.String)
	}
}
