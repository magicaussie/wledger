package backup

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/maintenance"
)

const (
	maxRestoreArchiveBytes   = 100 << 20
	maxRestoreArchiveEntries = 10_000
	maxRestoreManifestBytes  = 32 << 20
	maxRestoreFileBytes      = 50 << 20
	maxRestoreUploadsBytes   = 512 << 20
)

type Service interface {
	Export(ctx context.Context, w io.Writer) error
	Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error
}

// renameEntry is os.Rename, indirected so tests can inject filesystem failures
// into the uploads swap.
var (
	renameEntry                   = os.Rename
	afterRestoreTransactionCommit = func() {}
)

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

// RestoreRecoveryError means neither the current process nor startup recovery
// could prove a consistent active database/assets generation. Normal requests are
// blocked and the durable journal and generation directories must be preserved.
type RestoreRecoveryError struct {
	Reason string
}

func (e *RestoreRecoveryError) Error() string {
	return "restore recovery required: " + e.Reason
}

type service struct {
	db         *sql.DB
	store      db.Store
	uploadsDir string
	logger     *slog.Logger
	gate       *maintenance.Gate

	// restoreMu remains a local guard for direct service calls. The maintenance
	// gate also coordinates normal requests and other server processes.
	restoreMu sync.Mutex
}

func NewService(database *sql.DB, store db.Store, uploadsDir string, logger *slog.Logger, gates ...*maintenance.Gate) Service {
	absoluteUploads, err := filepath.Abs(uploadsDir)
	if err == nil {
		uploadsDir = absoluteUploads
	}
	gate := maintenance.New(uploadsDir)
	if len(gates) > 0 && gates[0] != nil {
		gate = gates[0]
	}
	return &service{
		db:         database,
		store:      store,
		uploadsDir: uploadsDir,
		logger:     logger,
		gate:       gate,
	}
}

