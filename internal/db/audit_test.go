package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestListAuditLogs(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a user
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        "admin@wledger.app",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create some audit logs
	err = q.CreateAuditLog(ctx, db.CreateAuditLogParams{
		UserID:     sql.NullInt64{Int64: int64(user.ID), Valid: true},
		ActionType: "CREATE",
		EntityType: "PART",
		EntityID:   1,
		Details:    sql.NullString{String: "Created part 1", Valid: true},
		OldValue:   []byte("{}"),
		NewValue:   []byte(`{"id": 1, "name": "Part 1"}`),
	})
	if err != nil {
		t.Fatalf("failed to create audit log 1: %v", err)
	}

	err = q.CreateAuditLog(ctx, db.CreateAuditLogParams{
		UserID:     sql.NullInt64{Int64: int64(user.ID), Valid: true},
		ActionType: "UPDATE",
		EntityType: "BIN",
		EntityID:   2,
		Details:    sql.NullString{String: "Updated bin 2", Valid: true},
		OldValue:   []byte(`{"id": 2, "name": "Bin 2"}`),
		NewValue:   []byte(`{"id": 2, "name": "Bin 2 Updated"}`),
	})
	if err != nil {
		t.Fatalf("failed to create audit log 2: %v", err)
	}

	// Test ListAuditLogs
	params := db.ListAuditLogsParams{
		Limit:      10,
		Offset:     0,
		ActionType: sql.NullString{String: "CREATE", Valid: true},
	}

	logs, err := q.ListAuditLogs(ctx, params)
	if err != nil {
		t.Fatalf("ListAuditLogs failed: %v", err)
	}

	if len(logs) != 1 {
		t.Errorf("expected 1 log entry, got %d", len(logs))
	}

	if logs[0].ActionType != "CREATE" {

		t.Errorf("expected action type CREATE, got %s", logs[0].ActionType)

	}

	if logs[0].UserEmail.String != "admin@wledger.app" {

		t.Errorf("expected user email admin@wledger.app, got %s", logs[0].UserEmail.String)

	}

	// Test CountAuditLogs

	countParams := db.CountAuditLogsParams{

		ActionType: sql.NullString{String: "CREATE", Valid: true},
	}

	count, err := q.CountAuditLogs(ctx, countParams)

	if err != nil {

		t.Fatalf("CountAuditLogs failed: %v", err)

	}

	if count != 1 {

		t.Errorf("expected count 1, got %d", count)

	}

	// Test with no filters

	totalCount, err := q.CountAuditLogs(ctx, db.CountAuditLogsParams{})

	if err != nil {

		t.Fatalf("CountAuditLogs (no filters) failed: %v", err)

	}

	if totalCount != 2 {

		t.Errorf("expected total count 2, got %d", totalCount)

	}

}
