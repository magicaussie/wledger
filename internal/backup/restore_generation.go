package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/maintenance"
)

const restoreJournalName = maintenance.JournalName

type restoreJournal struct {
	Version    int    `json:"version"`
	ID         string `json:"id"`
	HadUploads bool   `json:"had_uploads"`
}

func generationPaths(uploadsDir, id string) (parent, candidate, previous, journal string) {
	parent = filepath.Dir(uploadsDir)
	control := maintenance.ControlDir(uploadsDir)
	candidate = filepath.Join(control, id+".candidate")
	previous = filepath.Join(control, id+".previous")
	journal = filepath.Join(control, restoreJournalName)
	return
}

// journalPath returns the durable location of the restore journal for this
// uploads directory. When the uploads path is a mount point the journal lives
// inside the mount (in the hidden control directory), so it survives container
// recreation; otherwise it lives in the sibling control directory beside the
// uploads directory. The sibling control directory is on the container
// filesystem, so a journal written there is lost when the container is
// recreated — which is exactly why the mount-aware protocol keeps it in the
// mount.
func journalPath(uploadsDir string) string {
	if uploadsDirIsMountPoint(uploadsDir) {
		return filepath.Join(maintenance.InMountControlDir(uploadsDir), restoreJournalName)
	}
	return filepath.Join(maintenance.ControlDir(uploadsDir), restoreJournalName)
}

func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func syncTree(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!entry.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("candidate upload contains an unsafe filesystem entry %q", name)
		}
		if entry.IsDir() {
			dirs = append(dirs, name)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncDirectory(dirs[i]); err != nil {
			return fmt.Errorf("sync candidate directory %q: %w", dirs[i], err)
		}
	}
	return nil
}

func writeRestoreJournal(uploadsDir string, record restoreJournal) error {
	if record.Version != 1 || len(record.ID) != 32 || strings.Trim(record.ID, "0123456789abcdef") != "" {
		return errors.New("refusing to write an invalid restore journal")
	}
	journal := journalPath(uploadsDir)
	if err := os.MkdirAll(filepath.Dir(journal), 0755); err != nil {
		return fmt.Errorf("create restore journal directory: %w", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp := journal + "." + record.ID + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create restore journal temporary file: %w", err)
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write restore journal: %w", err)
	}
	if err := os.Rename(tmp, journal); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("activate restore journal: %w", err)
	}
	if err := syncDirectory(filepath.Dir(journal)); err != nil {
		return fmt.Errorf("sync restore journal directory: %w", err)
	}
	if err := syncDirectory(filepath.Dir(filepath.Dir(journal))); err != nil {
		return fmt.Errorf("sync restore journal parent directory: %w", err)
	}
	return nil
}

// readRestoreJournal reads the pending restore journal from every durable
// location it may occupy. A journal present in more than one location must
// describe the same generation; otherwise the state is contradictory and
// recovery fails closed.
func readRestoreJournal(uploadsDir string) (*restoreJournal, error) {
	var found *restoreJournal
	for _, path := range maintenance.JournalPaths(uploadsDir) {
		record, err := readJournalFile(path)
		if err != nil {
			return nil, err
		}
		if record == nil {
			continue
		}
		if found != nil && found.ID != record.ID {
			return nil, fmt.Errorf("contradictory restore journals: generation %s and %s disagree", found.ID, record.ID)
		}
		found = record
	}
	return found, nil
}

func readJournalFile(journal string) (*restoreJournal, error) {
	info, err := os.Lstat(journal)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect pending restore journal %q: %w", journal, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("pending restore journal %q is not a regular file", journal)
	}
	data, err := os.ReadFile(journal)
	if err != nil {
		return nil, fmt.Errorf("read pending restore journal %q: %w", journal, err)
	}
	var record restoreJournal
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, fmt.Errorf("decode pending restore journal: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("pending restore journal contains trailing data")
	}
	if record.Version != 1 || len(record.ID) != 32 || strings.Trim(record.ID, "0123456789abcdef") != "" {
		return nil, errors.New("pending restore journal has an unsupported version or invalid generation ID")
	}
	return &record, nil
}

