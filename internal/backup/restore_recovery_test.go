package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stagingDirFromPath returns the ".restore_tmp_*" directory component of a path,
// or "" if the path is not inside a staging directory.
func stagingDirFromPath(p string) string {
	parts := strings.Split(p, string(filepath.Separator))
	for i, part := range parts {
		if isStagingDir(part) {
			return strings.Join(parts[:i+1], string(filepath.Separator))
		}
	}
	return ""
}

// writeRecoveryDir creates a ".uploads_bak_*" recovery directory containing a
// file, simulating a previous restore whose filesystem rollback failed.
func writeRecoveryDir(t *testing.T, uploadsDir, name, file, content string) string {
	t.Helper()
	dir := filepath.Join(uploadsDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("create recovery dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0644); err != nil {
		t.Fatalf("write recovery file: %v", err)
	}
	return dir
}

// TestRestore_PreservesRecoveryDirectoryAcrossRestores verifies that a recovery
// directory left by a failed rollback survives a subsequent successful restore.
// Before the fix, a later restore deleted it (via cleanup and via capturing it
// into the backup directory that is removed on success).
func TestRestore_PreservesRecoveryDirectoryAcrossRestores(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	recovery := writeRecoveryDir(t, uploadsDir, ".uploads_bak_111", "old.txt", "precious")

	zipBytes := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(recovery, "old.txt"))
	if err != nil {
		t.Fatalf("recovery file was deleted by a later restore: %v", err)
	}
	if string(got) != "precious" {
		t.Errorf("recovery file content = %q, want %q", got, "precious")
	}
	// The restore still did its own job and cleaned up its own staging.
	if _, err := os.Stat(filepath.Join(uploadsDir, "new.txt")); os.IsNotExist(err) {
		t.Error("restored upload missing")
	}
	entries, _ := os.ReadDir(uploadsDir)
	for _, e := range entries {
		if isStagingDir(e.Name()) {
			t.Errorf("leftover staging directory after successful restore: %s", e.Name())
		}
	}
}

// TestRestore_CleanupRemovesAbandonedStagingButNotRecovery verifies that cleanup
// distinguishes an abandoned extraction staging directory (removed) from a
// recovery directory (preserved).
func TestRestore_CleanupRemovesAbandonedStagingButNotRecovery(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	abandoned := filepath.Join(uploadsDir, ".restore_tmp_999")
	if err := os.MkdirAll(abandoned, 0755); err != nil {
		t.Fatalf("create abandoned staging dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(abandoned, "leftover.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("write abandoned file: %v", err)
	}
	recovery := writeRecoveryDir(t, uploadsDir, ".uploads_bak_999", "old.txt", "keep")

	zipBytes := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("abandoned staging directory was not cleaned up: %v", err)
	}
	if _, err := os.Stat(filepath.Join(recovery, "old.txt")); err != nil {
		t.Errorf("recovery directory was removed by cleanup: %v", err)
	}
}

// TestRestore_RestartPreservesRecoveryData verifies that recovery data survives a
// service restart (a new service instance over the same uploads directory).
func TestRestore_RestartPreservesRecoveryData(t *testing.T) {
	database, s, _, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	recovery := writeRecoveryDir(t, uploadsDir, ".uploads_bak_222", "old.txt", "precious")

	// Simulate a process restart: a brand new service over the same directory.
	restarted := NewService(database, s, uploadsDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	zipBytes := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	if err := restarted.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore after restart failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(recovery, "old.txt")); err != nil {
		t.Errorf("recovery data was lost across a restart: %v", err)
	}
}