func (s *service) Export(ctx context.Context, w io.Writer) error {
	s.logger.Debug("starting system backup export")

	// Read the entire backup inside a single transaction so the manifest is a
	// consistent snapshot: a concurrent write cannot make one entity reference
	// another that the same manifest is missing. Any read failure aborts the
	// export instead of being reported as a successful, incomplete backup.
	// One snapshot yields the complete historical manifest, the physical storage
	// graph and every system flag.
	env, err := s.snapshotStorageManifest(ctx)
	if err != nil {
		return err
	}
	zw := newBackupZipWriter(w)

	// Add storage_data.json. A current backup writes exactly one manifest entry:
	// restore_data.json is no longer emitted, so an older build fails on the
	// missing legacy entry instead of silently ignoring physical storage it
	// cannot read.
	s.logger.Debug("adding storage_data.json to backup")
	fJson, err := zw.Create(currentStorageManifestName)
	if err != nil {
		return fmt.Errorf("failed to create json entry: %w", err)
	}
	enc := json.NewEncoder(fJson)
	enc.SetIndent("", "  ")
	if err := enc.Encode(env); err != nil {
		return fmt.Errorf("failed to encode json: %w", err)
	}

	// add human_readable_parts.csv
	s.logger.Debug("adding human_readable_parts.csv to backup")
	if err := writePartsCSV(zw, env.Legacy.Parts, env.Legacy.PartAssignments); err != nil {
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

// snapshotStorageManifest reads everything a current backup carries inside a
// single transaction: the complete historical manifest, the physical storage
// graph and every system flag. One transaction yields one consistent snapshot,
// so a concurrent write cannot make one entity reference another that the same
// snapshot omits. No snapshot is split across transactions and no nested
// transaction is used. Every read is checked; a failure aborts the export
// instead of silently producing an empty or partial manifest.
func (s *service) snapshotStorageManifest(ctx context.Context) (StorageManifest, error) {
	var env StorageManifest
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		var m Manifest
		m.Version = supportedLegacyManifestVersion
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

		// Read the physical graph and every system flag through the same querier, so
		// the one transaction covers the whole installation rather than only the
		// legacy tables.
		cabinets, err := q.ListCabinetsIncludingArchived(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch cabinets: %w", err)
		}
		drawers, err := q.ListAllDrawers(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch drawers: %w", err)
		}
		attachments, err := q.ListBinDrawerAttachments(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch bin attachments: %w", err)
		}
		ledAllocations, err := q.ListAllDrawerLEDAllocations(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch drawer LED allocations: %w", err)
		}
		cabinetControllers, err := q.ListAllCabinetControllers(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch cabinet controllers: %w", err)
		}
		cabinetLEDStrings, err := q.ListAllCabinetLEDStrings(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch cabinet LED strings: %w", err)
		}
		cabinetLEDPathSections, err := q.ListAllCabinetLEDPathSections(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch cabinet LED path sections: %w", err)
		}
		flags, err := q.GetAllSystemFlags(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch system flags: %w", err)
		}
		// An empty graph and flag list are captured as empty arrays rather than
		// null, so the current format always declares its physical storage and flag
		// set explicitly instead of leaving them unknown.
		if cabinets == nil {
			cabinets = []db.Cabinet{}
		}
		if drawers == nil {
			drawers = []db.Drawer{}
		}
		if attachments == nil {
			attachments = []db.BinDrawerAttachment{}
		}
		if ledAllocations == nil {
			ledAllocations = []db.DrawerLedAllocation{}
		}
		if cabinetControllers == nil {
			cabinetControllers = []db.CabinetController{}
		}
		if cabinetLEDStrings == nil {
			cabinetLEDStrings = []db.CabinetLedString{}
		}
		if cabinetLEDPathSections == nil {
			cabinetLEDPathSections = []db.CabinetLedPathSection{}
		}
		if flags == nil {
			flags = []db.SystemFlag{}
		}

		env = StorageManifest{
			Version:    supportedCurrentManifestVersion,
			ExportedAt: m.ExportedAt,
			Legacy:     m,
			PhysicalStorage: PhysicalStorageSnapshot{
				Cabinets:                   cabinets,
				Drawers:                    drawers,
				BinAttachments:             attachments,
				CabinetControllers:         cabinetControllers,
				LEDAllocations:             ledAllocations,
				CabinetLEDStrings:          cabinetLEDStrings,
				CabinetLEDPathSections:     cabinetLEDPathSections,
				AllocationCoordinateSpace:  "cabinet",
				LEDAllocationsDeclared:     true,
				CabinetControllersDeclared: true,
				CabinetWiringDeclared:      true,
			},
			SystemFlags: flags,
		}
		return nil
	})
	if err != nil {
		return StorageManifest{}, err
	}
	return env, nil
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

func (s *service) Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error {
	s.logger.Debug("starting system restore", "size", size)
	if s.gate.Unavailable() {
		return &RestoreRecoveryError{Reason: "this process is fail-closed after an earlier recovery error; restart the service to recover the pending journal"}
	}
	unlock, err := s.gate.WriteLock()
	if err != nil {
		return fmt.Errorf("acquire restore maintenance gate: %w", err)
	}
	defer unlock()
	s.restoreMu.Lock()
	defer s.restoreMu.Unlock()
	if err := recoverPendingRestore(ctx, s.db, s.uploadsDir, s.logger); err != nil {
		s.gate.Poison()
		return fmt.Errorf("recover previous restore before accepting another: %w", err)
	}

	if size <= 0 || size > maxRestoreArchiveBytes {
		return fmt.Errorf("invalid ZIP file size %d (maximum %d bytes)", size, maxRestoreArchiveBytes)
	}
	zr, err := zip.NewReader(zipReader, size)
	if err != nil {
		return fmt.Errorf("invalid ZIP file: %w", err)
	}
	if err := validateRestoreArchive(zr); err != nil {
		return err
	}

	// Validate the archive envelope before any state is changed: exactly one
	// supported manifest must be present, its JSON must be well-formed, and a
	// current manifest's physical storage graph must be internally consistent.
	// This runs before uploads/staging cleanup and before any database work, so an
	// unsupported, duplicate, ambiguous, malformed or inconsistent backup leaves
	// the installation untouched.
	envelope, err := readRestoreEnvelope(zr)
	if err != nil {
		return err
	}
	// A current archive declares its complete physical storage and may therefore
	// replace the target graph. A legacy archive declares none, so it is only
	// restorable into an installation that has no physical storage to lose.
	manifest := envelope.Legacy

	// Resolve the coordinate system of the backup's bin LED indices. An
	// unsupported explicit marker is rejected; an absent marker on a backup that
	// contains bins is treated as unresolved and is never inferred from index
	// values or timestamps.
	space, err := resolveBinIndexSpace(manifest)
	if err != nil {
		return err
	}

	// A legacy archive is interpreted in the coordinate space it declares, so its
	// bin indices are validated against the drawer allocations before anything is
	// changed: segment-relative and drawer-relative backups both validate bin
	// containment in their own coordinate space, and an unresolved backup only has
	// its allocation ranges checked, because bin containment cannot be assessed
	// without a known bin coordinate space.
	//
	// A current archive is a byte-exact capture of a running installation whose
	// flags are restored exactly, so the stored state is already authoritative:
	// the legacy interpretation heuristics are not applied to it. Its own physical
	// storage graph is validated by the envelope decode instead.
	if envelope.Current == nil {
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
	}

	// A legacy archive cannot reproduce physical storage, so restoring it over a
	// populated physical graph would discard that data irrecoverably. Refusing
	// here, before the uploads staging cleanup and before any database mutation,
	// leaves the installation's rows and files exactly as they were.
	if envelope.Current == nil {
		populated, err := physicalGraphPopulated(ctx, s.store)
		if err != nil {
			return err
		}
		if populated {
			return &PhysicalStorageConflictError{
				Reason: "the archive is a legacy " + legacyManifestName + " backup that carries no physical storage, but this installation already has cabinets, drawers or bin attachments",
			}
		}
	}
	activeSession, err := activePhysicalLocateSessionExists(ctx, s.store)
	if err != nil {
		return err
	}
	if activeSession {
		return &ActivePhysicalLocateConflictError{Reason: "a durable physical Locate session may still own pixels; stop and confirm cleanup before restoring a backup"}
	}
	if envelope.Current != nil && !envelope.Current.PhysicalStorage.LEDAllocationsDeclared {
		allocations, err := s.store.ListAllDrawerLEDAllocations(ctx)
		if err != nil {
			return fmt.Errorf("failed to check existing drawer LED allocations: %w", err)
		}
		if len(allocations) != 0 {
			return &DrawerLEDAllocationConflictError{Reason: "this older current-format backup does not contain drawer LED allocations, but this installation has saved mappings; export a new backup before restoring"}
		}
	}
	// A current archive that predates the membership table carries no secondary
	// controller assignments, so it can only reproduce each cabinet's own primary
	// controller. Refuse it over an installation that holds any other membership,
	// before the uploads staging cleanup and before any database mutation.
	if envelope.Current != nil && !envelope.Current.PhysicalStorage.CabinetControllersDeclared {
		secondary, err := secondaryCabinetMembershipsExist(ctx, s.store)
		if err != nil {
			return err
		}
		if secondary {
			return &CabinetControllerConflictError{Reason: "this older current-format backup does not contain cabinet controller memberships, but this installation has secondary controller assignments; export a new backup before restoring"}
		}
	}
	if envelope.Current != nil && !envelope.Current.PhysicalStorage.CabinetWiringDeclared {
		wiringExists, err := cabinetWiringExists(ctx, s.store)
		if err != nil {
			return err
		}
		if wiringExists {
			return &CabinetWiringConflictError{Reason: "this older current-format backup does not contain physical string/path wiring, but this installation has configured wiring; export a new backup before restoring"}
		}
	}

	// Build a complete candidate database against the current migration set before
	// touching the active uploads tree or beginning a live database transaction.
	generationBytes := make([]byte, 16)
	if _, err := rand.Read(generationBytes); err != nil {
		return fmt.Errorf("create restore generation ID: %w", err)
	}
	generation := hex.EncodeToString(generationBytes)
	if err := s.validateCandidateDatabase(ctx, manifest, envelope, space, generation); err != nil {
		return err
	}

	parent, tempDir, _, _ := generationPaths(s.uploadsDir, generation)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return fmt.Errorf("create uploads parent directory: %w", err)
	}
	s.logger.Debug("extracting uploads to candidate generation", "candidate_dir", tempDir)
	if err := os.Mkdir(tempDir, 0755); err != nil {
		return fmt.Errorf("create candidate uploads directory: %w", err)
	}
	journalWritten := false
	defer func() {
		if !journalWritten {
			_ = os.RemoveAll(tempDir)
		}
	}()

	var extractedBytes uint64
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "uploads/") && !f.FileInfo().IsDir() {
			relPath := strings.TrimPrefix(f.Name, "uploads/")
			targetPath := filepath.Join(tempDir, filepath.FromSlash(relPath))

			// Security check: reject any entry that would resolve outside the
			// staging directory. A relative-path test avoids the sibling-prefix
			// false negative of a raw string prefix comparison.
			if !withinDir(tempDir, targetPath) {
				return fmt.Errorf("invalid backup: upload path escapes staging directory: %q", f.Name)
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
			written, copyErr := io.Copy(outFile, io.LimitReader(rc, maxRestoreFileBytes+1))
			rc.Close()
			if copyErr == nil {
				copyErr = outFile.Sync()
			}
			closeErr := outFile.Close()
			if copyErr != nil {
				err = copyErr
			} else if closeErr != nil {
				err = closeErr
			} else if written > maxRestoreFileBytes {
				err = fmt.Errorf("extracted upload exceeds %d bytes", maxRestoreFileBytes)
			} else if extractedBytes+uint64(written) > maxRestoreUploadsBytes {
				err = fmt.Errorf("extracted uploads exceed %d bytes", maxRestoreUploadsBytes)
			} else {
				extractedBytes += uint64(written)
			}
			if err != nil {
				s.logger.Error("failed to write temp restore file", "err", err, "path", targetPath)
				return fmt.Errorf("failed to write temp file: %w", err)
			}
		}
	}

	if err := validateManifestAssetRefs(tempDir, manifest.Parts, manifest.PartDocs); err != nil {
		return fmt.Errorf("candidate upload references are invalid: %w", err)
	}
	if err := syncTree(tempDir); err != nil {
		return fmt.Errorf("make candidate uploads durable: %w", err)
	}
	hadUploads, err := pathExists(s.uploadsDir)
	if err != nil {
		return err
	}
	journal := restoreJournal{Version: 1, ID: generation, HadUploads: hadUploads}
	if err := writeRestoreJournal(s.uploadsDir, journal); err != nil {
		return err
	}
	journalWritten = true

	// Database Restore Transaction. The write reservation is taken up front
	// (BEGIN IMMEDIATE) so a concurrent coordinate conversion cannot commit
	// between the restore's reads and its writes.
	err = s.store.ExecImmediateTx(ctx, func(qtx db.Querier) error {
		// Re-check durable Locate ownership under the write reservation. This
		// protects direct restore callers as well as the handler's in-process lease.
		activeSession, err := activePhysicalLocateSessionExists(ctx, qtx)
		if err != nil {
			return err
		}
		if activeSession {
			return &ActivePhysicalLocateConflictError{Reason: "a durable physical Locate session may still own pixels; stop and confirm cleanup before restoring a backup"}
		}
		// Re-check inside the write transaction so a physical graph that appeared
		// after the pre-restore check is refused atomically rather than silently
		// cleared.
		if envelope.Current == nil {
			populated, err := physicalGraphPopulated(ctx, qtx)
			if err != nil {
				return err
			}
			if populated {
				return &PhysicalStorageConflictError{
					Reason: "the archive is a legacy " + legacyManifestName + " backup that carries no physical storage, but this installation already has cabinets, drawers or bin attachments",
				}
			}
		}
		if envelope.Current != nil && !envelope.Current.PhysicalStorage.LEDAllocationsDeclared {
			allocations, err := qtx.ListAllDrawerLEDAllocations(ctx)
			if err != nil {
				return fmt.Errorf("failed to recheck existing drawer LED allocations: %w", err)
			}
			if len(allocations) != 0 {
				return &DrawerLEDAllocationConflictError{Reason: "this older current-format backup does not contain drawer LED allocations, but this installation has saved mappings; export a new backup before restoring"}
			}
		}
		if envelope.Current != nil && !envelope.Current.PhysicalStorage.CabinetControllersDeclared {
			secondary, err := secondaryCabinetMembershipsExist(ctx, qtx)
			if err != nil {
				return err
			}
			if secondary {
				return &CabinetControllerConflictError{Reason: "this older current-format backup does not contain cabinet controller memberships, but this installation has secondary controller assignments; export a new backup before restoring"}
			}
		}
		if envelope.Current != nil && !envelope.Current.PhysicalStorage.CabinetWiringDeclared {
			wiringExists, err := cabinetWiringExists(ctx, qtx)
			if err != nil {
				return err
			}
			if wiringExists {
				return &CabinetWiringConflictError{Reason: "this older current-format backup does not contain physical string/path wiring, but this installation has configured wiring; export a new backup before restoring"}
			}
		}
		if err := s.applyRestoredDatabase(ctx, qtx, manifest, envelope, space, generation); err != nil {
			return err
		}
		if err := finishGeneration(s.uploadsDir, journal); err != nil {
			return fmt.Errorf("activate candidate uploads: %w", err)
		}
		return nil
	})

	if err != nil {
		s.logger.Error("restore transaction failed", "err", err)
		if recoveryErr := recoverPendingRestore(ctx, s.db, s.uploadsDir, s.logger); recoveryErr != nil {
			s.gate.Poison()
			return &RestoreRecoveryError{Reason: fmt.Sprintf("restore transaction failed (%v) and automatic recovery could not establish a consistent generation (%v); restart after preserving the pending journal and generation directories", err, recoveryErr)}
		}
		return err
	}

	afterRestoreTransactionCommit()
	if err := recoverPendingRestore(ctx, s.db, s.uploadsDir, s.logger); err != nil {
		s.gate.Poison()
		return &RestoreRecoveryError{Reason: fmt.Sprintf("restore committed but generation verification/recovery failed (%v); requests are blocked until startup recovery succeeds", err)}
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

	// A current backup already carries its complete derived state, so no
	// allocation may be derived for it: the restore reproduces the backed-up
	// system exactly, including every flag.
	if envelope.Current != nil {
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

func validateRestoreArchive(zr *zip.Reader) error {
	if len(zr.File) > maxRestoreArchiveEntries {
		return fmt.Errorf("invalid backup: archive has %d entries (maximum %d)", len(zr.File), maxRestoreArchiveEntries)
	}

	seenUploads := make(map[string]struct{})
	var declaredUploads uint64
	for _, entry := range zr.File {
		mode := entry.Mode()
		if mode&os.ModeType != 0 && !mode.IsRegular() && !mode.IsDir() {
			return fmt.Errorf("invalid backup: unsupported archive entry type for %q", entry.Name)
		}
		if entry.Name == legacyManifestName || entry.Name == currentStorageManifestName {
			if entry.UncompressedSize64 > maxRestoreManifestBytes {
				return fmt.Errorf("invalid backup: manifest exceeds %d bytes", maxRestoreManifestBytes)
			}
		}
		if !strings.HasPrefix(entry.Name, "uploads/") || entry.FileInfo().IsDir() {
			continue
		}

		rel := strings.TrimPrefix(entry.Name, "uploads/")
		clean := path.Clean(rel)
		if rel == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) || strings.Contains(rel, "\\") || clean != rel {
			return fmt.Errorf("invalid backup: unsafe upload path %q", entry.Name)
		}
		if _, exists := seenUploads[clean]; exists {
			return fmt.Errorf("invalid backup: duplicate upload path %q", entry.Name)
		}
		seenUploads[clean] = struct{}{}
		if entry.UncompressedSize64 > maxRestoreFileBytes {
			return fmt.Errorf("invalid backup: upload %q exceeds %d bytes", entry.Name, maxRestoreFileBytes)
		}
		declaredUploads += entry.UncompressedSize64
		if declaredUploads > maxRestoreUploadsBytes {
			return fmt.Errorf("invalid backup: extracted uploads exceed %d bytes", maxRestoreUploadsBytes)
		}
	}
	return nil
}

func (s *service) restoreData(ctx context.Context, qtx db.Querier, manifest Manifest) error {
	// Settings
	err := qtx.RestoreSettings(ctx, db.RestoreSettingsParams{
		RequireAuthForRead:      manifest.Settings.RequireAuthForRead,
		LocateTimeoutSeconds:    manifest.Settings.LocateTimeoutSeconds,
		EnableLocateTimeout:     manifest.Settings.EnableLocateTimeout,
		EnableDebugLogs:         manifest.Settings.EnableDebugLogs,
		ColorLocate:             manifest.Settings.ColorLocate,
		ColorStockOk:            manifest.Settings.ColorStockOk,
		ColorStockLow:           manifest.Settings.ColorStockLow,
		ColorStockCritical:      manifest.Settings.ColorStockCritical,
		ColorError:              manifest.Settings.ColorError,
		CreatedAt:               manifest.Settings.CreatedAt,
		UpdatedAt:               manifest.Settings.UpdatedAt,
		SupplierCacheTtlHours:   manifest.Settings.SupplierCacheTtlHours,
		DefaultCurrency:         manifest.Settings.DefaultCurrency,
		InspirationSeedsApplied: manifest.Settings.InspirationSeedsApplied,
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
		if err := qtx.RestoreController(ctx, db.RestoreControllerParams{
			ID: c.ID, Name: c.Name, IpAddress: c.IpAddress, Port: c.Port,
			MacAddress: c.MacAddress, IsOnline: c.IsOnline, LedCount: c.LedCount,
			CreatedAt: c.CreatedAt,
		}); err != nil {
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
