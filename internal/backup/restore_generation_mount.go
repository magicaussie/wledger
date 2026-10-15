package backup

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuxedocurly/wledger/internal/maintenance"
)

// Mount-aware upload generation activation.
//
// The default restore protocol swaps upload generations by renaming the live
// uploads directory itself aside and renaming the prepared candidate into its
// place. That whole-directory rename is atomic on one filesystem, but it fails
// with EBUSY when the uploads path is a mount point — which is exactly the case
// in the standard Docker deployment, where docker-compose.yaml bind-mounts the
// host uploads directory onto the application's uploads path (and the Dockerfile
// declares it a VOLUME).
//
// When the uploads path is a mount point the mounted directory is never renamed,
// unmounted or replaced. Instead the *contents* of the mount are swapped: every
// non-hidden top-level entry of the live directory is renamed into a staging
// "previous" generation, and every entry of the prepared candidate generation is
// renamed into the live directory. Both generations live inside the mount so
// every rename stays on a single filesystem and remains a cheap, atomic rename.
//
// Atomicity and recovery: a mount point cannot be swapped in one syscall, so the
// swap is a sequence of per-entry renames rather than one rename. The exclusive
// maintenance gate is held for the whole restore, so no request is served while
// the live namespace is between generations; a crash instead leaves a durable
// journal (see restore_generation.go), a database generation marker written in
// the same SQLite transaction, and the on-disk state described below. Recovery
// reads those to roll forward or back deterministically.
//
// The swap proceeds in two structurally distinguishable phases:
//
//	phase 1 (drain):  live entries -> previous, then a durable ".drained" marker
//	                  is written and fsynced inside "previous".
//	phase 2 (fill):   candidate entries -> live.
//
// Because the marker is durable before any fill rename, an interrupted restore
// can always be classified: without the marker the live directory still holds
// only previous-generation entries (finish the drain, or restore from
// "previous"); with the marker the live directory holds only candidate entries
// (undo the fill by moving them back into "candidate", then restore "previous").
const (
	// mountControlDirName is the hidden directory, created inside the mounted
	// uploads directory, that holds the generation staging directories. It is the
	// same directory the maintenance gate uses for the durable journal, so the name
	// is defined once in the maintenance package.
	mountControlDirName = maintenance.InMountControlDirName
	// mountDrainedMarker is written inside the previous generation once phase 1
	// (draining the live directory) has completed durably.
	mountDrainedMarker = ".drained"
)

// mountActivationFailpoint, when non-nil, is invoked after each individual mount
// activation rename and after the durable drain marker is written, so tests can
// simulate a process termination at a precise point. It is nil in production.
var mountActivationFailpoint func(phase string)

// uploadsDirIsMountPoint is a test seam over isMountPoint so the mount-aware
// protocol can be exercised deterministically without creating a real mount.
var uploadsDirIsMountPoint = isMountPoint

// isMountPoint reports whether path itself is a mount point. The detection lives
// in the maintenance package so the restore protocol and the maintenance lock
// agree on where the persistent mount is.
func isMountPoint(path string) bool {
	return maintenance.IsMountPoint(path)
}

// mountGenerationPaths returns the in-mount control directory and the candidate
// and previous generation directories it holds.
func mountGenerationPaths(uploadsDir, id string) (control, candidate, previous string) {
	control = maintenance.InMountControlDir(uploadsDir)
	candidate = filepath.Join(control, id+".candidate")
	previous = filepath.Join(control, id+".previous")
	return
}

// assetEntries returns the names of the non-hidden entries directly under dir.
// Hidden entries (names beginning with ".") are the restore control plane — the
// generation staging directory, the maintenance lock, and operator recovery
// directories — and are never part of an uploads generation, so they are never
// swapped and their data is never touched by activation or rollback.
func assetEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		names = append(names, entry.Name())
	}
	return names, nil
}

// moveAssets renames every non-hidden top-level entry of from into to. Callers
// guarantee by construction that the moved names do not already exist in to. The
// phase is passed to the activation failpoint (nil in production) after each
// rename so a test can terminate the process at a precise point.
func moveAssets(from, to, phase string) error {
	names, err := assetEntries(from)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := renameSynced(filepath.Join(from, name), filepath.Join(to, name)); err != nil {
			return err
		}
		if mountActivationFailpoint != nil {
			mountActivationFailpoint(phase)
		}
	}
	return nil
}

// mountDrained reports whether the durable drain marker exists in previous.
func mountDrained(previous string) (bool, error) {
	info, err := os.Lstat(filepath.Join(previous, mountDrainedMarker))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("restore drain marker %q is not a regular file", filepath.Join(previous, mountDrainedMarker))
	}
	return true, nil
}

// writeMountDrainedMarker durably records that phase 1 (the drain) completed.
func writeMountDrainedMarker(previous string) error {
	path := filepath.Join(previous, mountDrainedMarker)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create restore drain marker: %w", err)
	}
	if _, err := f.WriteString("1\n"); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write restore drain marker: %w", err)
	}
	return syncDirectory(previous)
}

