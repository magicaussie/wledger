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
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func TestHandleAuditLogs(t *testing.T) {
	// Reusing openTestDB and setupTestSchema from hardware_test.go
	// Run tests with go test ./internal/handler/...
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	session := scs.New()
	uiError := uierror.New(logger)

	auditService := audit.NewService(s)
	dashboardService := dashboard.NewService(s)
	wClient := wled.NewClient()
	wService := wled.NewService(s, wClient, logger)
	hardwareService := hardware.NewService(s, wClient, logger)
	settingsService := settings.NewService(s)

	h := &Handler{
		Logger:    logger,
		Queries:   s,
		Database:  dbConn,
		Session:   session,
		UIError:   uiError,
		Audit:     auditService,
		Dashboard: dashboardService,
		Hardware:  hardwareService,
		Settings:  settingsService,
		WLED:      wService,
	}

	ctx := context.Background()

	// Setup Users
	admin, err := s.CreateUser(ctx, db.CreateUserParams{
		Email:                  "admin@test.com",
		Role:                   "admin",
		PasswordHash:           "hash",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}

	viewer, err := s.CreateUser(ctx, db.CreateUserParams{
		Email:                  "viewer@test.com",
		Role:                   "viewer",
		PasswordHash:           "hash",
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create viewer: %v", err)
	}

	// Setup Data
	err = s.CreateAuditLog(ctx, db.CreateAuditLogParams{
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

	err = s.CreateAuditLog(ctx, db.CreateAuditLogParams{
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

	// Test: Infinite Scroll Partial Render
	req = httptest.NewRequest(http.MethodGet, "/audit-logs?scroll=true", nil)
	ctx = auth.WithUser(req.Context(), auth.User{ID: admin.ID, Role: "admin"})
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	h.HandleAuditLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}
	body = rr.Body.String()
	// Should contain rows but NOT the full page header
	if !strings.Contains(body, "Created Part") {
		t.Errorf("expected HTML to contain log entries")
	}
	if strings.Contains(body, "<html") || strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("expected partial HTML, but got full page wrapper")
	}

	// Test: Default Batch Size
	// Create > 20 logs to verify default limit
	for i := 0; i < 25; i++ {
		_ = s.CreateAuditLog(ctx, db.CreateAuditLogParams{
			ActionType: "UPDATE",
			EntityType: "PART",
			EntityID:   int64(i + 100),
			Details:    sql.NullString{String: "Bulk Log", Valid: true},
			OldValue:   []byte("{}"),
			NewValue:   []byte("{}"),
		})
	}

	req = httptest.NewRequest(http.MethodGet, "/audit-logs", nil)
	ctx = auth.WithUser(req.Context(), auth.User{ID: admin.ID, Role: "admin"})
	req = req.WithContext(ctx)

	rr = httptest.NewRecorder()
	h.HandleAuditLogs(rr, req)

	body = rr.Body.String()
	// Count occurrences of "Bulk Log"
	count := strings.Count(body, "Bulk Log")

	if count > 20 {
		t.Errorf("expected default limit of 20, but found %d items", count)
	}
}
