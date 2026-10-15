package uploads

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/categories"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/maintenance"
)

// The child owns an independent flock and SQLite connection, as another server
// would. Its reference is deliberately uncommitted when cleanup is attempted.
func TestReferenceWriterProcess(t *testing.T) {
	if os.Getenv("WLEDGER_UPLOAD_WRITER_TEST") != "1" {
		return
	}
	dir := os.Getenv("WLEDGER_UPLOAD_TEST_DIR")
	ctx, release, err := maintenance.UploadReferences(context.Background(), filepath.Join(dir, "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fmt.Println("CLAIMED")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Open("file:" + filepath.Join(dir, "wledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = categories.NewServiceWithUploadsDir(conn, filepath.Join(dir, "uploads")).Save(ctx, categories.Category{Name: "Concurrent category", ImagePath: "/uploads/images/race.jpg"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCleanupReferenceBetweenIdentificationAndDeletionCrossProcess(t *testing.T) {
	svc, dir := newTestService(t)
	writeUpload(t, dir, "images/race.jpg", "keep")
	writeUpload(t, dir, "images/race_thumb.jpg", "keep thumb")
	report, err := svc.Scan(context.Background())
	if err != nil || len(report.Orphans) != 2 {
		t.Fatalf("initial identification: %+v, %v", report, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReferenceWriterProcess$")
	cmd.Env = append(os.Environ(), "WLEDGER_UPLOAD_WRITER_TEST=1", "WLEDGER_UPLOAD_TEST_DIR="+filepath.Dir(dir))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "CLAIMED" {
		t.Fatalf("writer handshake: %q %v", line, err)
	}
	blockedCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	deleted, err := svc.Delete(blockedCtx, orphanPaths(report))
	stop()
	if !errors.Is(err, context.DeadlineExceeded) || len(deleted) != 0 {
		t.Fatalf("cleanup bypassed writer: %v %v", deleted, err)
	}
	// Let reference creation finish, strictly after the stale identification and
	// before the successful removal attempt. Delete must revalidate under EX.
	if _, err := fmt.Fprintln(stdin, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	deleted, err = svc.Delete(ctx, orphanPaths(report))
	if err != nil || len(deleted) != 0 {
		t.Fatalf("live file deleted: %v %v", deleted, err)
	}
	for _, rel := range orphanPaths(report) {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatal(err)
		}
	}
	var image string
	if err := svc.db.QueryRowContext(ctx, `SELECT image_path FROM categories WHERE name='Concurrent category'`).Scan(&image); err != nil || image != "/uploads/images/race.jpg" {
		t.Fatalf("reference: %q %v", image, err)
	}
	var integrity string
	if err := svc.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %q %v", integrity, err)
	}
}

func TestCleanupSkipsSymlinksAndRejectsClaimUpgrade(t *testing.T) {
	svc, dir := newTestService(t)
	outside := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "images", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	deleted, err := svc.Delete(context.Background(), []string{"images/link", "escape/precious", "../precious"})
	if err != nil || len(deleted) != 0 {
		t.Fatalf("symlinks: %v %v", deleted, err)
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "keep" {
		t.Fatalf("outside modified: %q %v", content, err)
	}
	ctx, release, err := maintenance.UploadReferences(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := svc.Delete(ctx, nil); err == nil {
		t.Fatal("shared-to-exclusive upgrade accepted")
	}
}

// A reference queued behind cleanup must not resurrect the now-missing path.
func TestCategoryReferenceAfterCleanupRejected(t *testing.T) {
	svc, dir := newTestService(t)
	writeUpload(t, dir, "images/gone.jpg", "obsolete")
	if deleted, err := svc.Delete(context.Background(), []string{"images/gone.jpg"}); err != nil || len(deleted) != 1 {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	_, err := categories.NewServiceWithUploadsDir(svc.db, dir).Save(context.Background(), categories.Category{Name: "Too late", ImagePath: "/uploads/images/gone.jpg"})
	if !errors.Is(err, categories.ErrInvalid) {
		t.Fatalf("missing image accepted: %v", err)
	}
	var count int
	if err := svc.db.QueryRow(`SELECT count(*) FROM categories WHERE name='Too late'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("dangling reference: %d %v", count, err)
	}
}