func retireRestoreJournal(uploadsDir, id string) error {
	for _, journal := range maintenance.JournalPaths(uploadsDir) {
		if err := os.Remove(journal); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove completed restore journal: %w", err)
		}
		if err := syncDirectory(filepath.Dir(journal)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func pathExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("restore generation path %q is not a real directory", path)
	}
	return true, nil
}

func renameSynced(oldPath, newPath string) error {
	if err := renameEntry(oldPath, newPath); err != nil {
		return err
	}
	oldParent := filepath.Dir(oldPath)
	newParent := filepath.Dir(newPath)
	if err := syncDirectory(oldParent); err != nil {
		return err
	}
	if newParent != oldParent {
		return syncDirectory(newParent)
	}
	return nil
}

func finishGeneration(uploadsDir string, record restoreJournal) error {
	if uploadsDirIsMountPoint(uploadsDir) {
		return finishGenerationMount(uploadsDir, record)
	}
	_, candidate, previous, _ := generationPaths(uploadsDir, record.ID)
	candidateExists, err := pathExists(candidate)
	if err != nil {
		return err
	}
	uploadsExists, err := pathExists(uploadsDir)
	if err != nil {
		return err
	}
	previousExists, err := pathExists(previous)
	if err != nil {
		return err
	}
	if !candidateExists {
		if !uploadsExists {
			return errors.New("committed restore has neither candidate nor active uploads directory")
		}
		return nil
	}
	if uploadsExists {
		if !record.HadUploads || previousExists {
			return errors.New("cannot safely determine which uploads directory is active during restore roll-forward")
		}
		if err := renameSynced(uploadsDir, previous); err != nil {
			return fmt.Errorf("preserve previous uploads generation: %w", err)
		}
		previousExists = true
	}
	if record.HadUploads && !previousExists {
		return errors.New("committed restore is missing the previous uploads generation")
	}
	if err := renameSynced(candidate, uploadsDir); err != nil {
		return fmt.Errorf("activate candidate uploads generation: %w", err)
	}
	return nil
}

func rollbackGeneration(uploadsDir string, record restoreJournal) error {
	if uploadsDirIsMountPoint(uploadsDir) {
		return rollbackGenerationMount(uploadsDir, record)
	}
	parent, candidate, previous, _ := generationPaths(uploadsDir, record.ID)
	candidateExists, err := pathExists(candidate)
	if err != nil {
		return err
	}
	uploadsExists, err := pathExists(uploadsDir)
	if err != nil {
		return err
	}
	previousExists, err := pathExists(previous)
	if err != nil {
		return err
	}
	if record.HadUploads {
		if previousExists {
			if uploadsExists {
				if candidateExists {
					return errors.New("rollback has both a candidate staging directory and an unexpected active uploads directory")
				}
				if err := renameSynced(uploadsDir, candidate); err != nil {
					return fmt.Errorf("preserve uncommitted candidate uploads: %w", err)
				}
				candidateExists = true
			}
			if err := renameSynced(previous, uploadsDir); err != nil {
				return fmt.Errorf("restore previous uploads generation: %w", err)
			}
		} else if !uploadsExists {
			return errors.New("uncommitted restore is missing both the old and active uploads directories")
		}
	} else if uploadsExists {
		if err := os.RemoveAll(uploadsDir); err != nil {
			return fmt.Errorf("remove uncommitted uploads generation: %w", err)
		}
		if err := syncDirectory(parent); err != nil {
			return err
		}
	}
	if candidateExists {
		if err := os.RemoveAll(candidate); err != nil {
			return fmt.Errorf("remove uncommitted candidate directory: %w", err)
		}
		if err := syncDirectory(filepath.Dir(candidate)); err != nil {
			return err
		}
	}
	return syncDirectory(parent)
}

