package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/maintenance"
)

// The mount-aware activation protocol is normally selected only when the uploads
// path is a mount point. uploadsDirIsMountPoint is overridden in these tests so
// the protocol can be exercised deterministically on ordinary temporary
// directories, and the real-bind-mount test at the end of this file exercises the
// genuine mount detection and the original EBUSY failure.
func forceMountMode(t *testing.T) {
	t.Helper()
	orig := uploadsDirIsMountPoint
	uploadsDirIsMountPoint = func(string) bool { return true }
	t.Cleanup(func() { uploadsDirIsMountPoint = orig })
}

func mountPaths(uploadsDir, id string) (control, candidate, previous string) {
	return mountGenerationPaths(uploadsDir, id)
}

// writeJournal durably writes a restore journal to the location the protocol
// selects for this uploads directory (inside the mount in mount mode), creating
// its control directory first (in production the control directory is created
// before extraction).
func writeJournal(t *testing.T, uploadsDir string, record restoreJournal) {
	t.Helper()
	journal := journalPath(uploadsDir)
	if err := os.MkdirAll(filepath.Dir(journal), 0755); err != nil {
		t.Fatalf("create journal control directory: %v", err)
	}
	if err := writeRestoreJournal(uploadsDir, record); err != nil {
		t.Fatalf("write restore journal: %v", err)
	}
}

