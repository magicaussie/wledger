package backup

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/maintenance"
)

const restoreCrashHelperEnv = "WLEDGER_TEST_RESTORE_CRASH_HELPER"

func TestRestoreCrashHelper(t *testing.T) {
	point := os.Getenv(restoreCrashHelperEnv)
	if point == "" {
		return
	}
	databasePath := os.Getenv("WLEDGER_TEST_RESTORE_DB")
	uploadsDir := os.Getenv("WLEDGER_TEST_RESTORE_UPLOADS")
	archivePath := os.Getenv("WLEDGER_TEST_RESTORE_ARCHIVE")
	database, err := db.Open("file:" + databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(database)
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	info, err := archive.Stat()
	if err != nil {
		t.Fatal(err)
	}

	crash := func(code int) {
		if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
			os.Exit(code)
		}
		select {}
	}
	if point != "after_commit" {
		originalRename := os.Rename
		renameEntry = func(src, dst string) error {
			if err := originalRename(src, dst); err != nil {
				return err
			}
			if point == "after_previous_rename" && strings.HasSuffix(dst, ".previous") {
				crash(71)
			}
			if point == "after_candidate_rename" && strings.HasSuffix(src, ".candidate") {
				crash(72)
			}
			return nil
		}
	} else {
		afterRestoreTransactionCommit = func() { crash(73) }
	}

	service := NewService(database, store, uploadsDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := service.Restore(context.Background(), archive, info.Size()); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRecoversActualProcessTerminationAtActivationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name          string
		point         string
		wantCommitted bool
	}{
		{name: "after old uploads renamed", point: "after_previous_rename"},
		{name: "after candidate uploads renamed", point: "after_candidate_rename"},
		{name: "after database commit", point: "after_commit", wantCommitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			databasePath := filepath.Join(root, "wledger.db")
			database, err := db.Open("file:" + databasePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(database); err != nil {
				t.Fatal(err)
			}
			store := db.NewStore(database)
			if err := store.InitSettings(context.Background()); err != nil {
				t.Fatal(err)
			}
			uploadsDir := filepath.Join(root, "app", "uploads")
			if err := os.MkdirAll(uploadsDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(uploadsDir, "old.txt"), []byte("old"), 0644); err != nil {
				t.Fatal(err)
			}
			archive := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
			archivePath := filepath.Join(root, "restore.zip")
			if err := os.WriteFile(archivePath, archive, 0600); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}

			command := exec.Command(os.Args[0], "-test.run=^TestRestoreCrashHelper$")
			command.Env = append(os.Environ(),
				restoreCrashHelperEnv+"="+tc.point,
				"WLEDGER_TEST_RESTORE_DB="+databasePath,
				"WLEDGER_TEST_RESTORE_UPLOADS="+uploadsDir,
				"WLEDGER_TEST_RESTORE_ARCHIVE="+archivePath,
			)
			if output, err := command.CombinedOutput(); err == nil {
				t.Fatalf("restore helper did not terminate at failpoint; output: %s", output)
			}

			restartedDB, err := db.Open("file:" + databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer restartedDB.Close()
			if err := db.Migrate(restartedDB); err != nil {
				t.Fatal(err)
			}
			gate := maintenance.New(uploadsDir)
			if err := RecoverStartup(context.Background(), restartedDB, uploadsDir, gate, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
				t.Fatalf("startup recovery failed: %v", err)
			}

			_, _, _, journal := generationPaths(uploadsDir, strings.Repeat("0", 32))
			if _, err := os.Stat(journal); !os.IsNotExist(err) {
				t.Errorf("restore journal remains after recovery: %v", err)
			}
			if tc.wantCommitted {
				if _, err := db.NewStore(restartedDB).GetUserByEmail(context.Background(), "restored@example.com"); err != nil {
					t.Errorf("committed restore database was not kept: %v", err)
				}
				if _, err := os.Stat(filepath.Join(uploadsDir, "new.txt")); err != nil {
					t.Errorf("committed candidate uploads were not completed: %v", err)
				}
				if _, err := os.Stat(filepath.Join(uploadsDir, "old.txt")); !os.IsNotExist(err) {
					t.Errorf("old upload remains active after committed recovery: %v", err)
				}
			} else {
				if _, err := db.NewStore(restartedDB).GetUserByEmail(context.Background(), "restored@example.com"); err == nil {
					t.Error("uncommitted restore database became active")
				}
				if _, err := os.Stat(filepath.Join(uploadsDir, "old.txt")); err != nil {
					t.Errorf("old generation uploads were not restored: %v", err)
				}
				if _, err := os.Stat(filepath.Join(uploadsDir, "new.txt")); !os.IsNotExist(err) {
					t.Errorf("uncommitted candidate upload remains active: %v", err)
				}
			}
		})
	}
}
