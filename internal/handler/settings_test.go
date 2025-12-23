package handler

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestHandleSettingsUpdate(t *testing.T) {
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	queries := db.New(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	session := scs.New()

	// Initialize settings
	queries.InitSettings(context.Background())

	h := &Handler{
		Logger:   logger,
		Queries:  queries,
		Database: dbConn,
		Session:  session,
	}

	// 1. Verify initial state (should be false)
	s, _ := queries.GetSettings(context.Background())
	if s.EnableDebugLogs.Bool {
		t.Fatal("expected EnableDebugLogs to be false initially")
	}

	// 2. Perform update
	form := url.Values{}
	form.Add("require_auth", "on")
	form.Add("enable_timeout", "on")
	form.Add("locate_timeout", "15")
	form.Add("enable_debug_logs", "on")
	form.Add("color_locate", "#0000FF")
	form.Add("color_ok", "#00FF00")
	form.Add("color_low", "#FFFF00")
	form.Add("color_critical", "#FF0000")

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.HandleSettingsUpdate(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected status 303, got %d", rr.Code)
	}

	// 3. Verify final state
	s, _ = queries.GetSettings(context.Background())
	if !s.EnableDebugLogs.Bool {
		t.Error("expected EnableDebugLogs to be true after update")
	}
	if s.LocateTimeoutSeconds.Int64 != 15 {
		t.Errorf("expected locate_timeout to be 15, got %d", s.LocateTimeoutSeconds.Int64)
	}
}
