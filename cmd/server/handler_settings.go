package main

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
	"golang.org/x/crypto/bcrypt"
)

// GET /settings
func (app *application) handleSettings(w http.ResponseWriter, r *http.Request) {
	// 1. Fetch Settings
	s, err := app.queries.GetSettings(r.Context())
	if err != nil {
		app.logger.Error("failed to get settings", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Fetch Users (for the management table)
	users, err := app.queries.ListUsers(r.Context())
	if err != nil {
		app.logger.Error("failed to list users", "error", err)
		// render settings, just with empty users list
		users = []db.ListUsersRow{}
	}

	// Render
	pages.Settings(s, users).Render(r.Context(), w)
}

// POST /settings
func (app *application) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	requireAuth := r.FormValue("require_auth") == "on"
	enableTimeout := r.FormValue("enable_timeout") == "on"
	timeout, _ := strconv.Atoi(r.FormValue("locate_timeout"))

	colorLocate := r.FormValue("color_locate")
	colorOk := r.FormValue("color_ok")
	colorLow := r.FormValue("color_low")
	colorCritical := r.FormValue("color_critical")

	ctx := r.Context()

	err := app.queries.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
		RequireAuthForRead:   sql.NullBool{Bool: requireAuth, Valid: true},
		LocateTimeoutSeconds: sql.NullInt64{Int64: int64(timeout), Valid: true},
		EnableLocateTimeout:  sql.NullBool{Bool: enableTimeout, Valid: true},
	})
	if err != nil {
		app.logger.Error("failed to update general settings", "error", err)
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
	}

	err = app.queries.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate:        sql.NullString{String: colorLocate, Valid: true},
		ColorStockOk:       sql.NullString{String: colorOk, Valid: true},
		ColorStockLow:      sql.NullString{String: colorLow, Valid: true},
		ColorStockCritical: sql.NullString{String: colorCritical, Valid: true},
	})
	if err != nil {
		app.logger.Error("failed to update color settings", "error", err)
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/password (Self-Service)
func (app *application) handleSettingsPassword(w http.ResponseWriter, r *http.Request) {
	userID := app.session.GetInt64(r.Context(), "user_id")
	if userID == 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	currentPw := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirmPw := r.FormValue("confirm_password")

	if newPw != confirmPw {
		app.logger.Warn("password change failed: mismatch")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	user, err := app.queries.GetUser(r.Context(), userID)
	if err != nil {
		http.Error(w, "Server Error", http.StatusInternalServerError)
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPw))
	if err != nil {
		app.logger.Warn("password change failed: wrong current password", "user_id", userID)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(newPw), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Server Error", http.StatusInternalServerError)
		return
	}

	err = app.queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
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
func (app *application) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	role := r.FormValue("role")
	tempPw := r.FormValue("temp_password")

	// Hash the temp password
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(tempPw), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Hashing failed", http.StatusInternalServerError)
		return
	}

	_, err = app.queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:                  email,
		PasswordHash:           string(hashedBytes),
		Role:                   role,
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true}, // Force password reset
	})

	if err != nil {
		app.logger.Error("failed to create user", "error", err)
		// Assuming error is duplicate email
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	app.logger.Info("created user", "email", email, "role", role)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/users/{id}/delete
func (app *application) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)
	currentUserID := app.session.GetInt64(r.Context(), "user_id")

	// Prevent self-deletion
	if int64(id) == currentUserID {
		app.logger.Warn("prevented self-deletion", "user_id", id)
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	err := app.queries.DeleteUser(r.Context(), int64(id))
	if err != nil {
		app.logger.Error("failed to delete user", "error", err)
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
