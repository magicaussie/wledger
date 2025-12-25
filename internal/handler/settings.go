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

	// Fetch current settings for diff
	current, err := h.Queries.GetSettings(ctx)
	if err != nil {
		h.Logger.Error("failed to fetch current settings for diff", "err", err)
	}

	err = h.Queries.ExecTx(ctx, func(q db.Querier) error {
		err = q.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
			RequireAuthForRead:   sql.NullBool{Bool: requireAuth, Valid: true},
			LocateTimeoutSeconds: sql.NullInt64{Int64: int64(timeout), Valid: true},
			EnableLocateTimeout:  sql.NullBool{Bool: enableTimeout, Valid: true},
			EnableDebugLogs:      sql.NullBool{Bool: enableDebugLogs, Valid: true},
		})
		if err != nil {
			return err
		}

		err = q.UpdateColors(ctx, db.UpdateColorsParams{
			ColorLocate:        sql.NullString{String: colorLocate, Valid: true},
			ColorStockOk:       sql.NullString{String: colorOk, Valid: true},
			ColorStockLow:      sql.NullString{String: colorLow, Valid: true},
			ColorStockCritical: sql.NullString{String: colorCritical, Valid: true},
		})
		if err != nil {
			return err
		}

		// Calculate Diff
		oldDiff := make(map[string]any)
		newDiff := make(map[string]any)

		if current.RequireAuthForRead.Bool != requireAuth {
			oldDiff["require_auth"] = current.RequireAuthForRead.Bool
			newDiff["require_auth"] = requireAuth
		}
		if current.EnableDebugLogs.Bool != enableDebugLogs {
			oldDiff["enable_debug_logs"] = current.EnableDebugLogs.Bool
			newDiff["enable_debug_logs"] = enableDebugLogs
		}
		if current.EnableLocateTimeout.Bool != enableTimeout {
			oldDiff["enable_timeout"] = current.EnableLocateTimeout.Bool
			newDiff["enable_timeout"] = enableTimeout
		}
		if int(current.LocateTimeoutSeconds.Int64) != timeout {
			oldDiff["locate_timeout"] = current.LocateTimeoutSeconds.Int64
			newDiff["locate_timeout"] = timeout
		}
		if current.ColorLocate.String != colorLocate {
			oldDiff["color_locate"] = current.ColorLocate.String
			newDiff["color_locate"] = colorLocate
		}
		if current.ColorStockOk.String != colorOk {
			oldDiff["color_ok"] = current.ColorStockOk.String
			newDiff["color_ok"] = colorOk
		}
		if current.ColorStockLow.String != colorLow {
			oldDiff["color_low"] = current.ColorStockLow.String
			newDiff["color_low"] = colorLow
		}
		if current.ColorStockCritical.String != colorCritical {
			oldDiff["color_critical"] = current.ColorStockCritical.String
			newDiff["color_critical"] = colorCritical
		}

		if len(oldDiff) > 0 {
			audit.Log(ctx, q, "UPDATE", "SETTINGS", 1, "Updated system configuration", oldDiff, newDiff)
		}
		return nil
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update settings", http.StatusInternalServerError)
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
		h.UIError.Respond(w, r, err, "User not found", http.StatusInternalServerError)
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
		h.UIError.Respond(w, r, err, "Failed to hash new password", http.StatusInternalServerError)
		return
	}

	err = h.Queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
		PasswordHash: string(hashedBytes),
		ID:           userID,
	})
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update database", http.StatusInternalServerError)
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
		h.UIError.Respond(w, r, err, "Failed to hash temporary password", http.StatusInternalServerError)
		return
	}

	err = h.Queries.ExecTx(r.Context(), func(q db.Querier) error {
		user, err := q.CreateUser(r.Context(), db.CreateUserParams{
			Email:                  email,
			PasswordHash:           string(hashedBytes),
			Role:                   role,
			ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true}, // Force password reset
		})

		if err != nil {
			return err
		}

		audit.Log(r.Context(), q, "CREATE", "USER", user.ID, "Created user", nil,
			map[string]any{"email": user.Email, "role": user.Role})

		return nil
	})

	if err != nil {
		h.Logger.Error("failed to create user", "err", err)
		// Assuming error is duplicate email or db error
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

	// Fetch before delete
	u, err := h.Queries.GetUser(r.Context(), int64(id))
	if err == nil {
		audit.Log(r.Context(), h.Queries, "DELETE", "USER", int64(id), "Deleted user", 
			map[string]any{"email": u.Email, "role": u.Role}, nil)
	}

	err = h.Queries.DeleteUser(r.Context(), int64(id))
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
		h.UIError.Respond(w, r, err, "Failed to force password reset", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), h.Queries, "UPDATE", "USER", int64(id), "Forced password reset", 
		map[string]any{"reset_required": false}, 
		map[string]any{"reset_required": true})

	h.Logger.Info("admin triggered force password reset", "target_id", id)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