func recoverPendingRestore(ctx context.Context, database *sql.DB, uploadsDir string, logger *slog.Logger) error {
	record, err := readRestoreJournal(uploadsDir)
	if err != nil {
		return err
	}
	// When the journal is missing, the durable generation staging directories
	// inside a mounted uploads directory may still describe an interrupted
	// restore (for example after the container was recreated and the journal was
	// lost, or if an operator removed it). Reconstruct the pending generation from
	// them, and fail closed if the on-disk state is ambiguous.
	diskID, err := mountGenerationID(uploadsDir)
	if err != nil {
		return err
	}
	if record != nil && diskID != "" && record.ID != diskID {
		return fmt.Errorf("restore journal generation %s contradicts the on-disk generation %s", record.ID, diskID)
	}
	if record == nil && diskID != "" {
		logger.Warn("restore journal is missing; reconstructing it from the durable in-mount generation", "generation", diskID)
		record = &restoreJournal{Version: 1, ID: diskID}
	}
	if record == nil {
		return nil
	}
	generation, err := db.NewStore(database).GetRestoreGeneration(ctx)
	if err != nil {
		return fmt.Errorf("read committed restore generation: %w", err)
	}
	if generation == record.ID {
		if err := finishGeneration(uploadsDir, *record); err != nil {
			return fmt.Errorf("recover committed restore %s: %w", record.ID, err)
		}
		if err := validateActiveGeneration(ctx, database, uploadsDir); err != nil {
			return fmt.Errorf("verify committed restore %s: %w", record.ID, err)
		}
		if err := retireRestoreJournal(uploadsDir, record.ID); err != nil {
			return fmt.Errorf("retire recovered restore journal: %w", err)
		}
		if uploadsDirIsMountPoint(uploadsDir) {
			_, _, previous := mountGenerationPaths(uploadsDir, record.ID)
			cleanupMountGeneration(uploadsDir, previous, logger)
			removeEmptyMountControlDir(uploadsDir, logger)
		} else if record.HadUploads {
			_, _, previous, _ := generationPaths(uploadsDir, record.ID)
			preserve, err := containsRecoveryDirectory(previous)
			if err != nil {
				logger.Warn("could not inspect old uploads generation for recovery data; preserving it", "path", previous, "err", err)
			} else if preserve {
				if err := removeStagingDirectories(previous); err != nil {
					logger.Warn("failed to clean stale staging directories from preserved generation", "path", previous, "err", err)
				}
				logger.Warn("preserving old uploads generation containing operator recovery data", "path", previous)
			} else if err := os.RemoveAll(previous); err != nil {
				logger.Warn("restore is active but old-generation cleanup failed", "path", previous, "err", err)
			}
		}
		logger.Info("completed interrupted restore", "generation", record.ID)
		return nil
	}
	if err := rollbackGeneration(uploadsDir, *record); err != nil {
		return fmt.Errorf("recover uncommitted restore %s: %w", record.ID, err)
	}
	if err := validateActiveGeneration(ctx, database, uploadsDir); err != nil {
		return fmt.Errorf("verify previous generation after restore rollback: %w", err)
	}
	if err := retireRestoreJournal(uploadsDir, record.ID); err != nil {
		return err
	}
	if uploadsDirIsMountPoint(uploadsDir) {
		removeEmptyMountControlDir(uploadsDir, logger)
	}
	logger.Warn("rolled back interrupted restore", "candidate_generation", record.ID, "active_generation", generation)
	return nil
}

// removeEmptyMountControlDir removes the in-mount restore control directory once
// it holds neither a journal nor a generation staging directory. It is a no-op
// when the directory is absent or still holds another generation, so it never
// discards recovery data.
func removeEmptyMountControlDir(uploadsDir string, logger *slog.Logger) {
	control := maintenance.InMountControlDir(uploadsDir)
	if err := os.Remove(control); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Debug("in-mount restore control directory not removed", "path", control, "err", err)
	}
}

