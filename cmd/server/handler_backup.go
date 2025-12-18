package main

import (
	"archive/zip"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/components"
)

type BackupManifest struct {
	Version         string              `json:"version"`
	ExportedAt      time.Time           `json:"exported_at"`
	Settings        db.Setting          `json:"settings"`
	Users           []db.User           `json:"users"`
	Controllers     []db.Controller     `json:"controllers"`
	Bins            []db.Bin            `json:"bins"`
	Parts           []db.Part           `json:"parts"`
	PartAssignments []db.PartAssignment `json:"part_assignments"`
	PartLinks       []db.PartLink       `json:"part_links"`
	PartDocs        []db.PartDoc        `json:"part_docs"`
	PartAiPrompts   []db.PartAiPrompt   `json:"part_ai_prompts"`
	AuditLogs       []db.AuditLog       `json:"audit_logs"`
}

// GET /settings/backup/download
func (app *application) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	// Admin Only
	user := auth.GetUserFromRequest(r)
	if !user.IsAdmin() {
		http.Error(w, "Unauthorized", http.StatusForbidden)
		return
	}

	ctx := r.Context()

	// Fetch Data
	settings, _ := app.queries.GetSettings(ctx)
	users, _ := app.queries.GetAllUsers(ctx)
	controllers, _ := app.queries.GetControllers(ctx)
	bins, _ := app.queries.GetAllBins(ctx)
	parts, _ := app.queries.GetAllParts(ctx)
	assignments, _ := app.queries.GetAllPartAssignments(ctx)
	links, _ := app.queries.GetAllPartLinks(ctx)
	docs, _ := app.queries.GetAllPartDocs(ctx)
	prompts, _ := app.queries.GetAllPartAiPrompts(ctx)
	logs, _ := app.queries.GetAllAuditLogs(ctx)

	manifest := BackupManifest{
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

	// Prepare Response
	filename := fmt.Sprintf("wledger_backup_%s.zip", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	zw := zip.NewWriter(w)
	defer zw.Close()

	// Add restore_data.json
	fJson, err := zw.Create("restore_data.json")
	if err != nil {
		app.logger.Error("backup failed to create json entry", "error", err)
		return
	}
	enc := json.NewEncoder(fJson)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		app.logger.Error("backup failed to encode json", "error", err)
		return
	}

	// add human_readable.csv
	fCsv, err := zw.Create("human_readable_parts.csv")
	if err == nil {
		cw := csv.NewWriter(fCsv)
		// Headers matching the Import format
		cw.Write([]string{"Name", "Description", "Part Number", "Manufacturer", "Supplier", "Unit Cost", "Reorder Level", "Min Stock", "Barcode", "Quantity"})
		for _, p := range parts {
			// Calculate total stock manually or map it
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
	uploadsRoot := "./app/uploads"
	err = filepath.Walk(uploadsRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// "uploads/..." in the root of the backup zip
		relInZip, _ := filepath.Rel(uploadsRoot, path)
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
		app.logger.Error("backup failed to zip uploads", "error", err)
	}

	audit.Log(ctx, app.queries, "BACKUP", "SYSTEM", 0, "Downloaded system backup", nil, nil)
}

// POST /settings/backup/restore
func (app *application) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	// Admin Only
	user := auth.GetUserFromRequest(r)
	if !user.IsAdmin() {
		http.Error(w, "Unauthorized", http.StatusForbidden)
		return
	}

	// Parse Upload
	err := r.ParseMultipartForm(100 << 20) // 100 MB memory buffer
	if err != nil {
		components.ImportResult(false, "Upload too large or invalid: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	file, header, err := r.FormFile("backup_file")
	if err != nil {
		components.ImportResult(false, "No file provided", nil).Render(r.Context(), w)
		return
	}
	defer file.Close()

	// Open Zip
	zipReader, err := zip.NewReader(file, header.Size)
	if err != nil {
		components.ImportResult(false, "Invalid ZIP file: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Validation: Find & Parse JSON Manifest
	var manifest BackupManifest
	var manifestFound bool

	for _, f := range zipReader.File {
		if f.Name == "restore_data.json" {
			rc, err := f.Open()
			if err != nil {
				components.ImportResult(false, "Failed to open restore_data.json", nil).Render(r.Context(), w)
				return
			}
			if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
				rc.Close()
				components.ImportResult(false, "Failed to parse backup JSON: "+err.Error(), nil).Render(r.Context(), w)
				return
			}
			rc.Close()
			manifestFound = true
			break
		}
	}

	if !manifestFound {
		components.ImportResult(false, "Invalid Backup: restore_data.json missing", nil).Render(r.Context(), w)
		return
	}

	// Preparation: Extract Uploads to Temp Directory
	// do this BEFORE the DB transaction to ensure the files are valid and extractable.
	timestamp := time.Now().UnixNano()
	tempDir := filepath.Join("app", fmt.Sprintf("restore_tmp_%d", timestamp))

	// Ensure cleanup of temp dir in all failure cases
	defer os.RemoveAll(tempDir)

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		app.logger.Error("failed to create temp restore dir", "path", tempDir, "error", err)
		components.ImportResult(false, "System Error: Failed to create temp directory", nil).Render(r.Context(), w)
		return
	}

	for _, f := range zipReader.File {
		if strings.HasPrefix(f.Name, "uploads/") && !f.FileInfo().IsDir() {
			// Strip "uploads/" to map to tempDir root
			// e.g. "uploads/images/part.jpg" -> "images/part.jpg"
			relPath := strings.TrimPrefix(f.Name, "uploads/")
			targetPath := filepath.Join(tempDir, relPath)

			// Security check: Ensure path is within tempDir
			if !strings.HasPrefix(filepath.Clean(targetPath), tempDir) {
				continue // Skip malicious paths
			}

			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				app.logger.Error("failed to create temp subdir", "path", targetPath, "error", err)
				components.ImportResult(false, "Failed to extract assets", nil).Render(r.Context(), w)
				return
			}

			outFile, err := os.Create(targetPath)
			if err != nil {
				app.logger.Error("failed to create temp file", "path", targetPath, "error", err)
				components.ImportResult(false, "Failed to extract assets", nil).Render(r.Context(), w)
				return
			}

			rc, err := f.Open()
			if err != nil {
				outFile.Close()
				app.logger.Error("failed to open zip file", "file", f.Name, "error", err)
				components.ImportResult(false, "Failed to extract assets", nil).Render(r.Context(), w)
				return
			}
			_, err = io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
			if err != nil {
				app.logger.Error("failed to write temp file", "path", targetPath, "error", err)
				components.ImportResult(false, "Failed to extract assets", nil).Render(r.Context(), w)
				return
			}
		}
	}

	// Database Restore Transaction
	ctx := r.Context()
	tx, err := app.database.Begin()
	if err != nil {
		components.ImportResult(false, "DB Error: "+err.Error(), nil).Render(r.Context(), w)
		return
	}
	// If the function returns before tx.Commit(), this rolls back changes.
	defer tx.Rollback()
	qtx := app.queries.WithTx(tx)

	// Disable FKs temporarily for bulk delete/insert freedom
	if _, err := tx.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		components.ImportResult(false, "Failed to disable FKs: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// TRUNCATE (Delete All)
	tables := []string{
		"part_assignments", "part_links", "part_docs", "part_ai_prompts", "part_tags",
		"parts", "bins", "controllers", "audit_logs", "sessions", "users", "settings",
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			components.ImportResult(false, fmt.Sprintf("Failed to clear table %s: %v", table, err), nil).Render(r.Context(), w)
			return
		}
	}

	// RESTORE (Insert)
	// Settings
	err = qtx.RestoreSettings(ctx, db.RestoreSettingsParams{
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
		components.ImportResult(false, "Failed to restore Settings: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Users
	for _, u := range manifest.Users {
		err = qtx.RestoreUser(ctx, db.RestoreUserParams(u))
		if err != nil {
			components.ImportResult(false, "Failed to restore User: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Controllers
	for _, c := range manifest.Controllers {
		err = qtx.RestoreController(ctx, db.RestoreControllerParams(c))
		if err != nil {
			components.ImportResult(false, "Failed to restore Controller: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Bins
	for _, b := range manifest.Bins {
		err = qtx.RestoreBin(ctx, db.RestoreBinParams(b))
		if err != nil {
			components.ImportResult(false, "Failed to restore Bin: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Parts
	for _, p := range manifest.Parts {
		err = qtx.RestorePart(ctx, db.RestorePartParams{
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
		})
		if err != nil {
			components.ImportResult(false, "Failed to restore Part: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Assignments
	for _, a := range manifest.PartAssignments {
		err = qtx.RestorePartAssignment(ctx, db.RestorePartAssignmentParams(a))
		if err != nil {
			components.ImportResult(false, "Failed to restore Assignment: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Links
	for _, l := range manifest.PartLinks {
		err = qtx.RestorePartLink(ctx, db.RestorePartLinkParams(l))
		if err != nil {
			components.ImportResult(false, "Failed to restore Link: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Docs
	for _, d := range manifest.PartDocs {
		err = qtx.RestorePartDoc(ctx, db.RestorePartDocParams(d))
		if err != nil {
			components.ImportResult(false, "Failed to restore Doc: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Prompts
	for _, p := range manifest.PartAiPrompts {
		err = qtx.RestorePartAiPrompt(ctx, db.RestorePartAiPromptParams(p))
		if err != nil {
			components.ImportResult(false, "Failed to restore AI Prompt: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Logs
	for _, l := range manifest.AuditLogs {
		err = qtx.RestoreAuditLog(ctx, db.RestoreAuditLogParams(l))
		if err != nil {
			components.ImportResult(false, "Failed to restore Audit Log: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Re-enable FKs
	if _, err := tx.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		components.ImportResult(false, "Failed to re-enable FKs: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Commit DB Transaction
	// If this fails, the DB is rolled back, and function exits.
	// `defer os.RemoveAll(tempDir)` will clean up the extracted files.
	// The live `app/uploads` is untouched.
	if err := tx.Commit(); err != nil {
		components.ImportResult(false, "Commit failed: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Atomic Swap of Assets
	// At this point, the DB has the NEW data. Now the files need to be swapped.
	liveUploads := filepath.Join("app", "uploads")
	backupUploads := filepath.Join("app", fmt.Sprintf("uploads_bak_%d", timestamp))
	backupCreated := false

	// Move Live -> Backup
	// Check if live uploads exists (might be fresh install)
	if _, err := os.Stat(liveUploads); err == nil {
		// If this fails, we are in a weird state (New DB, Old Files).
		// But haven't lost data.
		if err := os.Rename(liveUploads, backupUploads); err != nil {
			app.logger.Error("CRITICAL: Failed to move live uploads to backup. Files mismatch DB.", "error", err)
			components.ImportResult(true, "Restore successful, but file system swap failed. Check logs.", nil).Render(r.Context(), w)
			return
		}
		backupCreated = true
	}

	// Move Temp -> Live
	if err := os.Rename(tempDir, liveUploads); err != nil {
		app.logger.Error("CRITICAL: Failed to move temp uploads to live. Attempting rollback.", "error", err)

		// Attempt to restore backup
		if backupCreated {
			if recErr := os.Rename(backupUploads, liveUploads); recErr != nil {
				app.logger.Error("FATAL: Failed to restore backup uploads!", "error", recErr)
			}
		}

		components.ImportResult(false, "File system error during swap. Contact Admin.", nil).Render(r.Context(), w)
		return
	}

	// Cleanup Backup
	if backupCreated {
		os.RemoveAll(backupUploads)
	}
	// tempDir is empty now (moved), but defer will run os.RemoveAll(tempDir) which is fine.

	// Force Logout
	components.ImportResult(true, "System restored successfully. You will be logged out.", nil).Render(r.Context(), w)
}
