package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// setupTestDB creates an in memory DB and applies the schema
func setupTestDB(t *testing.T) (*sql.DB, *db.Queries, func()) {
	// Open in-memory DB
	// cache=shared ensures different connections see the same in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Read schema files
	// internal/backup -> ../../sql/schema
	schemaPath := "../../sql/schema"
	files := []string{"001_init.sql", "002_fts_triggers.sql"}

	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(schemaPath, file))
		if err != nil {
			t.Fatalf("failed to read schema file %s: %v", file, err)
		}
		if _, err := conn.Exec(string(content)); err != nil {
			t.Fatalf("failed to apply schema %s: %v", file, err)
		}
	}

	// Create queries helper
	q := db.New(conn)

	// return cleanup function
	return conn, q, func() {
		conn.Close()
	}
}

func setupTestUploads(t *testing.T) (string, func()) {
	// Create a temp directory for uploads
	dir, err := os.MkdirTemp("", "wledger_test_uploads_*")
	if err != nil {
		t.Fatalf("failed to create temp uploads dir: %v", err)
	}

	// Create a dummy file
	dummyFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(dummyFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	// Create a subdir
	subDir := filepath.Join(dir, "images")
	os.Mkdir(subDir, 0755)
	if err := os.WriteFile(filepath.Join(subDir, "img.png"), []byte("fake image"), 0644); err != nil {
		t.Fatalf("failed to create dummy image: %v", err)
	}

	return dir, func() {
		os.RemoveAll(dir)
	}
}

func TestExport_HappyPath(t *testing.T) {
	// Setup
	database, queries, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, queries, uploadsDir, logger)
	ctx := context.Background()

	// Seed Data
	queries.InitSettings(ctx)
	_, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "admin@example.com",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	// Execute Export
	var buf bytes.Buffer
	err = svc.Export(ctx, &buf)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify ZIP Content
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read generated zip: %v", err)
	}

	// Check for expected files
	files := make(map[string]bool)
	for _, f := range zr.File {
		files[f.Name] = true
	}

	if !files["restore_data.json"] {
		t.Error("restore_data.json missing from backup")
	}
	if !files["human_readable_parts.csv"] {
		t.Error("human_readable_parts.csv missing from backup")
	}

	if !files["uploads/test.txt"] {
		t.Error("uploads/test.txt missing from backup")
	}
	if !files["uploads/images/img.png"] {
		t.Error("uploads/images/img.png missing from backup")
	}

	// Verify JSON Content
	rc, _ := zr.Open("restore_data.json")
	defer rc.Close()
	var manifest Manifest
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		t.Fatalf("failed to decode manifest: %v", err)
	}

	if len(manifest.Users) != 1 {
		t.Errorf("expected 1 user in manifest, got %d", len(manifest.Users))
	}
	if manifest.Users[0].Email != "admin@example.com" {
		t.Errorf("expected user email 'admin@example.com', got %s", manifest.Users[0].Email)
	}
}

func TestRestore_HappyPath(t *testing.T) {
	// Setup
	database, queries, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	// the "Live" directory that gets replaced
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, queries, uploadsDir, logger)
	ctx := context.Background()

	// Create a "Backup" to restore
	// create a new zip in memory that represents a backup
	// containing a DIFFERENT user and DIFFERENT file.
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Manifest with 1 new user
	manifest := Manifest{
		Version: "1.0",
		Users: []db.User{
			{
				ID:           99,
				Email:        "restored@example.com",
				PasswordHash: "newhash",
				Role:         "admin",
				CreatedAt:    sql.NullTime{Time: time.Now(), Valid: true},
			},
		},
		Settings: db.Setting{
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
			UpdatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		},
	}
	fJson, _ := zw.Create("restore_data.json")
	json.NewEncoder(fJson).Encode(manifest)

	// File: "uploads/restored_file.txt"
	fFile, _ := zw.Create("uploads/restored_file.txt")
	fFile.Write([]byte("I am new here"))

	zw.Close()
	zipBytes := buf.Bytes()

	// Pre-state Check
	// DB is empty (setupTestDB doesn't seed)
	// uploadsDir has "test.txt" (from setupTestUploads)

	// Execute Restore
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verification

	// DB: Should have the restored user
	u, err := queries.GetUserByEmail(ctx, "restored@example.com")
	if err != nil {
		t.Fatalf("failed to find restored user: %v", err)
	}
	if u.ID != 99 {
		t.Errorf("expected restored user ID 99, got %d", u.ID)
	}

	// FS: Should have "restored_file.txt" and NOT "test.txt"
	if _, err := os.Stat(filepath.Join(uploadsDir, "restored_file.txt")); os.IsNotExist(err) {
		t.Error("restored file missing from uploads dir")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); err == nil {
		t.Error("old file 'test.txt' still exists in uploads dir (should be gone)")
	}
}

func TestRestore_RollbackOnDBError(t *testing.T) {
	// Setup
	database, queries, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, queries, uploadsDir, logger)
	ctx := context.Background()

	// Seed initial DB state
	queries.InitSettings(ctx)

	// Create a "Bad" Backup
	// Manifest contains a PartAssignment for a Part that doesn't exist.
	// This should trigger a Foreign Key violation during restore.
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	manifest := Manifest{
		Version: "1.0",
		Settings: db.Setting{
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
			UpdatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		},
		PartAssignments: []db.PartAssignment{
			{
				ID: 1, PartID: 9999, Quantity: 10, // Part 9999 does not exist
			},
		},
	}
	fJson, _ := zw.Create("restore_data.json")
	json.NewEncoder(fJson).Encode(manifest)

	// Add a file - this should NOT end up in uploads if rollback works
	fFile, _ := zw.Create("uploads/should_not_exist.txt")
	fFile.Write([]byte("bad"))

	zw.Close()
	zipBytes := buf.Bytes()

	// Execute Restore
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err == nil {
		t.Fatal("Expected error during restore due to FK violation, got nil")
	}

	// Verify Rollback

	s, err := queries.GetSettings(ctx)
	if err != nil {
		t.Errorf("Settings missing after rollback: %v", err)
	}
	if s.RequireAuthForRead.Bool {
		// Default is false/null, assuming InitSettings sets defaults.
	}

	// FS: "test.txt" should still exist, "should_not_exist.txt" should NOT exist.
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); os.IsNotExist(err) {
		t.Error("original file 'test.txt' missing after rollback")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "should_not_exist.txt")); err == nil {
		t.Error("new file 'should_not_exist.txt' exists despite rollback")
	}
}
