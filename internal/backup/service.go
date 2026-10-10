package backup

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

type Service interface {
	Export(ctx context.Context, w io.Writer) error
	Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error
}

// renameEntry is os.Rename, indirected so tests can inject filesystem failures
// into the uploads swap.
var renameEntry = os.Rename

// zipFileWriter is the subset of *zip.Writer used by the backup export. It is an
// interface so tests can inject deterministic archive and finalization failures.
type zipFileWriter interface {
	Create(name string) (io.Writer, error)
	Close() error
}

// Test seams. Each is the real function by default and is indirected so tests
// can inject deterministic failures into the export path.
var (
	walkUploads        = filepath.Walk
	openUpload         = os.Open
	newBackupZipWriter = func(w io.Writer) zipFileWriter { return zip.NewWriter(w) }
)

// RestoreCommittedError reports that a restore committed its data and files but
// a post-restore step did not complete cleanly: drawer allocation processing
// failed, or the restored LED coordinate space remains unresolved. It is a
// warning, not a failure, and must not be reported to the administrator as a
// failed restore.
type RestoreCommittedError struct {
	Reason string
}

func (e *RestoreCommittedError) Error() string {
	return "restore completed with warnings: " + e.Reason
}

type service struct {
	db         *sql.DB
	store      db.Store
	uploadsDir string
	logger     *slog.Logger

	// restoreMu serializes restore operations. It ensures a starting restore
	// cannot delete the staging directory of a restore that is still running, and
	// that two restores cannot interleave their uploads swaps.
	//
	// It is an in-process lock only. A separate process sharing the same uploads
	// directory would not be serialized by it. WLEDger runs a single server
	// process that performs restores (the MCP server does not), so this is
	// sufficient; a future multi-process deployment sharing uploads would need a
	// filesystem-level lock as well.
	restoreMu sync.Mutex
}

func NewService(database *sql.DB, store db.Store, uploadsDir string, logger *slog.Logger) Service {
	return &service{
		db:         database,
		store:      store,
		uploadsDir: uploadsDir,
		logger:     logger,
	}
}

func (s *service) Export(ctx context.Context, w io.Writer) error {
	s.logger.Debug("starting system backup export")

	// Read the entire backup inside a single transaction so the manifest is a
	// consistent snapshot: a concurrent write cannot make one entity reference
	// another that the same manifest is missing. Any read failure aborts the
	// export instead of being reported as a successful, incomplete backup.
	manifest, err := s.snapshotManifest(ctx)
	if err != nil {
		return err
	}
	zw := newBackupZipWriter(w)

	// Add restore_data.json
	s.logger.Debug("adding restore_data.json to backup")
	fJson, err := zw.Create("restore_data.json")
	if err != nil {
		return fmt.Errorf("failed to create json entry: %w", err)
	}
	enc := json.NewEncoder(fJson)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("failed to encode json: %w", err)
	}

	// add human_readable_parts.csv
	s.logger.Debug("adding human_readable_parts.csv to backup")
	if err := writePartsCSV(zw, manifest.Parts, manifest.PartAssignments); err != nil {
		return err
	}

	// Add Uploads
	s.logger.Debug("collecting upload files for backup", "dir", s.uploadsDir)
	if err := s.writeUploads(zw); err != nil {
		return fmt.Errorf("failed to archive uploads: %w", err)
	}

	// Finalize the archive. Only a successful close produces a valid ZIP; a
	// failure here must not be reported as a successful backup.
	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to finalize backup archive: %w", err)
	}

	// The archive is complete; only now record the successful backup.
	audit.Log(ctx, s.store, "BACKUP", "SYSTEM", 0, "Downloaded system backup", nil, nil)
	return nil
}

// recoveryDirPrefix names an uploads directory holding a previous restore's
// uploads that could not be swapped back. Such a directory is the
// administrator's only copy of those files and is never removed automatically.
const recoveryDirPrefix = ".uploads_bak_"

// stagingDirPrefix names a per-restore uploads extraction directory. These are
// transient: a completed restore removes its own, and an abandoned one left by
// an interrupted restore is removed at the start of the next restore.
const stagingDirPrefix = ".restore_tmp_"

func isRecoveryDir(name string) bool { return strings.HasPrefix(name, recoveryDirPrefix) }
func isStagingDir(name string) bool  { return strings.HasPrefix(name, stagingDirPrefix) }

