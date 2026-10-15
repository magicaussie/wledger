package documents

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// TestUploadDocumentDoesNotCollide verifies that two documents sharing the same
// original filename uploaded in the same second are stored as distinct files.
//
// Previously the stored name was the sanitized basename plus a whole-second
// timestamp, written with a truncating os.Create. Two same-named uploads in one
// second therefore resolved to the same path and the second silently destroyed
// the first, while separate database records still referenced it.
func TestUploadDocumentDoesNotCollide(t *testing.T) {
	t.Chdir(t.TempDir())

	_, s, cleanup := setupTestDB(t)
	defer cleanup()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	svc := NewService(s, logger)
	ctx := context.Background()

	partID, err := s.CreatePart(ctx, db.CreatePartParams{Name: "Collision Part"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}

	pathA, err := svc.UploadDocument(ctx, s, partID, strings.NewReader("first"), "datasheet.pdf")
	if err != nil {
		t.Fatalf("first upload: %v", err)
	}
	pathB, err := svc.UploadDocument(ctx, s, partID, strings.NewReader("second"), "datasheet.pdf")
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if pathA == pathB {
		t.Fatalf("same-name uploads collided: both stored at %q", pathA)
	}

	read := func(webPath string) string {
		disk := filepath.Join("app", "uploads", strings.TrimPrefix(webPath, "/uploads/"))
		b, err := os.ReadFile(disk)
		if err != nil {
			t.Fatalf("read %s: %v", disk, err)
		}
		return string(b)
	}
	if got := read(pathA); got != "first" {
		t.Errorf("first document bytes = %q, want %q", got, "first")
	}
	if got := read(pathB); got != "second" {
		t.Errorf("second document bytes = %q, want %q", got, "second")
	}
}

func TestDocumentTransactionRequiresPreTransactionClaim(t *testing.T) {
	t.Chdir(t.TempDir())
	_, store, cleanup := setupTestDB(t)
	defer cleanup()
	svc := NewService(store, slog.Default())
	ctx := context.Background()
	id, err := store.CreatePart(ctx, db.CreatePartParams{Name: "Claim test"})
	if err != nil {
		t.Fatal(err)
	}
	err = store.ExecTx(ctx, func(q db.Querier) error {
		_, err := svc.UploadDocument(ctx, q, id, strings.NewReader("must not write"), "unsafe.txt")
		if err == nil || !strings.Contains(err.Error(), "enclosing upload reference claim") {
			t.Fatalf("unclaimed transaction accepted: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("app/uploads/docs"); !os.IsNotExist(err) {
		t.Fatalf("unclaimed upload touched filesystem: %v", err)
	}
}
