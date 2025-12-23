package handler

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
)

// openTestDB opens an in-memory database with Foreign Keys enabled.
func openTestDB(t *testing.T) *sql.DB {
	dsn := "file::memory:?cache=shared&_foreign_keys=on"
	dbConn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	return dbConn
}

// setupTestSchema applies the schema files to the test database.
func setupTestSchema(t *testing.T, dbConn *sql.DB) {
	// Schema path relative to internal/handler
	schemaDir := "../../sql/schema"

	files := []string{"001_init.sql", "002_fts_triggers.sql"}
	for _, f := range files {
		path := filepath.Join(schemaDir, f)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read schema file %s: %v", path, err)
		}
		if _, err := dbConn.Exec(string(content)); err != nil {
			t.Fatalf("failed to apply schema %s: %v", f, err)
		}
	}
}

func TestControllerDeleteCascadesToBins(t *testing.T) {
	// Setup Environment
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	queries := db.New(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	h := &Handler{
		Logger:   logger,
		Queries:  queries,
		Database: dbConn,
	}

	ctx := context.Background()

	// Create Controller
	ctrl, err := queries.CreateController(ctx, db.CreateControllerParams{
		Name:      "TestController",
		IpAddress: "192.168.1.100",
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// Create Bins (Simulate an 8-pixel strip)
	for i := 0; i < 8; i++ {
		_, err := queries.CreateBin(ctx, db.CreateBinParams{
			Name:         "Bin-" + strconv.Itoa(i),
			ControllerID: sql.NullInt64{Int64: ctrl.ID, Valid: true},
			LedIndex:     sql.NullInt64{Int64: int64(i), Valid: true},
			Width:        sql.NullInt64{Int64: 1, Valid: true},
			GridX:        sql.NullInt64{Int64: int64(i), Valid: true},
			GridY:        sql.NullInt64{Int64: 0, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin %d: %v", i, err)
		}
	}

	// Verify Bins exist
	binsBefore, err := queries.GetBinsByController(ctx, sql.NullInt64{Int64: ctrl.ID, Valid: true})
	if err != nil {
		t.Fatalf("failed to fetch bins: %v", err)
	}
	if len(binsBefore) != 8 {
		t.Fatalf("expected 8 bins, got %d", len(binsBefore))
	}

	// Delete Controller via HTTP Handler
	// This exercises the `HandleHardwareDelete` method, ensuring the transaction logic works.
	r := chi.NewRouter()
	r.Post("/hardware/{id}/delete", h.HandleHardwareDelete)

	target := "/hardware/" + strconv.Itoa(int(ctrl.ID)) + "/delete"
	req := httptest.NewRequest(http.MethodPost, target, nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	// Check Handler Response
	if rr.Code != http.StatusSeeOther {
		t.Errorf("Handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	// Assertions

	// Check Bins by Controller (Should be 0)
	binsAfter, err := queries.GetBinsByController(ctx, sql.NullInt64{Int64: ctrl.ID, Valid: true})
	if err != nil {
		t.Fatalf("failed to fetch bins after delete: %v", err)
	}
	if len(binsAfter) != 0 {
		t.Errorf("expected 0 bins for controller, got %d", len(binsAfter))
	}

	// Check Global Bin Count (Should be 0 - ensuring no orphans/ghosts)
	// This confirms that bins were DELETED, not just set to NULL.
	var count int
	err = dbConn.QueryRow("SELECT count(*) FROM bins").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count bins: %v", err)
	}
	if count != 0 {
		t.Errorf("Ghost Bins Detected! Expected 0 total bins, got %d. They may have been orphaned.", count)
	}
}