// withinDir reports whether target resolves to base itself or to a descendant of
// base. It uses a relative-path test so a sibling whose name merely shares a
// string prefix with base (for example base "/a/b" and target "/a/bc") is
// correctly rejected, unlike a raw strings.HasPrefix comparison.
func withinDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// snapshotManifest reads every entity required by a backup inside a single
// transaction, so the manifest is a consistent snapshot and a concurrent write
// cannot make one entity reference another the manifest omits. Every read is
// checked; a failure aborts the export instead of silently producing an empty
// or partial manifest.
func (s *service) snapshotManifest(ctx context.Context) (Manifest, error) {
	var manifest Manifest
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		var m Manifest
		m.Version = "1.0"
		m.ExportedAt = time.Now()

		settings, err := q.GetSettings(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("failed to fetch settings: %w", err)
		}
		m.Settings = settings

		if m.Users, err = q.GetAllUsers(ctx); err != nil {
			return fmt.Errorf("failed to fetch users: %w", err)
		}
		if m.Controllers, err = q.GetControllers(ctx); err != nil {
			return fmt.Errorf("failed to fetch controllers: %w", err)
		}
		if m.Containers, err = q.GetAllContainers(ctx); err != nil {
			return fmt.Errorf("failed to fetch containers: %w", err)
		}
		if m.Walls, err = q.GetWalls(ctx); err != nil {
			return fmt.Errorf("failed to fetch walls: %w", err)
		}
		if m.WallCards, err = q.GetAllWallCards(ctx); err != nil {
			return fmt.Errorf("failed to fetch wall cards: %w", err)
		}
		if m.Bins, err = q.GetAllBins(ctx); err != nil {
			return fmt.Errorf("failed to fetch bins: %w", err)
		}
		if m.Parts, err = q.GetAllParts(ctx); err != nil {
			return fmt.Errorf("failed to fetch parts: %w", err)
		}
		if m.PartAssignments, err = q.GetAllPartAssignments(ctx); err != nil {
			return fmt.Errorf("failed to fetch part assignments: %w", err)
		}
		if m.PartLinks, err = q.GetAllPartLinks(ctx); err != nil {
			return fmt.Errorf("failed to fetch part links: %w", err)
		}
		if m.PartDocs, err = q.GetAllPartDocs(ctx); err != nil {
			return fmt.Errorf("failed to fetch part docs: %w", err)
		}
		if m.PartAiPrompts, err = q.GetAllPartAiPrompts(ctx); err != nil {
			return fmt.Errorf("failed to fetch part ai prompts: %w", err)
		}

		logs, err := q.GetAllAuditLogs(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch audit logs: %w", err)
		}
		var auditLogs []AuditLogEntry
		for _, l := range logs {
			auditLogs = append(auditLogs, AuditLogEntry{
				ID:         l.ID,
				UserID:     l.UserID,
				ActionType: l.ActionType,
				EntityType: l.EntityType,
				EntityID:   l.EntityID,
				Details:    l.Details,
				OldValue:   auditRawValue(l.OldValue, l.OldValueNull),
				NewValue:   auditRawValue(l.NewValue, l.NewValueNull),
				CreatedAt:  l.CreatedAt,
			})
		}
		m.AuditLogs = auditLogs

		if m.Tags, err = q.ListAllTags(ctx); err != nil {
			return fmt.Errorf("failed to fetch tags: %w", err)
		}
		if m.PartTags, err = q.GetAllPartTags(ctx); err != nil {
			return fmt.Errorf("failed to fetch part tags: %w", err)
		}
		if m.SupplierRefs, err = q.GetAllSupplierReferences(ctx); err != nil {
			return fmt.Errorf("failed to fetch supplier references: %w", err)
		}
		if m.PartParameters, err = q.GetAllPartParameters(ctx); err != nil {
			return fmt.Errorf("failed to fetch part parameters: %w", err)
		}

		partPricingRows, err := q.GetAllPartPricing(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch part pricing: %w", err)
		}
		partPricing := make([]db.PartPricing, len(partPricingRows))
		for i, row := range partPricingRows {
			partPricing[i] = db.PartPricing{
				ID:            row.ID,
				PartID:        row.PartID,
				SupplierRefID: row.SupplierRefID,
				MinQuantity:   row.MinQuantity,
				Price:         row.Price,
				Currency:      row.Currency,
				IncludesTax:   row.IncludesTax,
			}
		}
		m.PartPricing = partPricing

		if m.SupplierCredentials, err = q.GetAllSupplierCredentials(ctx); err != nil {
			return fmt.Errorf("failed to fetch supplier credentials: %w", err)
		}
		if m.PriceHistory, err = q.GetAllPriceHistory(ctx); err != nil {
			return fmt.Errorf("failed to fetch price history: %w", err)
		}

		// Export the actual coordinate space of the stored bin LED indices so a
		// restore can interpret them without guessing.
		space, err := ledspace.Current(ctx, q)
		if err != nil {
			return fmt.Errorf("failed to read LED coordinate space: %w", err)
		}
		m.BinIndexSpace = space

		manifest = m
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// writePartsCSV writes the human-readable parts CSV to the archive. Every write
// and the final flush are checked so a partial CSV cannot be reported as a
// successful backup.
func writePartsCSV(zw zipFileWriter, parts []db.Part, assignments []db.PartAssignment) error {
	fCsv, err := zw.Create("human_readable_parts.csv")
	if err != nil {
		return fmt.Errorf("failed to create csv entry: %w", err)
	}
	cw := csv.NewWriter(fCsv)
	if err := cw.Write([]string{"Name", "Description", "Part Number", "Manufacturer", "Supplier", "Unit Cost", "Reorder Level", "Min Stock", "Barcode", "Quantity"}); err != nil {
		return fmt.Errorf("failed to write parts csv header: %w", err)
	}
	for _, p := range parts {
		var total int64
		for _, a := range assignments {
			if a.PartID == p.ID {
				total += a.Quantity
			}
		}
		if err := cw.Write([]string{
			p.Name,
			p.Description.String,
			p.PartNumber.String,
			p.Manufacturer.String,
			p.Supplier.String,
			fmt.Sprintf("%.2f", p.UnitCost.Float64),
			fmt.Sprintf("%d", p.ReorderLevel.Int64),
			fmt.Sprintf("%d", p.MinStockThreshold.Int64),
			p.BarcodeData.String,
			fmt.Sprintf("%d", total),
		}); err != nil {
			return fmt.Errorf("failed to write parts csv row: %w", err)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return fmt.Errorf("failed to flush parts csv: %w", err)
	}
	return nil
}

// writeUploads streams the uploads directory into the archive. Traversal, entry
// creation, file opening, reading and closing are all checked and propagated so
// a failed upload archive cannot be reported as a successful backup.
func (s *service) writeUploads(zw zipFileWriter) error {
	return walkUploads(s.uploadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// A missing uploads directory is not an error: there is simply nothing
			// to archive. Any other traversal error must fail the backup.
			if os.IsNotExist(err) {
				s.logger.Debug("uploads directory not found, skipping", "path", s.uploadsDir)
				return nil
			}
			return err
		}

		// Never archive a symlink. filepath.Walk does not descend directory
		// symlinks, but openUpload would follow a file symlink and could pull a
		// file from outside the uploads directory into the archive. Skipping every
		// symlink keeps the archive confined to real upload files.
		if info.Mode()&os.ModeSymlink != 0 {
			s.logger.Debug("skipping symlink in uploads", "path", path)
			return nil
		}

		// Ignore hidden files/directories (recovery/staging dirs, .git, etc.).
		// They are never part of a backup.
		if strings.HasPrefix(info.Name(), ".") && path != s.uploadsDir {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		relInZip, err := filepath.Rel(s.uploadsDir, path)
		if err != nil {
			return err
		}
		zipPath := filepath.Join("uploads", relInZip)

		s.logger.Debug("adding file to backup zip", "path", path, "zip_path", zipPath)
		zf, err := zw.Create(zipPath)
		if err != nil {
			return err
		}

		fsFile, err := openUpload(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(zf, fsFile)
		closeErr := fsFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// cleanAbandonedStagingDirs removes extraction staging directories left behind
// by a previous, interrupted restore. It never removes a recovery directory
// (.uploads_bak_*): those hold a previous restore's uploads when a filesystem
// rollback failed, and deleting one would destroy the administrator's only copy.
//
// Restores are serialized by restoreMu, so no staging directory can belong to a
// restore that is still running when this is called.
func (s *service) cleanAbandonedStagingDirs() {
	entries, err := os.ReadDir(s.uploadsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		full := filepath.Join(s.uploadsDir, entry.Name())
		switch {
		case isRecoveryDir(entry.Name()):
			s.logger.Warn("preserving uploads recovery directory; it is not deleted automatically and may need manual recovery", "dir", full)
		case isStagingDir(entry.Name()):
			s.logger.Info("cleaning up abandoned restore staging directory", "dir", full)
			if err := os.RemoveAll(full); err != nil {
				s.logger.Warn("failed to remove abandoned restore staging directory", "dir", full, "err", err)
			}
		}
	}
}

func (s *service) Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error {
	s.logger.Debug("starting system restore", "size", size)
	// Serialize restores: this ensures a starting restore cannot delete another
	// running restore's staging directory, and that two restores cannot interleave
	// their uploads swaps.
	s.restoreMu.Lock()
	defer s.restoreMu.Unlock()

	s.cleanAbandonedStagingDirs()

	zr, err := zip.NewReader(zipReader, size)
	if err != nil {
		return fmt.Errorf("invalid ZIP file: %w", err)
	}

	// Validation: Find & Parse JSON Manifest
	var manifest Manifest
	var manifestFound bool

	for _, f := range zr.File {
		if f.Name == "restore_data.json" {
			s.logger.Debug("found restore_data.json in backup")
			rc, err := f.Open()
			if err != nil {
				return fmt.Errorf("failed to open restore_data.json: %w", err)
			}
			if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
				rc.Close()
				return fmt.Errorf("failed to parse backup JSON: %w", err)
			}
			rc.Close()
			manifestFound = true
			break
		}
	}

	if !manifestFound {
		return errors.New("invalid Backup: restore_data.json missing")
	}

	// Resolve the coordinate system of the backup's bin LED indices. An
	// unsupported explicit marker is rejected; an absent marker on a backup that
	// contains bins is treated as unresolved and is never inferred from index
	// values or timestamps.
	space, err := resolveBinIndexSpace(manifest)
	if err != nil {
		return err
	}

	// Validate restored drawer allocations before touching any persisted data,
	// using the coordinate system the backup declares. Segment-relative and
	// drawer-relative backups both validate bin containment against the drawer
	// allocation, but in their own coordinate space. Unresolved backups only have
	// their allocation ranges checked, because bin containment cannot be assessed
	// without a known bin coordinate space.
	switch space {
	case ledspace.Segment:
		if _, err := hardware.ValidateRestoredAllocations(manifest.Containers, manifest.Bins); err != nil {
			return fmt.Errorf("invalid backup: %w", err)
		}
	case ledspace.Drawer:
		if err := hardware.ValidateRestoredDrawerAllocations(manifest.Containers, manifest.Bins); err != nil {
			return fmt.Errorf("invalid backup: %w", err)
		}
	default: // unresolved
		if err := hardware.ValidateRestoredAllocationRanges(manifest.Containers); err != nil {
			return fmt.Errorf("invalid backup: %w", err)
		}
	}

	// Extract uploads to temp directory
	timestamp := time.Now().UnixNano()
	// Use a hidden folder inside uploadsDir to ensure same-filesystem operations
	// This avoids cross-device link errors when uploadsDir is a Docker volume
	tempDir := filepath.Join(s.uploadsDir, fmt.Sprintf(".restore_tmp_%d", timestamp))

	s.logger.Debug("extracting uploads to temp directory", "temp_dir", tempDir)
	defer os.RemoveAll(tempDir)

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		s.logger.Error("failed to create temp restore directory", "err", err, "path", tempDir)
		return fmt.Errorf("failed to create temp restore dir: %w", err)
	}

	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "uploads/") && !f.FileInfo().IsDir() {
			relPath := strings.TrimPrefix(f.Name, "uploads/")
			targetPath := filepath.Join(tempDir, relPath)

			// Security check: reject any entry that would resolve outside the
			// staging directory. A relative-path test avoids the sibling-prefix
			// false negative of a raw string prefix comparison.
			if !withinDir(tempDir, targetPath) {
				s.logger.Warn("security block: zip entry attempts to escape temp directory", "entry", f.Name)
				continue
			}

			s.logger.Debug("extracting file", "zip_path", f.Name, "target_path", targetPath)
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				s.logger.Error("failed to create temp restore subdirectory", "err", err, "path", filepath.Dir(targetPath))
				return fmt.Errorf("failed to create temp subdir: %w", err)
			}

			outFile, err := os.Create(targetPath)
			if err != nil {
				s.logger.Error("failed to create temp restore file", "err", err, "path", targetPath)
				return fmt.Errorf("failed to create temp file: %w", err)
			}

			rc, err := f.Open()
			if err != nil {
				outFile.Close()
				s.logger.Error("failed to open zip file entry for restore", "err", err, "name", f.Name)
				return fmt.Errorf("failed to open zip file entry: %w", err)
			}
			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				s.logger.Error("failed to write temp restore file", "err", err, "path", targetPath)
				return fmt.Errorf("failed to write temp file: %w", err)
			}
		}
	}

	// Database Restore Transaction. The write reservation is taken up front
	// (BEGIN IMMEDIATE) so a concurrent coordinate conversion cannot commit
	// between the restore's reads and its writes.
	err = s.store.ExecImmediateTx(ctx, func(qtx db.Querier) error {
		s.logger.Debug("clearing existing database records")
		// Order matters for Foreign Keys
		if err := qtx.ClearAuditLogs(ctx); err != nil {
			return fmt.Errorf("failed to clear audit logs: %w", err)
		}
		if err := qtx.ClearPartTags(ctx); err != nil {
			return fmt.Errorf("failed to clear part tags: %w", err)
		}
		if err := qtx.ClearTags(ctx); err != nil {
			return fmt.Errorf("failed to clear tags: %w", err)
		}
		if err := qtx.ClearPartAssignments(ctx); err != nil {
			return fmt.Errorf("failed to clear part assignments: %w", err)
		}
		if err := qtx.ClearPartDocs(ctx); err != nil {
			return fmt.Errorf("failed to clear part docs: %w", err)
		}
		if err := qtx.ClearPartLinks(ctx); err != nil {
			return fmt.Errorf("failed to clear part links: %w", err)
		}
		if err := qtx.ClearPartAiPrompts(ctx); err != nil {
			return fmt.Errorf("failed to clear part ai prompts: %w", err)
		}
		if err := qtx.ClearParts(ctx); err != nil {
			return fmt.Errorf("failed to clear parts: %w", err)
		}
		if err := qtx.ClearBins(ctx); err != nil {
			return fmt.Errorf("failed to clear bins: %w", err)
		}
		if err := qtx.ClearWallCards(ctx); err != nil {
			return fmt.Errorf("failed to clear wall cards: %w", err)
		}
		if err := qtx.ClearWalls(ctx); err != nil {
			return fmt.Errorf("failed to clear walls: %w", err)
		}
		if err := qtx.ClearContainers(ctx); err != nil {
			return fmt.Errorf("failed to clear containers: %w", err)
		}
		if err := qtx.ClearControllers(ctx); err != nil {
			return fmt.Errorf("failed to clear controllers: %w", err)
		}
		if err := qtx.ClearUsers(ctx); err != nil {
			return fmt.Errorf("failed to clear users: %w", err)
		}

		// Clear supplier tables
		if err := qtx.ClearPriceHistory(ctx); err != nil {
			return fmt.Errorf("failed to clear price history: %w", err)
		}
		if err := qtx.ClearPartPricing(ctx); err != nil {
			return fmt.Errorf("failed to clear part pricing: %w", err)
		}
		if err := qtx.ClearPartParameters(ctx); err != nil {
			return fmt.Errorf("failed to clear part parameters: %w", err)
		}
		if err := qtx.ClearSupplierReferences(ctx); err != nil {
			return fmt.Errorf("failed to clear supplier references: %w", err)
		}
		if err := qtx.ClearSupplierCredentials(ctx); err != nil {
			return fmt.Errorf("failed to clear supplier credentials: %w", err)
		}

		s.logger.Debug("restoring database records from manifest")
		if err := s.restoreData(ctx, qtx, manifest); err != nil {
			return err
		}
		// Persist the coordinate-space state atomically with the restored data so
		// it survives restarts and cannot be left stale by a crash.
		if err := ledspace.Set(ctx, qtx, space); err != nil {
			return err
		}
		if space == ledspace.Segment {
			// The restored bins are already segment-absolute, so migration 005 must
			// never run against them. Marking it applied here (atomically with the
			// restore) prevents a second conversion at the next startup even when the
			// flag was absent or false before the restore. Drawer-relative and
			// unresolved restores are protected by the coordinate-space check inside
			// migration 005 itself, which never converts a non-segment space.
			if err := qtx.SetFlag(ctx, db.SetFlagParams{Key: hardware.Migration005FlagKey, Value: "true"}); err != nil {
				return fmt.Errorf("failed to mark migration 005 applied: %w", err)
			}
		}
		return nil
	})

	if err != nil {
		s.logger.Error("failed to restore database", "err", err)
		return err
	}

	// Swap the staged uploads into place. The database has already been committed,
	// so a failure here is reported as a committed-with-warning error rather than a
	// failed restore. The previous uploads are moved aside (never overwritten in
	// place) and are deleted only after the swap has fully succeeded, so a failed
	// swap never destroys the only copy of the previous files.
	s.logger.Debug("swapping upload contents", "dir", s.uploadsDir)

	backupDir := filepath.Join(s.uploadsDir, fmt.Sprintf(".uploads_bak_%d", timestamp))
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return &RestoreCommittedError{Reason: "failed to create uploads backup directory: " + err.Error()}
	}

	// Helper to move contents
	moveContents := func(src, dst string) error {
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name := entry.Name()
			// Never move the restore's own staging/backup directories, and never
			// move a preserved recovery directory. The backup directory is deleted
			// after a successful swap, so capturing a recovery directory here would
			// destroy the administrator's only copy of those uploads.
			if name == filepath.Base(tempDir) || name == filepath.Base(backupDir) ||
				isRecoveryDir(name) || isStagingDir(name) {
				continue
			}
			srcPath := filepath.Join(src, name)
			dstPath := filepath.Join(dst, name)
			if err := renameEntry(srcPath, dstPath); err != nil {
				return err
			}
		}
		return nil
	}

	// 1. Move the current uploads aside. Nothing new has been placed yet, so a
	// failure here leaves the previous uploads recoverable.
	if err := moveContents(s.uploadsDir, backupDir); err != nil {
		if rbErr := moveContents(backupDir, s.uploadsDir); rbErr != nil {
			s.logger.Error("FATAL: failed to restore uploads after staging failure", "err", rbErr, "backup_dir", backupDir)
			return &RestoreCommittedError{Reason: "uploads staging failed and rollback failed; previous uploads preserved at " + backupDir}
		}
		return &RestoreCommittedError{Reason: "failed to stage current uploads: " + err.Error()}
	}

	// 2. Move the new uploads into place. On failure, unstage any new files that
	// were already placed and restore the previous uploads, so the live directory
	// is never left as a mix of old and new files.
	if err := moveContents(tempDir, s.uploadsDir); err != nil {
		s.logger.Error("failed to move new uploads into place, rolling back", "err", err)
		if mvErr := moveContents(s.uploadsDir, tempDir); mvErr != nil {
			s.logger.Error("FATAL: failed to unstage new uploads", "err", mvErr, "backup_dir", backupDir)
			return &RestoreCommittedError{Reason: "uploads swap failed and rollback failed; previous uploads preserved at " + backupDir}
		}
		if rbErr := moveContents(backupDir, s.uploadsDir); rbErr != nil {
			s.logger.Error("FATAL: failed to restore previous uploads", "err", rbErr, "backup_dir", backupDir)
			return &RestoreCommittedError{Reason: "uploads swap failed and rollback failed; previous uploads preserved at " + backupDir}
		}
		return &RestoreCommittedError{Reason: "failed to swap new uploads: " + err.Error()}
	}

	// 3. The swap succeeded; the previous uploads are no longer needed.
	if err := os.RemoveAll(backupDir); err != nil {
		s.logger.Warn("failed to remove uploads backup directory", "err", err, "dir", backupDir)
	}

	// The data and files are committed from here on. Any remaining problem is a
	// warning, not a failed restore.
	if space == ledspace.Unresolved {
		s.logger.Warn("restored backup has an unresolved LED coordinate space; drawer allocation backfill skipped")
		return &RestoreCommittedError{Reason: "restored LED coordinate space is unresolved; drawer allocations were not derived"}
	}
	if space == ledspace.Drawer {
		// Drawer-relative bins already carry their own allocations, and the
		// segment-based backfill would derive meaningless ranges from them.
		s.logger.Warn("restored backup is drawer-relative; segment-based drawer allocation backfill skipped")
		return nil
	}

	// Establish a usable drawer allocation state before reporting success. This
	// derives allocations for restored drawers that do not have one (for example
	// legacy backups that predate drawer allocations) so drawer locating works
	// immediately without a restart. It never runs a coordinate conversion, so
	// restored bin indices are never shifted twice. Ambiguous mappings are left
	// untouched and reported as warnings by the backfill rather than guessed.
	if err := hardware.BackfillDrawerAllocations(ctx, s.store, s.logger); err != nil {
		s.logger.Error("post-restore drawer allocation backfill failed", "err", err)
		return &RestoreCommittedError{Reason: "drawer allocation backfill failed: " + err.Error()}
	}

	return nil
}

