package backup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/maintenance"
)

const (
	mountRecreatePhaseEnv = "WLEDGER_TEST_MOUNT_RECREATE_PHASE"
	mountRecreatePointEnv = "WLEDGER_TEST_MOUNT_RECREATE_POINT"
)

// TestRestoreMountRecreateHelper is the in-container half of
// scripts/test-restore-mount-recreate.sh. It is a no-op unless
// WLEDGER_TEST_MOUNT_RECREATE_PHASE is set. The script runs it twice in two
// separate containers over the same persistent volumes: once ("crash") to
// perform a restore that is killed at a failpoint, and once ("recover") in a
// freshly created container to run startup recovery and assert the outcome.
//
// Because the second container has a fresh container filesystem, any recovery
// metadata kept outside the persistent volumes (the uploads bind mount and the
// data bind mount) is lost. The test therefore fails unless the recovery design
// keeps its metadata inside the persistent uploads mount.
func TestRestoreMountRecreateHelper(t *testing.T) {
	phase := os.Getenv(mountRecreatePhaseEnv)
	if phase == "" {
		t.Skip("set WLEDGER_TEST_MOUNT_RECREATE_PHASE to run the container-recreation helper")
	}
	uploadsDir := os.Getenv("WLEDGER_TEST_RESTORE_UPLOADS")
	databasePath := os.Getenv("WLEDGER_TEST_RESTORE_DB")
	archivePath := os.Getenv("WLEDGER_TEST_RESTORE_ARCHIVE")
	point := os.Getenv(mountRecreatePointEnv)
	if uploadsDir == "" || databasePath == "" {
		t.Fatal("WLEDGER_TEST_RESTORE_UPLOADS and WLEDGER_TEST_RESTORE_DB are required")
	}
	if !isMountPoint(uploadsDir) {
		t.Fatalf("%s is not a mount point; the container recreation test requires a real bind mount", uploadsDir)
	}

	database, err := db.Open("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(database)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	switch phase {
	case "crash":
		if err := store.InitSettings(ctx); err != nil {
			t.Fatal(err)
		}
		// Two old entries so a partial drain is genuinely partial.
		if err := os.WriteFile(filepath.Join(uploadsDir, "old.txt"), []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(uploadsDir, "images"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(uploadsDir, "images", "old.png"), []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
		archive := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new", "images/new.png": "new"})
		if err := os.WriteFile(archivePath, archive, 0600); err != nil {
			t.Fatal(err)
		}

		crash := func() {
			if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
				os.Exit(70)
			}
			select {}
		}
		if point == "commit" {
			afterRestoreTransactionCommit = crash
		} else {
			mountActivationFailpoint = func(p string) {
				if p == point {
					crash()
				}
			}
		}

		// Signal the harness that the restore is about to run, then run it. The
		// process is killed at the failpoint, so this call never returns.
		if err := os.WriteFile(filepath.Join(filepath.Dir(databasePath), ".crash-ready"), []byte(point), 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("MOUNT_RECREATE_CRASH_READY point=%s\n", point)
		os.Stdout.Sync()

		f, err := os.Open(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		svc := NewService(database, store, uploadsDir, logger)
		if err := svc.Restore(ctx, f, info.Size()); err != nil {
			t.Fatalf("restore returned instead of crashing at %s: %v", point, err)
		}
		t.Fatalf("restore completed instead of crashing at %s", point)

	case "recover":
		if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), logger); err != nil {
			t.Fatalf("startup recovery failed: %v", err)
		}
		if point == "commit" {
			if _, err := store.GetUserByEmail(ctx, "restored@example.com"); err != nil {
				t.Errorf("committed restore database was not kept: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(uploadsDir, "new.txt")); err != nil || string(got) != "new" {
				t.Errorf("committed new.txt missing: %q err=%v", got, err)
			}
			if _, err := os.Stat(filepath.Join(uploadsDir, "old.txt")); !os.IsNotExist(err) {
				t.Errorf("old.txt remains after committed recovery: %v", err)
			}
		} else {
			if _, err := store.GetUserByEmail(ctx, "restored@example.com"); err == nil {
				t.Error("uncommitted restore database became active")
			}
			if got, err := os.ReadFile(filepath.Join(uploadsDir, "old.txt")); err != nil || string(got) != "old" {
				t.Errorf("old.txt not restored: %q err=%v", got, err)
			}
			if got, err := os.ReadFile(filepath.Join(uploadsDir, "images", "old.png")); err != nil || string(got) != "old" {
				t.Errorf("images/old.png not restored: %q err=%v", got, err)
			}
			if _, err := os.Stat(filepath.Join(uploadsDir, "new.txt")); !os.IsNotExist(err) {
				t.Errorf("new.txt remains after rollback: %v", err)
			}
		}
		// No recovery artifact may remain after recovery.
		for _, path := range maintenance.JournalPaths(uploadsDir) {
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("restore journal remains after recovery: %s", path)
			}
		}
		if _, err := os.Stat(maintenance.InMountControlDir(uploadsDir)); !os.IsNotExist(err) {
			t.Errorf("in-mount restore control directory remains after recovery: %v", err)
		}
		fmt.Printf("MOUNT_RECREATE_RECOVER_DONE point=%s\n", point)
		os.Stdout.Sync()

	default:
		t.Fatalf("unknown phase %q", phase)
	}
}
