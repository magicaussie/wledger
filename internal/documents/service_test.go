package documents

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
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

func TestDocumentService(t *testing.T) {
	// Setup templ dir to handle file uploads
	wd, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(wd)

	_, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.Background()

	// Create Part
	partID, err := s.CreatePart(ctx, db.CreatePartParams{Name: "Doc Part"})
	if err != nil {
		t.Fatalf("create part failed: %v", err)
	}

	// Test AddLink
	err = svc.AddLink(ctx, s, partID, "http://google.com", "Google")
	if err != nil {
		t.Fatalf("AddLink failed: %v", err)
	}

	links, err := s.GetPartLinks(ctx, partID)
	if err != nil {
		t.Fatalf("GetPartLinks failed: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("expected 1 link, got %d", len(links))
	} else if links[0].Url != "http://google.com" {
		t.Errorf("unexpected url: %s", links[0].Url)
	}

	// Test DeleteLink
	err = svc.DeleteLink(ctx, links[0].ID)
	if err != nil {
		t.Fatalf("DeleteLink failed: %v", err)
	}
	links, _ = s.GetPartLinks(ctx, partID)
	if len(links) != 0 {
		t.Errorf("expected 0 links, got %d", len(links))
	}

	// Test UploadDocument
	content := "hello world"
	reader := strings.NewReader(content)
	path, err := svc.UploadDocument(ctx, s, partID, reader, "test.txt")
	if err != nil {
		t.Fatalf("UploadDocument failed: %v", err)
	}

	// Verify file path prefix
	if !strings.HasPrefix(path, "/uploads/docs/") {
		t.Errorf("unexpected path prefix: %s", path)
	}

	// Verify DB
	docs, err := s.GetPartDocs(ctx, partID)
	if err != nil {
		t.Fatalf("GetPartDocs failed: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].FilePath != path {
		t.Errorf("db path mismatch: %s vs %s", docs[0].FilePath, path)
	}

	// Verify File on Disk
	// path is like /uploads/docs/test_123.txt
	// config.DirUploads is ./app/uploads
	// config.UrlPrefixUploads is /uploads/
	relPath := strings.TrimPrefix(path, "/uploads/") // docs/test_123.txt
	diskPath := filepath.Join("app/uploads", relPath)

	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		t.Errorf("file not found on disk: %s", diskPath)
	}

	// Test DeleteDocument
	err = svc.DeleteDocument(ctx, docs[0].ID)
	if err != nil {
		t.Fatalf("DeleteDocument failed: %v", err)
	}

	docs, _ = s.GetPartDocs(ctx, partID)
	if len(docs) != 0 {
		t.Errorf("expected 0 docs, got %d", len(docs))
	}

	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		t.Errorf("file should be deleted: %s", diskPath)
	}
}