// auditRawValue returns the raw JSON for a stored audit value, or nil (which is
// omitted from the manifest) when the column is SQL NULL. This preserves SQL NULL
// distinctly from a JSON null ("null") or an empty object ("{}").
func auditRawValue(v []byte, isNull bool) json.RawMessage {
	if isNull {
		return nil
	}
	return json.RawMessage(v)
}

// auditJSONValue converts a raw JSON audit value into a value SQLite can store.
// An absent value (SQL NULL on export) becomes SQL NULL; any present value is
// stored as its raw JSON text, so SQL NULL, JSON null, empty objects, arrays,
// strings, numbers and booleans all round-trip with their original meaning. The
// read queries coalesce a SQL NULL value to '{}' for display.
func auditJSONValue(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	return []byte(raw)
}

// resolveBinIndexSpace determines the coordinate system of a backup's bin LED
// indices. It never infers the space from index values or timestamps: an absent
// marker on a backup that contains bins is unresolved. A backup with no bins has
// no coordinate system to misinterpret, so it is treated as resolved.
func resolveBinIndexSpace(m Manifest) (string, error) {
	switch m.BinIndexSpace {
	case ledspace.Segment:
		return ledspace.Segment, nil
	case ledspace.Drawer:
		return ledspace.Drawer, nil
	case ledspace.Unresolved:
		return ledspace.Unresolved, nil
	case "":
		if len(m.Bins) == 0 {
			return ledspace.Segment, nil
		}
		return ledspace.Unresolved, nil
	default:
		return "", fmt.Errorf("unsupported backup bin_index_space %q", m.BinIndexSpace)
	}
}

