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
	// Fetch Data
	settings, err := s.store.GetSettings(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to fetch settings: %w", err)
	}

	users, _ := s.store.GetAllUsers(ctx)
	controllers, _ := s.store.GetControllers(ctx)
	containers, _ := s.store.GetAllContainers(ctx)
	walls, _ := s.store.GetWalls(ctx)
	wallCards, _ := s.store.GetAllWallCards(ctx)
	bins, _ := s.store.GetAllBins(ctx)
	parts, _ := s.store.GetAllParts(ctx)
	assignments, _ := s.store.GetAllPartAssignments(ctx)
	links, _ := s.store.GetAllPartLinks(ctx)
	docs, _ := s.store.GetAllPartDocs(ctx)
	prompts, _ := s.store.GetAllPartAiPrompts(ctx)
	logs, _ := s.store.GetAllAuditLogs(ctx)
	var auditLogs []db.AuditLog
	for _, l := range logs {
		auditLogs = append(auditLogs, db.AuditLog{
			ID:         l.ID,
			UserID:     l.UserID,
			ActionType: l.ActionType,
			EntityType: l.EntityType,
			EntityID:   l.EntityID,
			Details:    l.Details,
			OldValue:   json.RawMessage(l.OldValue),
			NewValue:   json.RawMessage(l.NewValue),
			CreatedAt:  l.CreatedAt,
		})
	}
	tags, _ := s.store.ListAllTags(ctx)
	partTags, _ := s.store.GetAllPartTags(ctx)

	// Supplier data
	supplierRefs, _ := s.store.GetAllSupplierReferences(ctx)
	partParameters, _ := s.store.GetAllPartParameters(ctx)
	partPricingRows, _ := s.store.GetAllPartPricing(ctx)
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
	supplierCredentials, _ := s.store.GetAllSupplierCredentials(ctx)
	priceHistory, _ := s.store.GetAllPriceHistory(ctx)

	// Export the actual coordinate space of the stored bin LED indices so a
	// restore can interpret them without guessing.
	space, err := ledspace.Current(ctx, s.store)
	if err != nil {
		return fmt.Errorf("failed to read LED coordinate space: %w", err)
	}

	manifest := Manifest{
		Version:             "1.0",
		ExportedAt:          time.Now(),
		BinIndexSpace:       space,
		Settings:            settings,
		Users:               users,
		Controllers:         controllers,
		Containers:          containers,
		Walls:               walls,
		WallCards:           wallCards,
		Bins:                bins,
		Parts:               parts,
		PartAssignments:     assignments,
		PartLinks:           links,
		PartDocs:            docs,
		PartAiPrompts:       prompts,
		Tags:                tags,
		PartTags:            partTags,
		AuditLogs:           auditLogs,
		SupplierRefs:        supplierRefs,
		PartParameters:      partParameters,
		PartPricing:         partPricing,
		SupplierCredentials: supplierCredentials,
		PriceHistory:        priceHistory,
	}

	zw := zip.NewWriter(w)
	defer zw.Close()

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
	fCsv, err := zw.Create("human_readable_parts.csv")
	if err == nil {
		cw := csv.NewWriter(fCsv)
		cw.Write([]string{"Name", "Description", "Part Number", "Manufacturer", "Supplier", "Unit Cost", "Reorder Level", "Min Stock", "Barcode", "Quantity"})
		for _, p := range parts {
			var total int64
			for _, a := range assignments {
				if a.PartID == p.ID {
					total += a.Quantity
				}
			}
			cw.Write([]string{
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
			})
		}
		cw.Flush()
	}

	// Add Uploads
	s.logger.Debug("collecting upload files for backup", "dir", s.uploadsDir)
	err = filepath.Walk(s.uploadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// If uploads dir doesn't exist, just skip
			if os.IsNotExist(err) {
				s.logger.Debug("uploads directory not found, skipping", "path", s.uploadsDir)
				return nil
			}
			return err
		}

		// Ignore hidden files/directories (like .restore_tmp, .uploads_bak, .git, etc.)
		if strings.HasPrefix(info.Name(), ".") && path != s.uploadsDir {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.IsDir() {
			return nil
		}

		relInZip, _ := filepath.Rel(s.uploadsDir, path)
		zipPath := filepath.Join("uploads", relInZip)

		s.logger.Debug("adding file to backup zip", "path", path, "zip_path", zipPath)
		zf, err := zw.Create(zipPath)
		if err != nil {
			return err
		}

		fsFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fsFile.Close()

		_, err = io.Copy(zf, fsFile)
		return err
	})

	if err != nil {
		s.logger.Error("backup failed to zip uploads", "err", err)
		// TODO: implement better handling of this case. Ensure a uierror partial is
		// returned in the frontend (or an error toast) for the user.
	}

	// Log audit
	audit.Log(ctx, s.store, "BACKUP", "SYSTEM", 0, "Downloaded system backup", nil, nil)
	// return nil if zip succeeds
	return nil
}

func (s *service) cleanOldTempFiles() {
	entries, err := os.ReadDir(s.uploadsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && (strings.HasPrefix(entry.Name(), ".restore_tmp_") || strings.HasPrefix(entry.Name(), ".uploads_bak_")) {
			s.logger.Info("cleaning up old temporary restore directory", "name", entry.Name())
			os.RemoveAll(filepath.Join(s.uploadsDir, entry.Name()))
		}
	}
}

func (s *service) Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error {
	s.logger.Debug("starting system restore", "size", size)
	s.cleanOldTempFiles()

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

			// Security check
			if !strings.HasPrefix(filepath.Clean(targetPath), tempDir) {
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

	// Database Restore Transaction
	err = s.store.ExecTx(ctx, func(qtx db.Querier) error {
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

	// Atomic swap of assets
	s.logger.Debug("swapping upload contents", "dir", s.uploadsDir)

	// Create backup folder inside uploadsDir
	backupDir := filepath.Join(s.uploadsDir, fmt.Sprintf(".uploads_bak_%d", timestamp))
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup dir: %w", err)
	}
	defer os.RemoveAll(backupDir)

	// Helper to move contents
	moveContents := func(src, dst string) error {
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			// skip the temp directories
			if entry.Name() == filepath.Base(tempDir) || entry.Name() == filepath.Base(backupDir) {
				continue
			}
			srcPath := filepath.Join(src, entry.Name())
			dstPath := filepath.Join(dst, entry.Name())
			if err := os.Rename(srcPath, dstPath); err != nil {
				return err
			}
		}
		return nil
	}

	// Move current contents to backup
	if err := moveContents(s.uploadsDir, backupDir); err != nil {
		return fmt.Errorf("failed to move current uploads to backup: %w", err)
	}

	// Move new contents from temp to live
	if err := moveContents(tempDir, s.uploadsDir); err != nil {
		s.logger.Error("failed to move new uploads to live, attempting rollback", "err", err)
		// Rollback
		if rbErr := moveContents(backupDir, s.uploadsDir); rbErr != nil {
			s.logger.Error("FATAL: rollback failed", "err", rbErr)
		}
		return fmt.Errorf("failed to swap new uploads: %w", err)
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
		if err := qtx.RestoreAuditLog(ctx, db.RestoreAuditLogParams(l)); err != nil {
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
