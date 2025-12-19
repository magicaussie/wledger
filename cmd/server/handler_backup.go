package main

import (
	"fmt"
	"net/http"
	"time"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/components"
)

// GET /settings/backup/download
func (app *application) handleBackupDownload(w http.ResponseWriter, r *http.Request) {
	// Admin Only
	user := auth.GetUserFromRequest(r)
	if !user.IsAdmin() {
		http.Error(w, "Unauthorized", http.StatusForbidden)
		return
	}

	// Prepare Response Headers
	filename := fmt.Sprintf("wledger_backup_%s.zip", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	// Stream Backup to Response
	if err := app.backup.Export(r.Context(), w); err != nil {
		app.logger.Error("failed to generate backup", "error", err)
		// Since headers are already sent, a clean error response can't be sent.
		// The zip might be corrupted on the client side if this fails mid-stream.
		return
	}
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

	// Execute Restore via Service
	if err := app.backup.Restore(r.Context(), file, header.Size); err != nil {
		app.logger.Error("restore failed", "error", err)
		components.ImportResult(false, "Restore failed: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Force Logout / Success
	components.ImportResult(true, "System restored successfully. You will be logged out.", nil).Render(r.Context(), w)
}