func (s *service) restoreData(ctx context.Context, qtx db.Querier, manifest Manifest) error {
	// Settings
	err := qtx.RestoreSettings(ctx, db.RestoreSettingsParams{
		RequireAuthForRead:    manifest.Settings.RequireAuthForRead,
		LocateTimeoutSeconds:  manifest.Settings.LocateTimeoutSeconds,
		EnableLocateTimeout:   manifest.Settings.EnableLocateTimeout,
		ColorLocate:           manifest.Settings.ColorLocate,
		ColorStockOk:          manifest.Settings.ColorStockOk,
		ColorStockLow:         manifest.Settings.ColorStockLow,
		ColorStockCritical:    manifest.Settings.ColorStockCritical,
		CreatedAt:             manifest.Settings.CreatedAt,
		UpdatedAt:             manifest.Settings.UpdatedAt,
		SupplierCacheTtlHours: manifest.Settings.SupplierCacheTtlHours,
		DefaultCurrency:       manifest.Settings.DefaultCurrency,
	})
	if err != nil {
		return fmt.Errorf("settings restore: %w", err)
	}

	for _, u := range manifest.Users {
		if err := qtx.RestoreUser(ctx, db.RestoreUserParams(u)); err != nil {
			return fmt.Errorf("user restore: %w", err)
		}
	}
	for _, c := range manifest.Controllers {
		if err := qtx.RestoreController(ctx, db.RestoreControllerParams(c)); err != nil {
			return fmt.Errorf("controller restore: %w", err)
		}
	}
	for _, c := range manifest.Containers {
		if err := qtx.RestoreContainer(ctx, db.RestoreContainerParams{
			ID:            c.ID,
			Name:          c.Name,
			ControllerID:  c.ControllerID,
			SegmentID:     c.SegmentID,
			ConfigJson:    c.ConfigJson,
			PositionIndex: c.PositionIndex,
			LedStart:      c.LedStart,
			LedCount:      c.LedCount,
			CreatedAt:     c.CreatedAt,
			UpdatedAt:     c.UpdatedAt,
		}); err != nil {
			return fmt.Errorf("container restore: %w", err)
		}
	}
	for _, w := range manifest.Walls {
		if err := qtx.RestoreWall(ctx, db.RestoreWallParams{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description,
			CreatedAt:   w.CreatedAt,
			UpdatedAt:   w.UpdatedAt,
		}); err != nil {
			return fmt.Errorf("wall restore: %w", err)
		}
	}
	for _, wc := range manifest.WallCards {
		if err := qtx.RestoreWallCard(ctx, db.RestoreWallCardParams{
			ID:            wc.ID,
			WallID:        wc.WallID,
			ContainerID:   wc.ContainerID,
			PositionIndex: wc.PositionIndex,
			ConfigJson:    wc.ConfigJson,
		}); err != nil {
			return fmt.Errorf("wall_card restore: %w", err)
		}
	}
	for _, b := range manifest.Bins {
		if err := qtx.RestoreBin(ctx, db.RestoreBinParams(b)); err != nil {
			return fmt.Errorf("bin restore: %w", err)
		}
	}
	for _, p := range manifest.Parts {
		if err := qtx.RestorePart(ctx, db.RestorePartParams{
			ID:                p.ID,
			Name:              p.Name,
			Description:       p.Description,
			PartNumber:        p.PartNumber,
			Manufacturer:      p.Manufacturer,
			Supplier:          p.Supplier,
			UnitCost:          p.UnitCost,
			ReorderLevel:      p.ReorderLevel,
			MinStockThreshold: p.MinStockThreshold,
			ImagePath:         p.ImagePath,
			BarcodeData:       p.BarcodeData,
			IsFavorite:        p.IsFavorite,
			Tags:              p.Tags,
			Footprint:         p.Footprint,
			CreatedAt:         p.CreatedAt,
			UpdatedAt:         p.UpdatedAt,
		}); err != nil {
			return fmt.Errorf("part restore: %w", err)
		}
	}
	for _, a := range manifest.PartAssignments {
		if err := qtx.RestorePartAssignment(ctx, db.RestorePartAssignmentParams(a)); err != nil {
			return fmt.Errorf("assignment restore: %w", err)
		}
	}
	for _, l := range manifest.PartLinks {
		if err := qtx.RestorePartLink(ctx, db.RestorePartLinkParams(l)); err != nil {
			return fmt.Errorf("link restore: %w", err)
		}
	}
	for _, d := range manifest.PartDocs {
		if err := qtx.RestorePartDoc(ctx, db.RestorePartDocParams(d)); err != nil {
			return fmt.Errorf("doc restore: %w", err)
		}
	}
	for _, p := range manifest.PartAiPrompts {
		if err := qtx.RestorePartAiPrompt(ctx, db.RestorePartAiPromptParams(p)); err != nil {
			return fmt.Errorf("prompt restore: %w", err)
		}
	}
	for _, t := range manifest.Tags {
		if err := qtx.RestoreTag(ctx, db.RestoreTagParams{
			ID:   t.ID,
			Name: t.Name,
		}); err != nil {
			return fmt.Errorf("tag restore: %w", err)
		}
	}
	for _, pt := range manifest.PartTags {
		if err := qtx.RestorePartTag(ctx, db.RestorePartTagParams(pt)); err != nil {
			return fmt.Errorf("part_tag restore: %w", err)
		}
	}
	for _, l := range manifest.AuditLogs {
		if err := qtx.RestoreAuditLog(ctx, db.RestoreAuditLogParams{
			ID:         l.ID,
			UserID:     l.UserID,
			ActionType: l.ActionType,
			EntityType: l.EntityType,
			EntityID:   l.EntityID,
			Details:    l.Details,
			OldValue:   auditJSONValue(l.OldValue),
			NewValue:   auditJSONValue(l.NewValue),
			CreatedAt:  l.CreatedAt,
		}); err != nil {
			return fmt.Errorf("audit log restore: %w", err)
		}
	}

	// Restore supplier data
	for _, sr := range manifest.SupplierRefs {
		if err := qtx.RestoreSupplierReference(ctx, db.RestoreSupplierReferenceParams{
			ID:          sr.ID,
			PartID:      sr.PartID,
			ProviderKey: sr.ProviderKey,
			ProviderID:  sr.ProviderID,
			ProviderUrl: sr.ProviderUrl,
			CreatedAt:   sr.CreatedAt,
		}); err != nil {
			return fmt.Errorf("supplier reference restore: %w", err)
		}
	}
	for _, pp := range manifest.PartParameters {
		if err := qtx.RestorePartParameter(ctx, db.RestorePartParameterParams{
			ID:         pp.ID,
			PartID:     pp.PartID,
			Name:       pp.Name,
			ValueText:  pp.ValueText,
			ValueTyp:   pp.ValueTyp,
			ValueMin:   pp.ValueMin,
			ValueMax:   pp.ValueMax,
			Unit:       pp.Unit,
			Symbol:     pp.Symbol,
			ParamGroup: pp.ParamGroup,
		}); err != nil {
			return fmt.Errorf("part parameter restore: %w", err)
		}
	}
	for _, pr := range manifest.PartPricing {
		if err := qtx.RestorePartPricing(ctx, db.RestorePartPricingParams{
			ID:            pr.ID,
			PartID:        pr.PartID,
			SupplierRefID: pr.SupplierRefID,
			MinQuantity:   pr.MinQuantity,
			Price:         pr.Price,
			Currency:      pr.Currency,
			IncludesTax:   pr.IncludesTax,
		}); err != nil {
			return fmt.Errorf("part pricing restore: %w", err)
		}
	}
	for _, sc := range manifest.SupplierCredentials {
		if err := qtx.RestoreSupplierCredential(ctx, db.RestoreSupplierCredentialParams{
			ID:             sc.ID,
			ProviderKey:    sc.ProviderKey,
			ApiKey:         sc.ApiKey,
			ApiSecret:      sc.ApiSecret,
			AccessToken:    sc.AccessToken,
			RefreshToken:   sc.RefreshToken,
			TokenExpiresAt: sc.TokenExpiresAt,
			IsActive:       sc.IsActive,
			CreatedAt:      sc.CreatedAt,
			UpdatedAt:      sc.UpdatedAt,
		}); err != nil {
			return fmt.Errorf("supplier credential restore: %w", err)
		}
	}
	for _, ph := range manifest.PriceHistory {
		if err := qtx.RestorePriceHistory(ctx, db.RestorePriceHistoryParams{
			ID:            ph.ID,
			PartID:        ph.PartID,
			SupplierRefID: ph.SupplierRefID,
			MinQuantity:   ph.MinQuantity,
			Price:         ph.Price,
			Currency:      ph.Currency,
			IncludesTax:   ph.IncludesTax,
			RecordedAt:    ph.RecordedAt,
		}); err != nil {
			return fmt.Errorf("price history restore: %w", err)
		}
	}

	return nil
}
