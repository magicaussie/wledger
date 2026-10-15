package handler

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

// setupUploadsMaintenanceHandler builds a Handler whose uploads directory is a
// disposable temp directory, so no real upload files are touched.
func setupUploadsMaintenanceHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	t.Chdir(t.TempDir())
	dbConn := openTestDB(t)
	setupTestSchema(t, dbConn)
	h := &Handler{Database: dbConn, Queries: db.NewStore(dbConn), UIError: uierror.New(slog.Default())}
	uploadsDir := config.DirUploads
	if err := os.MkdirAll(filepath.Join(uploadsDir, "images"), 0o755); err != nil {
		t.Fatalf("create uploads dir: %v", err)
	}
	return h, uploadsDir
}

func writeMaintenanceUpload(t *testing.T, uploadsDir, rel, content string) {
	t.Helper()
	full := filepath.Join(uploadsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write upload: %v", err)
	}
}

func TestUploadsMaintenancePageListsOrphans(t *testing.T) {
	h, uploadsDir := setupUploadsMaintenanceHandler(t)
	if _, err := h.Database.Exec(`INSERT INTO parts (name, image_path) VALUES ('Part', '/uploads/images/part_1.jpg')`); err != nil {
		t.Fatalf("insert part: %v", err)
	}
	writeMaintenanceUpload(t, uploadsDir, "images/part_1.jpg", "referenced")
	writeMaintenanceUpload(t, uploadsDir, "images/part_1_thumb.jpg", "referenced")
	writeMaintenanceUpload(t, uploadsDir, "images/orphan.jpg", "orphan")

	router := chi.NewRouter()
	router.Get("/settings/uploads", h.HandleUploadsMaintenance)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/settings/uploads", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "images/orphan.jpg") {
		t.Errorf("orphan file not listed: %s", body)
	}
	if strings.Contains(body, "images/part_1.jpg") {
		t.Errorf("referenced file must not be listed as an orphan: %s", body)
	}
}

func TestUploadsMaintenanceDeleteRemovesOnlyOrphans(t *testing.T) {
	h, uploadsDir := setupUploadsMaintenanceHandler(t)
	if _, err := h.Database.Exec(`INSERT INTO parts (name, image_path) VALUES ('Part', '/uploads/images/part_1.jpg')`); err != nil {
		t.Fatalf("insert part: %v", err)
	}
	writeMaintenanceUpload(t, uploadsDir, "images/part_1.jpg", "referenced")
	writeMaintenanceUpload(t, uploadsDir, "images/part_1_thumb.jpg", "referenced")
	writeMaintenanceUpload(t, uploadsDir, "images/orphan.jpg", "orphan")

	router := chi.NewRouter()
	router.Post("/settings/uploads/delete", h.HandleUploadsMaintenanceDelete)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/settings/uploads/delete", nil))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); location != "/settings/uploads?deleted=1" {
		t.Errorf("redirect = %q, want /settings/uploads?deleted=1", location)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "images", "orphan.jpg")); !os.IsNotExist(err) {
		t.Errorf("orphan file still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "images", "part_1.jpg")); err != nil {
		t.Errorf("referenced file was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "images", "part_1_thumb.jpg")); err != nil {
		t.Errorf("referenced thumbnail was removed: %v", err)
	}
}

func TestUploadsMaintenanceReportsAuditFailureAfterRemoval(t *testing.T) {
	h, dir := setupUploadsMaintenanceHandler(t)
	writeMaintenanceUpload(t, dir, "images/orphan.jpg", "obsolete")
	if _, err := h.Database.Exec(`CREATE TRIGGER fail_upload_audit BEFORE INSERT ON audit_logs BEGIN SELECT RAISE(ABORT, 'forced audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.HandleUploadsMaintenanceDelete(rec, httptest.NewRequest(http.MethodPost, "/settings/uploads/delete", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("audit failure falsely reported success: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "images/orphan.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected removal before audit failure: %v", err)
	}
}
