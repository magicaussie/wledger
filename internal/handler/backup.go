package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/web/components"
)

// GET /settings/backup/download
func (h *Handler) HandleBackupDownload(w http.ResponseWriter, r *http.Request) {
	// Admin Only
	user := auth.GetUserFromRequest(r)
	if !user.IsAdmin() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	// Prepare Response Headers
	filename := fmt.Sprintf("wledger_backup_%s.zip", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	// Stream Backup to Response
	if err := h.Backup.Export(r.Context(), w); err != nil {
		h.Logger.Error("failed to generate backup", "err", err)
		// Since headers are already sent, a clean error response can't be sent.
		// The zip might be corrupted on the client side if this fails mid-stream.
		return
	}
}

// POST /settings/backup/restore
func (h *Handler) HandleBackupRestore(w http.ResponseWriter, r *http.Request) {
	// Admin Only
	user := auth.GetUserFromRequest(r)
	if !user.IsAdmin() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	// Parse Upload
	err := r.ParseMultipartForm(config.MaxUploadSizeBackup) // 100 MB memory buffer
	if err != nil {
		h.Logger.Error("failed to parse multipart form for backup restore", "err", err)
		components.ImportResult(false, "Upload too large or invalid: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Clean up files after request
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("backup_file")
	if err != nil {
		h.Logger.Error("failed to get backup file from request", "err", err)
		components.ImportResult(false, "No file provided", nil).Render(r.Context(), w)
		return
	}
	defer file.Close()

	// Execute Restore via Service
	if err := h.Backup.Restore(r.Context(), file, header.Size); err != nil {
		h.Logger.Error("restore failed", "err", err)
		components.ImportResult(false, "Restore failed: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Force Logout / Success
	components.ImportResult(true, "System restored successfully. You will be logged out.", nil).Render(r.Context(), w)
}
