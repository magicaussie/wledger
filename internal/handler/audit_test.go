package handler

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestHandleAuditLogs(t *testing.T) {
	// Reusing openTestDB and setupTestSchema from hardware_test.go
	// Ensure to run tests with go test ./internal/handler/...
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	queries := db.New(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	session := scs.New()

	h := &Handler{
		Logger:   logger,
		Queries:  queries,
		Database: dbConn,
		Session:  session,
	}

	ctx := context.Background()

	// Setup Users
	admin, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:                  "admin@test.com",
		Role:                   "admin",
		PasswordHash:           "hash",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}

	viewer, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:                  "viewer@test.com",
		Role:                   "viewer",
		PasswordHash:           "hash",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create viewer: %v", err)
	}

	// Setup Data
	err = queries.CreateAuditLog(ctx, db.CreateAuditLogParams{
		ActionType: "CREATE",
		EntityType: "PART",
		EntityID:   1,
		Details:    sql.NullString{String: "Created Part", Valid: true},
		OldValue:   []byte("{}"),
		NewValue:   []byte("{}"),
	})
	if err != nil {
		t.Fatalf("failed to create log 1: %v", err)
	}

	err = queries.CreateAuditLog(ctx, db.CreateAuditLogParams{
		ActionType: "DELETE",
		EntityType: "BIN",
		EntityID:   2,
		Details:    sql.NullString{String: "Deleted Bin", Valid: true},
		OldValue:   []byte("{}"),
		NewValue:   []byte("{}"),
	})
	if err != nil {
		t.Fatalf("failed to create log 2: %v", err)
	}

	// Test: Admin can filter logs
	req := httptest.NewRequest(http.MethodGet, "/audit-logs?action_type=CREATE", nil)
	// Inject Admin Context
	ctx = auth.WithUser(req.Context(), auth.User{ID: admin.ID, Role: "admin"})
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.HandleAuditLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "Created Part") {
		t.Errorf("expected HTML to contain 'Created Part'")
	}
	if strings.Contains(body, "Deleted Bin") {
		t.Errorf("expected HTML NOT to contain 'Deleted Bin' (filtered out)")
	}

	// Test: Non-Admin is Forbidden
	req = httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	// Inject Viewer Context
	ctx = auth.WithUser(req.Context(), auth.User{ID: viewer.ID, Role: "viewer"})
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	h.HandleAuditLogs(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 Forbidden for viewer, got %d", rr.Code)
	}
}
