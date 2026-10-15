package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/uploads"
	"github.com/tuxedocurly/wledger/web/pages"
)

// HandleUploadsMaintenance renders the admin upload-maintenance page: it lists
// upload files that no part, category or document references. Detection is
// read-only.
func (h *Handler) HandleUploadsMaintenance(w http.ResponseWriter, r *http.Request) {
	svc := uploads.NewService(h.Database, config.DirUploads)
	report, err := svc.Scan(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to scan uploads", http.StatusInternalServerError)
		return
	}
	deleted, _ := strconv.Atoi(r.URL.Query().Get("deleted"))
	notice := ""
	if r.URL.Query().Get("deleted") != "" {
		notice = "Deleted " + strconv.Itoa(deleted) + " orphaned upload file(s)."
	}
	pages.UploadMaintenance(auth.GetUserFromRequest(r), report, deleted, notice).Render(r.Context(), w)
}

// HandleUploadsMaintenanceDelete removes every orphaned upload file. The service
// re-scans immediately before deleting and refuses to remove any file that has
// become referenced, so a concurrent upload, edit or restore can never lose a
// live asset. The deletion is recorded in the audit log.
func (h *Handler) HandleUploadsMaintenanceDelete(w http.ResponseWriter, r *http.Request) {
	svc := uploads.NewService(h.Database, config.DirUploads)
	report, err := svc.Scan(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to scan uploads", http.StatusInternalServerError)
		return
	}
	paths := make([]string, 0, len(report.Orphans))
	for _, orphan := range report.Orphans {
		paths = append(paths, orphan.RelPath)
	}
	deleted, err := svc.Delete(r.Context(), paths)
	if len(deleted) > 0 {
		// File removal is not transactional. Record successful removals even
		// when a later removal fails or the initiating request is canceled.
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		auditErr := audit.LogTx(auditCtx, h.Queries, "DELETE", "UPLOADS", 0,
			"Deleted "+strconv.Itoa(len(deleted))+" orphaned upload file(s)", nil, deleted)
		cancel()
		if auditErr != nil {
			err = errors.Join(err, fmt.Errorf("files removed but cleanup audit failed: %w", auditErr))
		}
	}
	if err != nil {
		h.UIError.Respond(w, r, err, "Upload cleanup partially completed; deleted "+strconv.Itoa(len(deleted))+" file(s)", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/settings/uploads?deleted="+strconv.Itoa(len(deleted)), http.StatusSeeOther)
}
