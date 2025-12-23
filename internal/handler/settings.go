package handler

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/logger"
	"github.com/tuxedocurly/wledger/web/pages"
	"golang.org/x/crypto/bcrypt"
)

// GET /settings
func (h *Handler) HandleSettings(w http.ResponseWriter, r *http.Request) {
	// Get User
	user := auth.GetUserFromRequest(r)

	var settings db.Setting
	var users []db.ListUsersRow
	var err error

	// Fetch Admin Data (Only if User is Admin)
	// Viewers/Editors don't need this data since the template hides those sections.
	if user.IsAdmin() {
		settings, err = h.Queries.GetSettings(r.Context())
		if err != nil {
			h.Logger.Error("failed to get settings", "err", err)
			// If settings table is empty/broken, consider handling it gracefully,
			// but for now, log it
			// TODO: implement graceful handling
		}

		users, err = h.Queries.ListUsers(r.Context())
		if err != nil {
			h.Logger.Error("failed to list users", "err", err)
			users = []db.ListUsersRow{}
		}
	} else {
		// Initialize empty for non-admins to satisfy signature
		users = []db.ListUsersRow{}
	}

	// Render with User object
	pages.Settings(user, settings, users).Render(r.Context(), w)
}

// POST /settings
func (h *Handler) HandleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	requireAuth := r.FormValue("require_auth") == "on"
	enableTimeout := r.FormValue("enable_timeout") == "on"
	enableDebugLogs := r.FormValue("enable_debug_logs") == "on"
	timeout, _ := strconv.Atoi(r.FormValue("locate_timeout"))

	colorLocate := r.FormValue("color_locate")
	colorOk := r.FormValue("color_ok")
	colorLow := r.FormValue("color_low")
	colorCritical := r.FormValue("color_critical")

	ctx := r.Context()

	// Start Transaction
	tx, err := h.Database.Begin()
	if err != nil {
		h.Logger.Error("failed to begin transaction", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	qtx := h.Queries.WithTx(tx)

	err = qtx.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		RequireAuthForRead:   sql.NullBool{Bool: requireAuth, Valid: true},
		LocateTimeoutSeconds: sql.NullInt64{Int64: int64(timeout), Valid: true},
		EnableLocateTimeout:  sql.NullBool{Bool: enableTimeout, Valid: true},
		EnableDebugLogs:      sql.NullBool{Bool: enableDebugLogs, Valid: true},
	})
	if err != nil {
		h.Logger.Error("failed to update general settings", "err", err)
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
	}

	err = qtx.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate:        sql.NullString{String: colorLocate, Valid: true},
		ColorStockOk:       sql.NullString{String: colorOk, Valid: true},
		ColorStockLow:      sql.NullString{String: colorLow, Valid: true},
		ColorStockCritical: sql.NullString{String: colorCritical, Valid: true},
	})
	if err != nil {
		h.Logger.Error("failed to update color settings", "err", err)
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
	}

	// Add Audit Log
	audit.Log(ctx, qtx, "UPDATE", "SETTINGS", 1, "Updated system configuration", nil, nil)

	if err := tx.Commit(); err != nil {
		h.Logger.Error("failed to commit settings update", "err", err)
		http.Error(w, "Commit failed", http.StatusInternalServerError)
		return
	}

	// Update Runtime Log Level
	logger.SetDebug(enableDebugLogs)

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/password (Self-Service)
func (h *Handler) HandleSettingsPassword(w http.ResponseWriter, r *http.Request) {
	userID := h.Session.GetInt64(r.Context(), config.SessionKeyUserID)
	if userID == 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	currentPw := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirmPw := r.FormValue("confirm_password")

	if newPw != confirmPw {
		h.Logger.Warn("password change failed: mismatch")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	user, err := h.Queries.GetUser(r.Context(), userID)
	if err != nil {
		http.Error(w, "Server Error", http.StatusInternalServerError)
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPw))
	if err != nil {
		h.Logger.Warn("password change failed: wrong current password", "user_id", userID)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(newPw), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Server Error", http.StatusInternalServerError)
		return
	}

	err = h.Queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
		PasswordHash: string(hashedBytes),
		ID:           userID,
	})
	if err != nil {
		http.Error(w, "Database Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/users (Create User)
func (h *Handler) HandleUserCreate(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	role := r.FormValue("role")
	tempPw := r.FormValue("temp_password")

	// Hash the temp password
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(tempPw), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Hashing failed", http.StatusInternalServerError)
		return
	}

	_, err = h.Queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:                  email,
		PasswordHash:           string(hashedBytes),
		Role:                   role,
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true}, // Force password reset
	})

	if err != nil {
		h.Logger.Error("failed to create user", "err", err)
		// Assuming error is duplicate email
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	h.Logger.Info("created user", "email", email, "role", role)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/users/{id}/delete
func (h *Handler) HandleUserDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)
	currentUserID := h.Session.GetInt64(r.Context(), config.SessionKeyUserID)

	// Prevent self-deletion
	if int64(id) == currentUserID {
		h.Logger.Warn("prevented self-deletion", "user_id", id)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	err := h.Queries.DeleteUser(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to delete user", "err", err)
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/users/{id}/reset
func (h *Handler) HandleUserForceReset(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)
	currentUserID := h.Session.GetInt64(r.Context(), config.SessionKeyUserID)

	// Don't flag yourself (UX preference, use standard change password instead)
	if int64(id) == currentUserID {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	err := h.Queries.SetPasswordResetFlag(r.Context(), db.SetPasswordResetFlagParams{
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true},
		ID:                     int64(id),
	})

	if err != nil {
		h.Logger.Error("failed to force password reset", "err", err, "target_id", id)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("admin triggered force password reset", "target_id", id)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