// mountGenerationID returns the single generation ID described by the durable
// generation staging directories inside a mounted uploads directory, or "" when
// none are present. It returns an error when the on-disk state is ambiguous
// (more than one generation ID, or an unrecognised or non-directory entry), so
// recovery fails closed rather than guessing which restore is pending. It only
// inspects the in-mount control directory: for an ordinary uploads directory the
// journal and staging share the container filesystem and are lost together, so
// reconstruction is neither possible nor needed there.
func mountGenerationID(uploadsDir string) (string, error) {
	if !uploadsDirIsMountPoint(uploadsDir) {
		return "", nil
	}
	control := maintenance.InMountControlDir(uploadsDir)
	entries, err := os.ReadDir(control)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect in-mount restore control directory: %w", err)
	}
	ids := map[string]struct{}{}
	for _, entry := range entries {
		name := entry.Name()
		var id string
		switch {
		case strings.HasSuffix(name, ".candidate"):
			id = strings.TrimSuffix(name, ".candidate")
		case strings.HasSuffix(name, ".previous"):
			id = strings.TrimSuffix(name, ".previous")
		default:
			continue
		}
		if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
			return "", fmt.Errorf("in-mount restore control directory holds an unrecognised generation entry %q", name)
		}
		if !entry.IsDir() {
			return "", fmt.Errorf("in-mount restore generation %q is not a directory", name)
		}
		ids[id] = struct{}{}
	}
	if len(ids) == 0 {
		return "", nil
	}
	if len(ids) > 1 {
		return "", errors.New("in-mount restore control directory holds multiple generation IDs; refusing to guess which restore is pending")
	}
	for id := range ids {
		return id, nil
	}
	return "", nil
}

func containsRecoveryDirectory(root string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name != root && entry.IsDir() && isRecoveryDir(entry.Name()) {
			found = true
			return fs.SkipDir
		}
		return nil
	})
	return found, err
}

func removeStagingDirectories(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && isStagingDir(entry.Name()) {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
			continue
		}
		if entry.IsDir() {
			if err := removeStagingDirectories(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecoverStartup resolves a durable restore journal before any normal application
// service, data migration, hardware worker or route is initialized.
func RecoverStartup(ctx context.Context, database *sql.DB, uploadsDir string, gate *maintenance.Gate, logger *slog.Logger) error {
	unlock, err := gate.WriteLock()
	if err != nil {
		return err
	}
	defer unlock()
	return RecoverStartupLocked(ctx, database, uploadsDir, logger)
}

// RecoverStartupLocked is used by the server while it holds the exclusive gate
// across database open, migration, restore recovery and integrity validation.
func RecoverStartupLocked(ctx context.Context, database *sql.DB, uploadsDir string, logger *slog.Logger) error {
	releaseUploads, err := maintenance.UploadCleanup(ctx, uploadsDir)
	if err != nil {
		return err
	}
	defer releaseUploads()

	if err := recoverPendingRestore(ctx, database, uploadsDir, logger); err != nil {
		return err
	}
	if _, err := os.Lstat(uploadsDir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(uploadsDir, 0755); err != nil {
			return fmt.Errorf("create uploads directory after restore recovery: %w", err)
		}
		if err := syncDirectory(filepath.Dir(uploadsDir)); err != nil {
			return fmt.Errorf("sync new uploads directory parent: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect active uploads path after restore recovery: %w", err)
	}
	if err := validateActiveGeneration(ctx, database, uploadsDir); err != nil {
		return fmt.Errorf("active restore generation is not valid: %w", err)
	}
	return nil
}

func validateActiveGeneration(ctx context.Context, database *sql.DB, uploadsDir string) error {
	exists, err := pathExists(uploadsDir)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("active uploads directory %q is missing", uploadsDir)
	}
	if err := validateSQLite(database); err != nil {
		return err
	}
	if err := validateReferencedAssets(ctx, db.NewStore(database), uploadsDir); err != nil {
		return err
	}
	return nil
}

func validateSQLite(database *sql.DB) error {
	var result string
	if err := database.QueryRow("PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("SQLite quick_check failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("SQLite quick_check reported %q", result)
	}
	rows, err := database.Query("PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("SQLite foreign_key_check failed: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var fkID int64
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return err
		}
		return fmt.Errorf("SQLite foreign-key violation in %s row %v referencing %s (constraint %d)", table, rowID, parent, fkID)
	}
	return rows.Err()
}
