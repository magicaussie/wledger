package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// buildRestoreZipWithFiles builds a restore archive with the manifest plus the
// given uploads files (name -> contents).
func buildRestoreZipWithFiles(t *testing.T, manifest Manifest, files map[string]string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	fJson, err := zw.Create("restore_data.json")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if err := json.NewEncoder(fJson).Encode(manifest); err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	for name, content := range files {
		f, err := zw.Create("uploads/" + name)
		if err != nil {
			t.Fatalf("create uploads entry: %v", err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("write uploads entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func findFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = p
		}
		return nil
	})
	return found
}

func restoreUserManifest() Manifest {
	return Manifest{
		Version:  "1.0",
		Settings: minimalSettings(),
		Users: []db.User{{
			ID: 99, Email: "restored@example.com", PasswordHash: "h", Role: "admin",
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		}},
	}
}

// TestRestore_DBFailureWithUploadsPresent verifies that a database failure leaves
// both the database and the uploads directory untouched.
func TestRestore_DBFailureWithUploadsPresent(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	// A manifest whose part assignment references a non-existent part -> FK error.
	manifest := Manifest{
		Version:  "1.0",
		Settings: minimalSettings(),
		PartAssignments: []db.PartAssignment{
			{ID: 1, PartID: 9999, Quantity: 1},
		},
	}
	zipBytes := buildRestoreZipWithFiles(t, manifest, map[string]string{"new.txt": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err == nil {
		t.Fatal("expected restore to fail on FK violation")
	}
	if _, err := s.GetSettings(ctx); err != nil {
		t.Errorf("settings missing after failed restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); os.IsNotExist(err) {
		t.Error("original upload missing after failed restore")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "new.txt")); err == nil {
		t.Error("new upload present despite failed restore")
	}
}

// TestRestore_FilesystemSwapFailureRollsBack verifies that a failure while moving
// the staged uploads into place rolls the filesystem back to the previous state
// and is reported as a committed-with-warning error (the database is committed).
func TestRestore_FilesystemSwapFailureRollsBack(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	orig := renameEntry
	defer func() { renameEntry = orig }()
	renameEntry = func(src, dst string) error {
		// Fail only when moving a staged temp file into the live uploads dir.
		if strings.Contains(src, ".restore_tmp_") {
			return errors.New("injected swap failure")
		}
		return os.Rename(src, dst)
	}

	zipBytes := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	var committed *RestoreCommittedError
	if !errors.As(err, &committed) {
		t.Fatalf("expected RestoreCommittedError, got %v", err)
	}

	// Database committed.
	if _, err := s.GetUserByEmail(ctx, "restored@example.com"); err != nil {
		t.Errorf("restored user missing (database should be committed): %v", err)
	}
	// Filesystem rolled back to the previous state.
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); os.IsNotExist(err) {
		t.Error("original upload not restored after swap rollback")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "new.txt")); err == nil {
		t.Error("new upload present despite failed swap")
	}
}

// TestRestore_FilesystemRollbackFailurePreservesPreviousUploads verifies that when
// both the swap and its rollback fail, the previous uploads are preserved (never
// deleted) and the error points at their location.
func TestRestore_FilesystemRollbackFailurePreservesPreviousUploads(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	orig := renameEntry
	defer func() { renameEntry = orig }()
	renameEntry = func(src, dst string) error {
		// Fail the temp->live move and the backup->live restore.
		if strings.Contains(src, ".restore_tmp_") {
			return errors.New("injected swap failure")
		}
		if strings.Contains(src, ".uploads_bak_") {
			return errors.New("injected rollback failure")
		}
		return os.Rename(src, dst)
	}

	zipBytes := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	var committed *RestoreCommittedError
	if !errors.As(err, &committed) {
		t.Fatalf("expected RestoreCommittedError, got %v", err)
	}
	if !strings.Contains(committed.Reason, ".uploads_bak_") {
		t.Errorf("error should point at the preserved uploads, got %q", committed.Reason)
	}

	// The previous upload must still exist somewhere under the uploads dir.
	if findFile(t, uploadsDir, "test.txt") == "" {
		t.Error("previous upload was lost after a failed swap and rollback")
	}
}

// TestRestore_SuccessRestoresDBAndUploads verifies a successful restore replaces
// both the database records and the uploaded files together.
func TestRestore_SuccessRestoresDBAndUploads(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()

	zipBytes := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if _, err := s.GetUserByEmail(ctx, "restored@example.com"); err != nil {
		t.Errorf("restored user missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "new.txt")); os.IsNotExist(err) {
		t.Error("restored upload missing")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); err == nil {
		t.Error("previous upload still present after successful restore")
	}
	// No leftover staging/backup directories.
	entries, _ := os.ReadDir(uploadsDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".restore_tmp_") || strings.HasPrefix(e.Name(), ".uploads_bak_") {
			t.Errorf("leftover staging directory after successful restore: %s", e.Name())
		}
	}
}