// TestRestore_FailedRollbackThenLaterRestorePreservesRecovery verifies that the
// only recoverable copy of the previous uploads survives a subsequent restore
// after a failed swap and rollback.
func TestRestore_FailedRollbackThenLaterRestorePreservesRecovery(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	orig := renameEntry
	defer func() { renameEntry = orig }()
	renameEntry = func(src, dst string) error {
		if strings.Contains(src, ".restore_tmp_") {
			return errors.New("injected swap failure")
		}
		if strings.Contains(src, ".uploads_bak_") {
			return errors.New("injected rollback failure")
		}
		return os.Rename(src, dst)
	}

	zipBytes := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err == nil {
		t.Fatal("expected the first restore to fail")
	}
	if findFile(t, uploadsDir, "test.txt") == "" {
		t.Fatal("previous upload was not preserved by the failed rollback")
	}

	// A later, successful restore must not destroy the preserved uploads.
	renameEntry = orig
	zipBytes2 := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new2.txt": "new2"})
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes2), int64(len(zipBytes2))); err != nil {
		t.Fatalf("second restore failed: %v", err)
	}
	if findFile(t, uploadsDir, "test.txt") == "" {
		t.Error("a later restore destroyed the only recoverable copy of the previous uploads")
	}
}

// TestRestore_ConcurrentRestoresDoNotDeleteEachOthersStaging verifies that
// restores are serialized: a second restore cannot delete the staging directory
// of a restore that is still running.
func TestRestore_ConcurrentRestoresDoNotDeleteEachOthersStaging(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	orig := renameEntry
	defer func() { renameEntry = orig }()

	release := make(chan struct{})
	staged := make(chan string, 1)
	var once sync.Once
	renameEntry = func(src, dst string) error {
		if dir := stagingDirFromPath(src); dir != "" {
			once.Do(func() {
				staged <- dir
				<-release
			})
		}
		return os.Rename(src, dst)
	}

	zipA := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"a.txt": "a"})
	errA := make(chan error, 1)
	go func() { errA <- svc.Restore(ctx, bytes.NewReader(zipA), int64(len(zipA))) }()

	var stagingDir string
	select {
	case stagingDir = <-staged:
	case <-time.After(5 * time.Second):
		t.Fatal("first restore did not reach its staging swap")
	}

	// Start a second restore while the first is paused mid-swap. If restores were
	// not serialized, its cleanup would delete the first restore's staging dir.
	zipB := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"b.txt": "b"})
	errB := make(chan error, 1)
	go func() { errB <- svc.Restore(ctx, bytes.NewReader(zipB), int64(len(zipB))) }()

	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(stagingDir); err != nil {
		t.Fatalf("concurrent restore removed another restore's staging directory: %v", err)
	}

	close(release)
	if err := <-errA; err != nil {
		t.Fatalf("first restore failed: %v", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("second restore failed: %v", err)
	}

	entries, _ := os.ReadDir(uploadsDir)
	for _, e := range entries {
		if isStagingDir(e.Name()) {
			t.Errorf("leftover staging directory after concurrent restores: %s", e.Name())
		}
	}
}

// TestWithinDir verifies the containment helper used to block ZIP path traversal,
// including the sibling-prefix case a raw prefix comparison would miss.
func TestWithinDir(t *testing.T) {
	base := filepath.Join(string(filepath.Separator), "u", ".restore_tmp_1")
	cases := []struct {
		name   string
		target string
		want   bool
	}{
		{"descendant", filepath.Join(base, "a", "b.txt"), true},
		{"base itself", base, true},
		{"sibling prefix", base + "evil" + string(filepath.Separator) + "x", false},
		{"parent escape", filepath.Join(base, "..", "escaped.txt"), false},
		{"absolute", filepath.Join(string(filepath.Separator), "etc", "passwd"), false},
	}
	for _, tc := range cases {
		if got := withinDir(base, tc.target); got != tc.want {
			t.Errorf("%s: withinDir(%q, %q) = %v, want %v", tc.name, base, tc.target, got, tc.want)
		}
	}
}

// TestRestore_RejectsZipPathTraversal verifies a traversal entry is skipped while
// a legitimate entry is still extracted.
func TestRestore_RejectsZipPathTraversal(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	zipBytes := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{
		"../escaped.txt": "pwn",
		"ok.txt":         "ok",
	})
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "escaped.txt")); !os.IsNotExist(err) {
		t.Errorf("path traversal was not blocked: escaped.txt exists in %s", uploadsDir)
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "ok.txt")); err != nil {
		t.Errorf("legitimate upload missing: %v", err)
	}
}