// mustNoJournal asserts that no restore journal remains in any durable location.
func mustNoJournal(t *testing.T, uploadsDir string) {
	t.Helper()
	for _, path := range maintenance.JournalPaths(uploadsDir) {
		mustNotExist(t, path)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create parent of %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected %s to be absent, got err=%v", path, err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
}

// TestRestore_MountModeSuccessSwapsContentsWithoutRenamingMountPoint verifies a
// successful restore replaces the contents of a mounted uploads directory,
// leaves no staging/control artifacts behind, and commits the database.
func TestRestore_MountModeSuccessSwapsContentsWithoutRenamingMountPoint(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(uploadsDir, "images", "old.png"), "old")

	archive := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"images/new.png": "new", "new.txt": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(archive), int64(len(archive))); err != nil {
		t.Fatalf("mount-mode restore failed: %v", err)
	}

	if _, err := s.GetUserByEmail(ctx, "restored@example.com"); err != nil {
		t.Errorf("restored user missing: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "images", "new.png")); err != nil || string(got) != "new" {
		t.Errorf("restored image missing or wrong: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(uploadsDir, "images", "old.png"))
	mustNotExist(t, filepath.Join(uploadsDir, mountControlDirName))

	mustNoJournal(t, uploadsDir)
}

// TestRestore_MountModeInvalidBackupLeavesUploadsUntouched verifies an archive
// rejected before activation changes neither the database nor the mounted
// uploads contents.
func TestRestore_MountModeInvalidBackupLeavesUploadsUntouched(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(uploadsDir, "old.txt"), "old")

	manifest := Manifest{
		Version:  "1.0",
		Settings: minimalSettings(),
		PartAssignments: []db.PartAssignment{
			{ID: 1, PartID: 9999, Quantity: 1},
		},
	}
	archive := buildRestoreZipWithFiles(t, manifest, map[string]string{"new.txt": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("expected restore to fail on FK violation")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "old.txt")); err != nil {
		t.Errorf("original upload missing after rejected restore: %v", err)
	}
	mustNotExist(t, filepath.Join(uploadsDir, "new.txt"))
	mustNotExist(t, filepath.Join(uploadsDir, mountControlDirName))
}

// TestRestore_MountModeInjectedActivationFailureRollsBack verifies that when
// moving the candidate into the live namespace fails mid-activation, the mounted
// uploads directory is rolled back to the previous contents and the database
// transaction does not commit.
func TestRestore_MountModeInjectedActivationFailureRollsBack(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(uploadsDir, "images", "old.png"), "old")

	orig := renameEntry
	var once sync.Once
	renameEntry = func(src, dst string) error {
		if strings.Contains(src, ".candidate") {
			var injected error
			once.Do(func() { injected = errors.New("injected activation failure") })
			if injected != nil {
				return injected
			}
		}
		return os.Rename(src, dst)
	}
	defer func() { renameEntry = orig }()

	archive := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"images/new.png": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(archive), int64(len(archive))); err == nil {
		t.Fatal("expected activation to fail")
	}

	if _, err := s.GetUserByEmail(ctx, "restored@example.com"); err == nil {
		t.Error("database committed despite failed activation")
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "images", "old.png")); err != nil || string(got) != "old" {
		t.Errorf("previous upload not restored after rollback: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(uploadsDir, "images", "new.png"))
	mustNotExist(t, filepath.Join(uploadsDir, mountControlDirName))
}

// TestRestore_MountModePreservesRecoveryDataAndCleansStaging verifies that a
// hidden operator recovery directory survives a mounted restore while abandoned
// extraction staging directories are removed.
func TestRestore_MountModePreservesRecoveryDataAndCleansStaging(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(uploadsDir, ".uploads_bak_111", "precious.txt"), "precious")
	writeFile(t, filepath.Join(uploadsDir, ".restore_tmp_999", "leftover.txt"), "x")

	archive := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(archive), int64(len(archive))); err != nil {
		t.Fatalf("mount-mode restore failed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, ".uploads_bak_111", "precious.txt")); err != nil || string(got) != "precious" {
		t.Errorf("operator recovery data lost: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(uploadsDir, ".restore_tmp_999"))
	mustExist(t, filepath.Join(uploadsDir, "new.txt"))
}

// TestRestore_MountModeRecoversInterruptedDrain reconstructs the on-disk state
// produced by a process killed during the drain phase (before the durable drain
// marker) and verifies startup recovery restores the previous generation.
func TestRestore_MountModeRecoversInterruptedDrain(t *testing.T) {
	database, s, _, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	writeFile(t, filepath.Join(uploadsDir, "images", "old.png"), "old")
	control, candidate, previous := mountPaths(uploadsDir, id)
	// Drain partially completed: old.txt moved aside, images/old.png not yet.
	writeFile(t, filepath.Join(previous, "old.txt"), "old")
	writeFile(t, filepath.Join(candidate, "images", "new.png"), "new")
	writeFile(t, filepath.Join(candidate, "new.txt"), "new")
	writeJournal(t, uploadsDir, restoreJournal{Version: 1, ID: id, HadUploads: true})

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("startup recovery failed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "old.txt")); err != nil || string(got) != "old" {
		t.Errorf("old.txt not restored: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "images", "old.png")); err != nil || string(got) != "old" {
		t.Errorf("images/old.png not restored: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(uploadsDir, "new.txt"))
	mustNotExist(t, filepath.Join(uploadsDir, "images", "new.png"))
	mustNotExist(t, filepath.Join(control, id+".candidate"))
	mustNotExist(t, filepath.Join(control, id+".previous"))
	mustNoJournal(t, uploadsDir)
}

// TestRestore_MountModeRecoversInterruptedFill reconstructs a process killed
// during the fill phase (after the durable drain marker, with some candidate
// entries already active) and verifies startup recovery restores the previous
// generation exactly.
func TestRestore_MountModeRecoversInterruptedFill(t *testing.T) {
	database, s, _, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	const id = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	control, candidate, previous := mountPaths(uploadsDir, id)
	// Drain completed: the previous generation holds both old entries and the
	// durable marker. Fill started: images/new.png is already active, new.txt is
	// still staged in the candidate.
	writeFile(t, filepath.Join(previous, "old.txt"), "old")
	writeFile(t, filepath.Join(previous, "images", "old.png"), "old")
	if err := writeMountDrainedMarker(previous); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(uploadsDir, "images", "new.png"), "new")
	writeFile(t, filepath.Join(candidate, "new.txt"), "new")
	writeJournal(t, uploadsDir, restoreJournal{Version: 1, ID: id, HadUploads: true})

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("startup recovery failed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "old.txt")); err != nil || string(got) != "old" {
		t.Errorf("old.txt not restored: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "images", "old.png")); err != nil || string(got) != "old" {
		t.Errorf("images/old.png not restored: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(uploadsDir, "new.txt"))
	mustNotExist(t, filepath.Join(uploadsDir, "images", "new.png"))
	mustNotExist(t, filepath.Join(control, id+".candidate"))
	mustNotExist(t, filepath.Join(control, id+".previous"))
}

// TestRestore_MountModeRollsForwardCommittedRestore reconstructs a process killed
// after the database committed (the candidate is already active) and verifies
// startup recovery keeps the new generation, retires the journal and clears the
// replaced generation.
func TestRestore_MountModeRollsForwardCommittedRestore(t *testing.T) {
	database, s, _, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	const id = "cccccccccccccccccccccccccccccccc"

	control, _, previous := mountPaths(uploadsDir, id)
	writeFile(t, filepath.Join(uploadsDir, "new.txt"), "new")
	writeFile(t, filepath.Join(previous, "old.txt"), "old")
	if err := writeMountDrainedMarker(previous); err != nil {
		t.Fatal(err)
	}
	writeJournal(t, uploadsDir, restoreJournal{Version: 1, ID: id, HadUploads: true})
	if err := s.SetRestoreGeneration(ctx, id); err != nil {
		t.Fatal(err)
	}

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("startup recovery failed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "new.txt")); err != nil || string(got) != "new" {
		t.Errorf("committed generation lost: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(previous, "old.txt"))
	mustNoJournal(t, uploadsDir)
	mustNotExist(t, control)
}

// TestRestore_MountModeRestoresAndExportsCategoryPartAndDocAssets verifies that
// restored category images, part images and documents are physically present and
// readable after a mounted restore, and that a subsequent export still contains
// them.
func TestRestore_MountModeRestoresAndExportsCategoryPartAndDocAssets(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}

	manifest := restoreUserManifest()
	manifest.Parts = []db.Part{{
		ID: 1, Name: "Widget",
		ImagePath: sql.NullString{String: "/uploads/images/part.png", Valid: true},
	}}
	manifest.PartDocs = []db.PartDoc{{ID: 1, PartID: 1, FilePath: "/uploads/docs/spec.pdf", FileName: "spec.pdf"}}
	manifest.Categories = []db.Category{{ID: 1, Name: "Hardware", ImagePath: "/uploads/images/category.png"}}
	archive := buildRestoreZipWithFiles(t, manifest, map[string]string{
		"images/part.png":     "part-bytes",
		"images/category.png": "category-bytes",
		"docs/spec.pdf":       "pdf-bytes",
	})
	if err := svc.Restore(ctx, bytes.NewReader(archive), int64(len(archive))); err != nil {
		t.Fatalf("mount-mode restore failed: %v", err)
	}

	for path, want := range map[string]string{
		"images/part.png":     "part-bytes",
		"images/category.png": "category-bytes",
		"docs/spec.pdf":       "pdf-bytes",
	} {
		got, err := os.ReadFile(filepath.Join(uploadsDir, filepath.FromSlash(path)))
		if err != nil || string(got) != want {
			t.Errorf("restored asset %s = %q err=%v, want %q", path, got, err, want)
		}
	}
	parts, err := s.GetAllParts(ctx)
	if err != nil || len(parts) != 1 || parts[0].ImagePath.String != "/uploads/images/part.png" {
		t.Errorf("restored part image reference = %+v err=%v", parts, err)
	}
	categories, err := s.GetAllCategories(ctx)
	if err != nil || len(categories) != 1 || categories[0].ImagePath != "/uploads/images/category.png" {
		t.Errorf("restored category image reference = %+v err=%v", categories, err)
	}
	docs, err := s.GetAllPartDocs(ctx)
	if err != nil || len(docs) != 1 || docs[0].FilePath != "/uploads/docs/spec.pdf" {
		t.Errorf("restored document reference = %+v err=%v", docs, err)
	}

	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf); err != nil {
		t.Fatalf("export after restore failed: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read exported archive: %v", err)
	}
	members := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		members[f.Name] = string(data)
	}
	for path, want := range map[string]string{
		"uploads/images/part.png":     "part-bytes",
		"uploads/images/category.png": "category-bytes",
		"uploads/docs/spec.pdf":       "pdf-bytes",
	} {
		if got, ok := members[path]; !ok || got != want {
			t.Errorf("exported archive member %s = %q present=%v, want %q", path, got, ok, want)
		}
	}
}

// TestRestore_MountModeMatchesNonMountModeOutcome runs the same archive through
// the mount-aware and the default directory-swap protocols and verifies the
// resulting active uploads namespace and database are identical.
func TestRestore_MountModeMatchesNonMountModeOutcome(t *testing.T) {
	archive := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{
		"images/a.png": "a",
		"docs/b.pdf":   "b",
	})

	run := func(mount bool) map[string]string {
		_, s, svc, uploadsDir, cleanup := newBackupService(t)
		defer cleanup()
		ctx := context.Background()
		if err := s.InitSettings(ctx); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(uploadsDir, "images", "z.png"), "z")
		if mount {
			forceMountMode(t)
		}
		if err := svc.Restore(ctx, bytes.NewReader(archive), int64(len(archive))); err != nil {
			t.Fatalf("restore (mount=%v) failed: %v", mount, err)
		}
		files := map[string]string{}
		_ = filepath.WalkDir(uploadsDir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			data, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(uploadsDir, p)
			files[rel] = string(data)
			return nil
		})
		return files
	}

	mountOutcome := run(true)
	plainOutcome := run(false)
	if len(mountOutcome) != len(plainOutcome) {
		t.Fatalf("mount outcome %v != non-mount outcome %v", mountOutcome, plainOutcome)
	}
	for k, v := range plainOutcome {
		if mountOutcome[k] != v {
			t.Errorf("mount outcome[%q] = %q, want %q", k, mountOutcome[k], v)
		}
	}
}

// TestRestore_RealBindMountPoint reproduces the original D1 failure — renaming
// the mounted uploads directory returns EBUSY — using a real mount, then proves
// the mount-aware restore succeeds on that same directory. It only runs when a
// genuinely bind-mounted uploads directory is provided via
// WLEDGER_TEST_MOUNT_UPLOADS (see scripts/test-restore-mount.sh, which creates
// the mount exactly as docker-compose.yaml does).
func TestRestore_RealBindMountPoint(t *testing.T) {
	uploadsDir := os.Getenv("WLEDGER_TEST_MOUNT_UPLOADS")
	if uploadsDir == "" {
		t.Skip("set WLEDGER_TEST_MOUNT_UPLOADS to a bind-mounted uploads directory to run this test")
	}
	abs, err := filepath.Abs(uploadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if !isMountPoint(abs) {
		t.Fatalf("%s is not a mount point; this test requires a real bind mount", abs)
	}

	// 1. Reproduce the original D1 failure: the default protocol renames the live
	// uploads directory aside, which fails with EBUSY on a mount point.
	sibling := filepath.Join(filepath.Dir(abs), "wledger-ebusy-probe")
	renameErr := os.Rename(abs, sibling)
	if renameErr == nil {
		_ = os.Rename(sibling, abs)
		t.Fatalf("renaming the uploads mount point unexpectedly succeeded; EBUSY not reproduced")
	}
	if !errors.Is(renameErr, syscall.EBUSY) {
		t.Fatalf("renaming the uploads mount point failed with %v, want EBUSY", renameErr)
	}
	t.Logf("reproduced D1: rename(%s) -> %v", abs, renameErr)

	// 2. The mount-aware restore must succeed on the very same directory.
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	svc := NewService(database, s, abs, slog.New(slog.NewTextHandler(io.Discard, nil)))
	writeFile(t, filepath.Join(abs, "images", "before.png"), "before")

	archive := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{
		"images/after.png": "after",
		"docs/spec.pdf":    "spec",
	})
	if err := svc.Restore(ctx, bytes.NewReader(archive), int64(len(archive))); err != nil {
		t.Fatalf("mount-aware restore failed on a real bind mount: %v", err)
	}

	if got, err := os.ReadFile(filepath.Join(abs, "images", "after.png")); err != nil || string(got) != "after" {
		t.Errorf("restored image missing or wrong: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(abs, "docs", "spec.pdf")); err != nil || string(got) != "spec" {
		t.Errorf("restored document missing or wrong: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(abs, "images", "before.png"))
	mustNotExist(t, filepath.Join(abs, mountControlDirName))
	// The mount point itself must still be present and mounted.
	if !isMountPoint(abs) {
		t.Errorf("uploads directory is no longer a mount point after restore")
	}
	if _, err := s.GetUserByEmail(ctx, "restored@example.com"); err != nil {
		t.Errorf("restored user missing: %v", err)
	}
}
