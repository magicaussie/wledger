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
	// 1. Admin Only
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

	// Find JSON Manifest
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

	// Database Restore Transaction
	ctx := r.Context()
	tx, err := app.database.Begin()
	if err != nil {
		components.ImportResult(false, "DB Error: "+err.Error(), nil).Render(r.Context(), w)
		return
	}
	defer tx.Rollback()
	qtx := app.queries.WithTx(tx)

	// Disable FKs temporarily for bulk delete/insert freedom
	if _, err := tx.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		components.ImportResult(false, "Failed to disable FKs: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// TRUNCATE (Delete All)
	// Order doesn't strictly matter with FKs off, but good practice
	tables := []string{
		"part_assignments", "part_links", "part_docs", "part_ai_prompts", "part_tags",
		"parts_fts", "parts", "bins", "controllers", "audit_logs", "sessions", "users", "settings",
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
		err = qtx.RestoreUser(ctx, db.RestoreUserParams{
			ID:                     u.ID,
			Email:                  u.Email,
			PasswordHash:           u.PasswordHash,
			Role:                   u.Role,
			ChangePasswordRequired: u.ChangePasswordRequired,
			CreatedAt:              u.CreatedAt,
		})
		if err != nil {
			components.ImportResult(false, "Failed to restore User: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Controllers
	for _, c := range manifest.Controllers {
		err = qtx.RestoreController(ctx, db.RestoreControllerParams{
			ID:         c.ID,
			Name:       c.Name,
			IpAddress:  c.IpAddress,
			Port:       c.Port,
			MacAddress: c.MacAddress,
			IsOnline:   c.IsOnline,
			LedCount:   c.LedCount,
			ConfigJson: c.ConfigJson,
			CreatedAt:  c.CreatedAt,
		})
		if err != nil {
			components.ImportResult(false, "Failed to restore Controller: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Bins
	for _, b := range manifest.Bins {
		err = qtx.RestoreBin(ctx, db.RestoreBinParams{
			ID:           b.ID,
			Name:         b.Name,
			ControllerID: b.ControllerID,
			LedIndex:     b.LedIndex,
			Width:        b.Width,
			GridX:        b.GridX,
			GridY:        b.GridY,
		})
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
		err = qtx.RestorePartAssignment(ctx, db.RestorePartAssignmentParams{
			ID:       a.ID,
			PartID:   a.PartID,
			BinID:    a.BinID,
			Quantity: a.Quantity,
		})
		if err != nil {
			components.ImportResult(false, "Failed to restore Assignment: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Links
	for _, l := range manifest.PartLinks {
		err = qtx.RestorePartLink(ctx, db.RestorePartLinkParams{
			ID:     l.ID,
			PartID: l.PartID,
			Url:    l.Url,
			Label:  l.Label,
		})
		if err != nil {
			components.ImportResult(false, "Failed to restore Link: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Docs
	for _, d := range manifest.PartDocs {
		err = qtx.RestorePartDoc(ctx, db.RestorePartDocParams{
			ID:       d.ID,
			PartID:   d.PartID,
			FilePath: d.FilePath,
			FileName: d.FileName,
		})
		if err != nil {
			components.ImportResult(false, "Failed to restore Doc: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Prompts
	for _, p := range manifest.PartAiPrompts {
		err = qtx.RestorePartAiPrompt(ctx, db.RestorePartAiPromptParams{
			ID:         p.ID,
			PartID:     p.PartID,
			PromptText: p.PromptText,
			AiResponse: p.AiResponse,
			ModelUsed:  p.ModelUsed,
			CreatedAt:  p.CreatedAt,
		})
		if err != nil {
			components.ImportResult(false, "Failed to restore AI Prompt: "+err.Error(), nil).Render(r.Context(), w)
			return
		}
	}

	// Logs
	for _, l := range manifest.AuditLogs {
		err = qtx.RestoreAuditLog(ctx, db.RestoreAuditLogParams{
			ID:         l.ID,
			UserID:     l.UserID,
			ActionType: l.ActionType,
			EntityType: l.EntityType,
			EntityID:   l.EntityID,
			Details:    l.Details,
			OldValue:   l.OldValue,
			NewValue:   l.NewValue,
			CreatedAt:  l.CreatedAt,
		})
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

	// restore Assets
	// Wipe existing uploads ensures a clean state
	os.RemoveAll("./app/uploads")
	os.MkdirAll("./app/uploads", 0755)

	for _, f := range zipReader.File {
		if strings.HasPrefix(f.Name, "uploads/") && !f.FileInfo().IsDir() {
			// uploads/images/foo.jpg -> ./app/uploads/images/foo.jpg
			targetPath := filepath.Join("app", f.Name) // "app/uploads/..."

			// Security check: Ensure path is within app/uploads
			if !strings.HasPrefix(filepath.Clean(targetPath), "app"+string(os.PathSeparator)+"uploads") {
				continue // Skip malicious paths
			}

			os.MkdirAll(filepath.Dir(targetPath), 0755)
			outFile, err := os.Create(targetPath)
			if err != nil {
				app.logger.Warn("Failed to create asset file", "path", targetPath, "error", err)
				continue
			}

			rc, err := f.Open()
			if err != nil {
				outFile.Close()
				continue
			}
			io.Copy(outFile, rc)
			rc.Close()
			outFile.Close()
		}
	}

	// Commit
	if err := tx.Commit(); err != nil {
		components.ImportResult(false, "Commit failed: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Force Logout
	components.ImportResult(true, "System restored successfully. You will be logged out.", nil).Render(r.Context(), w)
}
