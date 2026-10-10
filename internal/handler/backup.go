package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/backup"
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

	// Build the archive into a temporary file first. Only once the export has
	// completed successfully do we send the download headers, so a failed export
	// is reported as an error instead of streaming a truncated or corrupt ZIP to
	// the administrator as if it were a valid backup.
	tmp, err := os.CreateTemp("", "wledger_backup_*.zip")
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate backup", http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := h.Backup.Export(r.Context(), tmp); err != nil {
		tmp.Close()
		h.UIError.Respond(w, r, err, "Failed to generate backup", http.StatusInternalServerError)
		return
	}
	if err := tmp.Close(); err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate backup", http.StatusInternalServerError)
		return
	}

	archive, err := os.Open(tmpPath)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate backup", http.StatusInternalServerError)
		return
	}
	defer archive.Close()

	filename := fmt.Sprintf("wledger_backup_%s.zip", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	if _, err := io.Copy(w, archive); err != nil {
		// Headers are already sent; this is a client-side disconnect or write
		// failure, so there is nothing more we can do but record it.
		h.Logger.Error("failed to stream backup to client", "err", err)
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

		// A restore whose data and files were committed but whose post-restore
		// allocation processing did not complete cleanly is a warning, not a
		// failure. Report it distinctly so a committed restore is never shown as
		// failed.
		var committed *backup.RestoreCommittedError
		if errors.As(err, &committed) {
			components.ImportResult(true, "Restore completed with warning: "+committed.Reason+". You will be logged out.", nil).Render(r.Context(), w)
			return
		}

		components.ImportResult(false, "Restore failed: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Force Logout / Success
	components.ImportResult(true, "System restored successfully. You will be logged out.", nil).Render(r.Context(), w)
}