// cleanAbandonedMountGenerations removes generation directories left inside the
// in-mount control directory by an earlier restore that has no pending journal
// (for example a process killed during extraction, before the journal was
// written). The exclusive gate is held for the whole restore, so these cannot
// belong to a live operation.
func cleanAbandonedMountGenerations(control string) error {
	entries, err := os.ReadDir(control)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".candidate") || strings.HasSuffix(name, ".previous") {
			if err := os.RemoveAll(filepath.Join(control, name)); err != nil {
				return err
			}
		}
	}
	return syncDirectory(control)
}

// finishGenerationMount activates the candidate generation inside a mounted
// uploads directory without renaming the mount point: it drains the current
// contents into the previous generation (recording the completed drain with a
// durable marker) and then moves the candidate contents into the live namespace.
// It is idempotent, so it also completes a committed restore at startup.
func finishGenerationMount(uploadsDir string, record restoreJournal) error {
	control, candidate, previous := mountGenerationPaths(uploadsDir, record.ID)
	candidateExists, err := pathExists(candidate)
	if err != nil {
		return err
	}
	if !candidateExists {
		// Roll-forward is already complete: the candidate has been consumed.
		return nil
	}
	if err := os.MkdirAll(previous, 0755); err != nil {
		return fmt.Errorf("create previous uploads generation: %w", err)
	}
	drained, err := mountDrained(previous)
	if err != nil {
		return err
	}
	if !drained {
		if err := moveAssets(uploadsDir, previous, "drain"); err != nil {
			return fmt.Errorf("preserve previous uploads generation: %w", err)
		}
		if err := writeMountDrainedMarker(previous); err != nil {
			return err
		}
		if mountActivationFailpoint != nil {
			mountActivationFailpoint("drain-marker")
		}
	}
	if err := moveAssets(candidate, uploadsDir, "fill"); err != nil {
		return fmt.Errorf("activate candidate uploads generation: %w", err)
	}
	if err := os.RemoveAll(candidate); err != nil {
		return fmt.Errorf("remove activated candidate uploads generation: %w", err)
	}
	return syncDirectory(control)
}

// rollbackGenerationMount restores the previous generation inside a mounted
// uploads directory after an uncommitted restore, without renaming the mount
// point. It reverses the drain and fill in the correct order, using the durable
// drain marker to know whether the candidate may already have been moved into
// the live namespace.
func rollbackGenerationMount(uploadsDir string, record restoreJournal) error {
	control, candidate, previous := mountGenerationPaths(uploadsDir, record.ID)
	previousExists, err := pathExists(previous)
	if err != nil {
		return err
	}
	if previousExists {
		drained, err := mountDrained(previous)
		if err != nil {
			return err
		}
		if drained {
			// The candidate may already be (partly) active: move whatever the
			// fill put into the live namespace back into the candidate staging
			// directory so the previous generation can be restored into a
			// namespace that no longer holds candidate entries.
			if err := os.MkdirAll(candidate, 0755); err != nil {
				return fmt.Errorf("recreate candidate uploads generation for rollback: %w", err)
			}
			if err := moveAssets(uploadsDir, candidate, "rollback-fill"); err != nil {
				return fmt.Errorf("preserve uncommitted candidate uploads: %w", err)
			}
		}
		if err := moveAssets(previous, uploadsDir, "rollback-drain"); err != nil {
			return fmt.Errorf("restore previous uploads generation: %w", err)
		}
		if err := os.RemoveAll(previous); err != nil {
			return fmt.Errorf("remove rolled-back previous uploads generation: %w", err)
		}
	}
	if err := os.RemoveAll(candidate); err != nil {
		return fmt.Errorf("remove uncommitted candidate directory: %w", err)
	}
	// Remove the now-empty in-mount control directory so no restore artifact is
	// left in the mounted uploads namespace. If it still holds another generation
	// directory (not empty), leaving it is harmless.
	if err := os.Remove(control); err == nil || errors.Is(err, os.ErrNotExist) {
		return syncDirectory(uploadsDir)
	}
	return syncDirectory(control)
}

// cleanupMountGeneration removes the replaced generation and any abandoned
// extraction staging directories from the mounted namespace once a new
// generation is verified active. The previous generation holds only replaced
// top-level upload entries — operator recovery directories are hidden and stay
// untouched in the live namespace — so it is safe to remove. Cleanup failures
// are warnings: they cannot invalidate the active, already-verified generation.
func cleanupMountGeneration(uploadsDir, previous string, logger *slog.Logger) {
	if err := os.RemoveAll(previous); err != nil {
		logger.Warn("restore is active but old-generation cleanup failed", "path", previous, "err", err)
	}
	if err := removeStagingDirectories(uploadsDir); err != nil {
		logger.Warn("failed to clean stale staging directories from the mounted uploads", "dir", uploadsDir, "err", err)
	}
	control := filepath.Dir(previous)
	if err := os.Remove(control); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Not empty (a concurrent generation exists) or not removable; leaving an
		// empty hidden control directory is harmless.
		logger.Debug("in-mount restore control directory not removed", "path", control, "err", err)
	}
}
