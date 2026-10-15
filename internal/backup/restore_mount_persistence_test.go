package backup

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/maintenance"
)

// These tests cover the D1 follow-up: the recovery metadata must live inside the
// persistent uploads mount, not on the container filesystem, so an interrupted
// restore is still recoverable after Docker recreates the container with the
// same persistent volumes but a fresh container filesystem. They also cover the
// fail-closed behaviour required when the recovery metadata is missing,
// corrupted or contradictory.

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRestore_MountModeJournalLivesInsideMountDuringActivation verifies that the
// durable restore journal is written inside the mount (not on the container
// filesystem) while activation is in progress.
func TestRestore_MountModeJournalLivesInsideMountDuringActivation(t *testing.T) {
	_, s, svc, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(uploadsDir, "old.txt"), "old")

	reached := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	orig := mountActivationFailpoint
	mountActivationFailpoint = func(phase string) {
		if phase == "drain" {
			once.Do(func() {
				close(reached)
				<-release
			})
		}
	}
	defer func() { mountActivationFailpoint = orig }()

	archive := buildRestoreZipWithFiles(t, restoreUserManifest(), map[string]string{"new.txt": "new"})
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Restore(ctx, bytes.NewReader(archive), int64(len(archive))) }()

	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("restore did not reach the drain failpoint")
	}
	// The durable journal must be inside the mount, not on the container filesystem.
	mustExist(t, filepath.Join(uploadsDir, mountControlDirName, restoreJournalName))
	mustNotExist(t, filepath.Join(maintenance.ControlDir(uploadsDir), restoreJournalName))

	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	mustNoJournal(t, uploadsDir)
	mustNotExist(t, filepath.Join(uploadsDir, mountControlDirName))
}

