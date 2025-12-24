package handler

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

func (h *Handler) HandleAuditLogs(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	// Access control - admin only
	if !user.IsAdmin() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Parse Query Params
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	actionType := q.Get("action_type")
	entityType := q.Get("entity_type")
	search := q.Get("search")

	var userID sql.NullInt64
	if uidStr := q.Get("user_id"); uidStr != "" {
		if uid, err := strconv.Atoi(uidStr); err == nil {
			userID = sql.NullInt64{Int64: int64(uid), Valid: true}
		}
	}

	// Call DB
	params := db.ListAuditLogsParams{
		Limit:      int64(limit),
		Offset:     int64(offset),
		ActionType: sql.NullString{String: actionType, Valid: actionType != ""},
		EntityType: sql.NullString{String: entityType, Valid: entityType != ""},
		Search:     sql.NullString{String: search, Valid: search != ""},
		UserID:     userID,
	}

	logs, err := h.Queries.ListAuditLogs(r.Context(), params)
	if err != nil {
		h.Logger.Error("failed to list audit logs", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Count for pagination
	countParams := db.CountAuditLogsParams{
		ActionType: params.ActionType,
		EntityType: params.EntityType,
		UserID:     params.UserID,
		Search:     params.Search,
	}
	totalCount, err := h.Queries.CountAuditLogs(r.Context(), countParams)
	if err != nil {
		h.Logger.Error("failed to count audit logs", "err", err)
	}

	// Fetch users for filter
	users, err := h.Queries.ListUsers(r.Context())
	if err != nil {
		h.Logger.Error("failed to list users for audit filter", "err", err)
	}

	// Render Template
	pages.AuditLogs(user, logs, users, params, totalCount, page).Render(r.Context(), w)
}
