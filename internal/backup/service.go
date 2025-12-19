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
)

type Service interface {
	Export(ctx context.Context, w io.Writer) error
	Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error
}

type service struct {
	db         *sql.DB
	queries    *db.Queries
	uploadsDir string
	logger     *slog.Logger
}

func NewService(database *sql.DB, queries *db.Queries, uploadsDir string, logger *slog.Logger) Service {
	return &service{
		db:         database,
		queries:    queries,
		uploadsDir: uploadsDir,
		logger:     logger,
	}
}

func (s *service) Export(ctx context.Context, w io.Writer) error {
	// Fetch Data
	settings, err := s.queries.GetSettings(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to fetch settings: %w", err)
	}

	users, _ := s.queries.GetAllUsers(ctx)
	controllers, _ := s.queries.GetControllers(ctx)
	bins, _ := s.queries.GetAllBins(ctx)
	parts, _ := s.queries.GetAllParts(ctx)
	assignments, _ := s.queries.GetAllPartAssignments(ctx)
	links, _ := s.queries.GetAllPartLinks(ctx)
	docs, _ := s.queries.GetAllPartDocs(ctx)
	prompts, _ := s.queries.GetAllPartAiPrompts(ctx)
	logs, _ := s.queries.GetAllAuditLogs(ctx)

	manifest := Manifest{
		Version:         "1.0",
		ExportedAt:      time.Now(),
		Settings:        settings,
		Users:           users,
		Controllers:     controllers,
		Bins:            bins,
		Parts:           parts,
		PartAssignments: assignments,
		PartLinks:       links,
		PartDocs:        docs,
		PartAiPrompts:   prompts,
		AuditLogs:       logs,
	}

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Add restore_data.json
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
	err = filepath.Walk(s.uploadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// If uploads dir doesn't exist, just skip
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			return nil
		}

		relInZip, _ := filepath.Rel(s.uploadsDir, path)
		zipPath := filepath.Join("uploads", relInZip)

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
		s.logger.Error("backup failed to zip uploads", "error", err)
		// don't fail the whole backup for this, but maybe we should?
		// Keeping existing behavior: return nil if zip succeeds generally
	}

	// Log audit
	audit.Log(ctx, s.queries, "BACKUP", "SYSTEM", 0, "Downloaded system backup", nil, nil)
	return nil
}

func (s *service) Restore(ctx context.Context, zipReader io.ReaderAt, size int64) error {
	zr, err := zip.NewReader(zipReader, size)
	if err != nil {
		return fmt.Errorf("invalid ZIP file: %w", err)
	}

	// Validation: Find & Parse JSON Manifest
	var manifest Manifest
	var manifestFound bool

	for _, f := range zr.File {
		if f.Name == "restore_data.json" {
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

	// Preparation: Extract Uploads to Temp Directory
	timestamp := time.Now().UnixNano()
	// Use uploadsDir parent to keep temp folders in "app/" root
	// app/uploads -> app/restore_tmp_123
	appDir := filepath.Dir(s.uploadsDir)
	tempDir := filepath.Join(appDir, fmt.Sprintf("restore_tmp_%d", timestamp))

	defer os.RemoveAll(tempDir)

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp restore dir: %w", err)
	}

	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "uploads/") && !f.FileInfo().IsDir() {
			relPath := strings.TrimPrefix(f.Name, "uploads/")
			targetPath := filepath.Join(tempDir, relPath)

			// Security check
			if !strings.HasPrefix(filepath.Clean(targetPath), tempDir) {
				continue
			}

			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return fmt.Errorf("failed to create temp subdir: %w", err)
			}

			outFile, err := os.Create(targetPath)
			if err != nil {
				return fmt.Errorf("failed to create temp file: %w", err)
			}

			rc, err := f.Open()
			if err != nil {
				outFile.Close()
				return fmt.Errorf("failed to open zip file entry: %w", err)
			}
			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				return fmt.Errorf("failed to write temp file: %w", err)
			}
		}
	}

	// Database Restore Transaction
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("db begin error: %w", err)
	}
	defer tx.Rollback()
	qtx := s.queries.WithTx(tx)

	if _, err := tx.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("failed to disable FKs: %w", err)
	}

	tables := []string{
		"part_assignments", "part_links", "part_docs", "part_ai_prompts", "part_tags",
		"parts", "bins", "controllers", "audit_logs", "sessions", "users", "settings",
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			return fmt.Errorf("failed to clear table %s: %w", table, err)
		}
	}

	// Restore Data
	if err := s.restoreData(ctx, qtx, manifest); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("failed to re-enable FKs: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit failed: %w", err)
	}

	// Atomic Swap of Assets
	liveUploads := s.uploadsDir
	backupUploads := filepath.Join(appDir, fmt.Sprintf("uploads_bak_%d", timestamp))

	if err := os.Rename(liveUploads, backupUploads); err != nil {
		// If live dir doesn't exist (first run), just ignore
		if !os.IsNotExist(err) {
			s.logger.Error("CRITICAL: Failed to move live uploads to backup.", "error", err)
			return fmt.Errorf("file system swap failed (DB restored): %w", err)
		}
	}

	if err := os.Rename(tempDir, liveUploads); err != nil {
		s.logger.Error("CRITICAL: Failed to move temp uploads to live.", "error", err)
		// Rollback Backup -> Live
		if recErr := os.Rename(backupUploads, liveUploads); recErr != nil {
			s.logger.Error("FATAL: Failed to restore backup uploads!", "error", recErr)
		}
		return fmt.Errorf("file system error during swap: %w", err)
	}

	os.RemoveAll(backupUploads)
	return nil
}

func (s *service) restoreData(ctx context.Context, qtx *db.Queries, manifest Manifest) error {
	// Settings
	err := qtx.RestoreSettings(ctx, db.RestoreSettingsParams{
		RequireAuthForRead:   manifest.Settings.RequireAuthForRead,
		LocateTimeoutSeconds: manifest.Settings.LocateTimeoutSeconds,
		EnableLocateTimeout:  manifest.Settings.EnableLocateTimeout,
		ColorLocate:          manifest.Settings.ColorLocate,
		ColorStockOk:         manifest.Settings.ColorStockOk,
		ColorStockLow:        manifest.Settings.ColorStockLow,
		ColorStockCritical:   manifest.Settings.ColorStockCritical,
		CreatedAt:            manifest.Settings.CreatedAt,
		UpdatedAt:            manifest.Settings.UpdatedAt,
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
	for _, l := range manifest.AuditLogs {
		if err := qtx.RestoreAuditLog(ctx, db.RestoreAuditLogParams(l)); err != nil {
			return fmt.Errorf("audit log restore: %w", err)
		}
	}
	return nil
}
