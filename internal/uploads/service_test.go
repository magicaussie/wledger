package uploads

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open("file:" + filepath.Join(dir, "wledger.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	uploadsDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(filepath.Join(uploadsDir, "images"), 0o755); err != nil {
		t.Fatalf("create images dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(uploadsDir, "docs"), 0o755); err != nil {
		t.Fatalf("create docs dir: %v", err)
	}
	return NewService(conn, uploadsDir), uploadsDir
}

func writeUpload(t *testing.T, uploadsDir, rel, content string) {
	t.Helper()
	full := filepath.Join(uploadsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create upload dir for %q: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write upload %q: %v", rel, err)
	}
}

func orphanPaths(report Report) []string {
	paths := make([]string, 0, len(report.Orphans))
	for _, orphan := range report.Orphans {
		paths = append(paths, orphan.RelPath)
	}
	return paths
}

func TestScanReportsOnlyUnreferencedFiles(t *testing.T) {
	svc, uploadsDir := newTestService(t)
	ctx := context.Background()

	// Referenced assets: a part image (with its derived thumbnail), a category
	// image and a part document.
	if _, err := svc.db.ExecContext(ctx, `INSERT INTO parts (name, image_path) VALUES ('Part', '/uploads/images/part_1.jpg')`); err != nil {
		t.Fatalf("insert part: %v", err)
	}
	if _, err := svc.db.ExecContext(ctx, `INSERT INTO categories (name, image_path) VALUES ('Cat', '/uploads/images/category_1.jpg')`); err != nil {
		t.Fatalf("insert category: %v", err)
	}
	if _, err := svc.db.ExecContext(ctx, `INSERT INTO part_docs (part_id, file_path, file_name) VALUES (1, '/uploads/docs/report_ab.pdf', 'report.pdf')`); err != nil {
		t.Fatalf("insert doc: %v", err)
	}

	writeUpload(t, uploadsDir, "images/part_1.jpg", "referenced")
	writeUpload(t, uploadsDir, "images/part_1_thumb.jpg", "referenced thumbnail")
	writeUpload(t, uploadsDir, "images/category_1.jpg", "referenced")
	writeUpload(t, uploadsDir, "images/category_1_thumb.jpg", "referenced thumbnail")
	writeUpload(t, uploadsDir, "docs/report_ab.pdf", "referenced")
	writeUpload(t, uploadsDir, "images/part_orphan.jpg", "orphan")
	writeUpload(t, uploadsDir, "images/part_orphan_thumb.jpg", "orphan thumbnail")
	writeUpload(t, uploadsDir, "docs/stale.pdf", "orphan")
	// Hidden control-plane entries must never be reported.
	writeUpload(t, uploadsDir, ".uploads_bak_old/precious.txt", "operator recovery data")
	writeUpload(t, uploadsDir, ".wledger-restore/journal.json", "restore control")

	report, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := orphanPaths(report)
	want := []string{"docs/stale.pdf", "images/part_orphan.jpg", "images/part_orphan_thumb.jpg"}
	if len(got) != len(want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("orphans = %v, want %v", got, want)
		}
	}
	if report.Referenced != 5 {
		t.Errorf("referenced = %d, want 5", report.Referenced)
	}
}

func TestDeleteRemovesOnlyConfirmedOrphans(t *testing.T) {
	svc, uploadsDir := newTestService(t)
	ctx := context.Background()

	if _, err := svc.db.ExecContext(ctx, `INSERT INTO parts (name, image_path) VALUES ('Part', '/uploads/images/part_1.jpg')`); err != nil {
		t.Fatalf("insert part: %v", err)
	}
	writeUpload(t, uploadsDir, "images/part_1.jpg", "referenced")
	writeUpload(t, uploadsDir, "images/part_1_thumb.jpg", "referenced thumbnail")
	writeUpload(t, uploadsDir, "images/orphan.jpg", "orphan")

	// A referenced file must never be deleted even if requested, and a path that
	// escapes the uploads directory must be ignored.
	deleted, err := svc.Delete(ctx, []string{"images/orphan.jpg", "images/part_1.jpg", "../escape.txt", "images/missing.jpg"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "images/orphan.jpg" {
		t.Fatalf("deleted = %v, want [images/orphan.jpg]", deleted)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "images", "orphan.jpg")); !os.IsNotExist(err) {
		t.Errorf("orphan file still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "images", "part_1.jpg")); err != nil {
		t.Errorf("referenced file was removed: %v", err)
	}
}

func TestDeleteRechecksReferencesBeforeRemoving(t *testing.T) {
	svc, uploadsDir := newTestService(t)
	ctx := context.Background()

	writeUpload(t, uploadsDir, "images/soon_referenced.jpg", "content")
	report, err := svc.Scan(ctx)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(report.Orphans) != 1 {
		t.Fatalf("expected one orphan, got %v", orphanPaths(report))
	}

	// The file becomes referenced between the scan and the delete.
	if _, err := svc.db.ExecContext(ctx, `INSERT INTO parts (name, image_path) VALUES ('Part', '/uploads/images/soon_referenced.jpg')`); err != nil {
		t.Fatalf("insert part: %v", err)
	}

	deleted, err := svc.Delete(ctx, []string{"images/soon_referenced.jpg"})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("deleted = %v, want none (file became referenced)", deleted)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "images", "soon_referenced.jpg")); err != nil {
		t.Errorf("newly referenced file was removed: %v", err)
	}
}

func TestScanMissingUploadsDirectoryIsEmpty(t *testing.T) {
	svc, uploadsDir := newTestService(t)
	if err := os.RemoveAll(uploadsDir); err != nil {
		t.Fatalf("remove uploads dir: %v", err)
	}
	report, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(report.Orphans) != 0 {
		t.Fatalf("orphans = %v, want none", orphanPaths(report))
	}
}