// TestRestore_MountModeRecoversInterruptedDrainWithoutJournal reconstructs the
// on-disk state produced by a process killed during the drain phase, with the
// journal lost (as after a container recreation), and verifies the durable
// generation directories alone let startup recovery restore the previous
// generation exactly.
func TestRestore_MountModeRecoversInterruptedDrainWithoutJournal(t *testing.T) {
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
	writeFile(t, filepath.Join(candidate, "new.txt"), "new")
	// No journal: it was lost with the container filesystem.

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), discardLogger()); err != nil {
		t.Fatalf("startup recovery failed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "old.txt")); err != nil || string(got) != "old" {
		t.Errorf("old.txt not restored: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "images", "old.png")); err != nil || string(got) != "old" {
		t.Errorf("images/old.png not restored: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(uploadsDir, "new.txt"))
	mustNotExist(t, filepath.Join(control, id+".candidate"))
	mustNotExist(t, filepath.Join(control, id+".previous"))
	mustNoJournal(t, uploadsDir)
	mustNotExist(t, control)
}

// TestRestore_MountModeRecoversInterruptedFillWithoutJournal reconstructs a
// process killed during the fill phase with the journal lost and verifies
// startup recovery restores the previous generation exactly.
func TestRestore_MountModeRecoversInterruptedFillWithoutJournal(t *testing.T) {
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

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), discardLogger()); err != nil {
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
	mustNotExist(t, control)
}

// TestRestore_MountModeRollsForwardCommittedWithoutJournal reconstructs a
// process killed after the database committed but before cleanup, with the
// journal lost, and verifies startup recovery keeps the committed generation and
// clears the replaced one.
func TestRestore_MountModeRollsForwardCommittedWithoutJournal(t *testing.T) {
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
	// No journal, but the database records the committed generation.
	if err := s.SetRestoreGeneration(ctx, id); err != nil {
		t.Fatal(err)
	}

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), discardLogger()); err != nil {
		t.Fatalf("startup recovery failed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "new.txt")); err != nil || string(got) != "new" {
		t.Errorf("committed generation lost: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(previous, "old.txt"))
	mustNoJournal(t, uploadsDir)
	mustNotExist(t, control)
}

// TestRestore_MountModeJournalSurvivesContainerRecreation simulates Docker
// recreating the container with the same persistent uploads bind mount but a
// fresh container filesystem: the sibling control directory (on the container
// filesystem) disappears while the mount and its in-mount journal persist.
// Startup recovery must still restore the previous generation exactly.
func TestRestore_MountModeJournalSurvivesContainerRecreation(t *testing.T) {
	database, s, _, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	const id = "dddddddddddddddddddddddddddddddd"

	writeFile(t, filepath.Join(uploadsDir, "images", "old.png"), "old")
	control, candidate, previous := mountPaths(uploadsDir, id)
	writeFile(t, filepath.Join(previous, "old.txt"), "old")
	writeFile(t, filepath.Join(candidate, "new.txt"), "new")
	writeJournal(t, uploadsDir, restoreJournal{Version: 1, ID: id, HadUploads: true})

	// The journal is durable inside the mount.
	mustExist(t, filepath.Join(uploadsDir, mountControlDirName, restoreJournalName))

	// Recreate the container: the container filesystem (and therefore the sibling
	// control directory) is discarded, but the persistent mount is kept.
	if err := os.RemoveAll(maintenance.ControlDir(uploadsDir)); err != nil {
		t.Fatal(err)
	}
	mustNotExist(t, filepath.Join(maintenance.ControlDir(uploadsDir), restoreJournalName))

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), discardLogger()); err != nil {
		t.Fatalf("startup recovery after container recreation failed: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "old.txt")); err != nil || string(got) != "old" {
		t.Errorf("old.txt not restored after recreation: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "images", "old.png")); err != nil || string(got) != "old" {
		t.Errorf("images/old.png not restored after recreation: %q err=%v", got, err)
	}
	mustNotExist(t, filepath.Join(uploadsDir, "new.txt"))
	mustNotExist(t, filepath.Join(control, id+".candidate"))
	mustNotExist(t, filepath.Join(control, id+".previous"))
	mustNoJournal(t, uploadsDir)
	mustNotExist(t, control)
}

// TestRestore_MountModeMissingJournalAmbiguousFailsClosed verifies that when the
// journal is missing and the durable generation directories are ambiguous (more
// than one generation ID), startup recovery fails closed and preserves every
// file rather than guessing.
func TestRestore_MountModeMissingJournalAmbiguousFailsClosed(t *testing.T) {
	database, s, _, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(uploadsDir, "live.txt"), "live")
	control, _, _ := mountPaths(uploadsDir, "00000000000000000000000000000000")
	writeFile(t, filepath.Join(control, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.previous", "old.txt"), "old")
	writeFile(t, filepath.Join(control, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.candidate", "new.txt"), "new")

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), discardLogger()); err == nil {
		t.Fatal("expected recovery to fail closed on ambiguous generation directories")
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "live.txt")); err != nil || string(got) != "live" {
		t.Errorf("live file not preserved: %q err=%v", got, err)
	}
	mustExist(t, filepath.Join(control, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.previous", "old.txt"))
	mustExist(t, filepath.Join(control, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.candidate", "new.txt"))
}

// TestRestore_MountModeCorruptedJournalFailsClosed verifies that a corrupted
// journal makes startup recovery fail closed and preserves all data.
func TestRestore_MountModeCorruptedJournalFailsClosed(t *testing.T) {
	database, s, _, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(uploadsDir, "live.txt"), "live")
	journal := filepath.Join(uploadsDir, mountControlDirName, restoreJournalName)
	writeFile(t, journal, "{not valid json")

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), discardLogger()); err == nil {
		t.Fatal("expected recovery to fail closed on a corrupted journal")
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "live.txt")); err != nil || string(got) != "live" {
		t.Errorf("live file not preserved: %q err=%v", got, err)
	}
	mustExist(t, journal)
}

// TestRestore_MountModeContradictoryJournalFailsClosed verifies that a journal
// whose generation disagrees with the durable on-disk generation makes startup
// recovery fail closed and preserves all data.
func TestRestore_MountModeContradictoryJournalFailsClosed(t *testing.T) {
	database, s, _, uploadsDir, cleanup := newBackupService(t)
	defer cleanup()
	forceMountMode(t)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}

	const journalID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const diskID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writeFile(t, filepath.Join(uploadsDir, "live.txt"), "live")
	control, _, _ := mountPaths(uploadsDir, journalID)
	writeFile(t, filepath.Join(control, diskID+".previous", "old.txt"), "old")
	writeJournal(t, uploadsDir, restoreJournal{Version: 1, ID: journalID, HadUploads: true})

	if err := RecoverStartup(ctx, database, uploadsDir, maintenance.New(uploadsDir), discardLogger()); err == nil {
		t.Fatal("expected recovery to fail closed on contradictory metadata")
	}
	if got, err := os.ReadFile(filepath.Join(uploadsDir, "live.txt")); err != nil || string(got) != "live" {
		t.Errorf("live file not preserved: %q err=%v", got, err)
	}
	mustExist(t, filepath.Join(control, diskID+".previous", "old.txt"))
	mustExist(t, filepath.Join(uploadsDir, mountControlDirName, restoreJournalName))
}
