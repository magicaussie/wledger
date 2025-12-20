package main

import (
	"net/http"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /login
func (app *application) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Check if already logged in
	if app.session.Exists(r.Context(), config.SessionKeyUserID) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Check if guest access is allowed
	settings, err := app.queries.GetSettings(r.Context())
	allowGuest := false
	if err == nil {
		// If RequireAuthForRead is FALSE, then Guest is TRUE
		allowGuest = !settings.RequireAuthForRead.Bool
	}

	pages.Login("", allowGuest).Render(r.Context(), w)
}

// POST /login
func (app *application) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	// Get settings for re-rendering if needed
	settings, _ := app.queries.GetSettings(r.Context())
	allowGuest := !settings.RequireAuthForRead.Bool

	// Find user
	user, err := app.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		// Generic error message for security purposes
		pages.Login("Invalid email or password", allowGuest).Render(r.Context(), w)
		return
	}

	// check password
	if !auth.CheckPassword(password, user.PasswordHash) {
		pages.Login("Invalid email or password", allowGuest).Render(r.Context(), w)
		return
	}

	// Login success - Renew token
	if err := app.session.RenewToken(r.Context()); err != nil {
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	// Store session data
	app.session.Put(r.Context(), config.SessionKeyUserID, int64(user.ID))
	app.session.Put(r.Context(), config.SessionKeyRole, user.Role)

	app.logger.Info("user logged in", "email", email)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// POST /logout
func (app *application) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Destroy wipes the data, Renew creates a fresh ID
	err := app.session.Destroy(r.Context())
	if err != nil {
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	err = app.session.RenewToken(r.Context())
	if err != nil {
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// GET /setup
func (app *application) handleSetup(w http.ResponseWriter, r *http.Request) {
	// Don't show setup form if setup is already done
	count, _ := app.queries.CountUsers(r.Context())
	if count > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	pages.Setup().Render(r.Context(), w)
}

// POST /setup
func (app *application) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	// Check count (Race condition prevention)
	count, _ := app.queries.CountUsers(r.Context())
	if count > 0 {
		http.Error(w, "Setup already completed", http.StatusForbidden)
		return
	}

	// Hash password
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "Password error", http.StatusInternalServerError)
		return
	}

	// Create admin user
	user, err := app.queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Role:         "admin",
	})
	if err != nil {
		app.logger.Error("failed to create admin", "error", err)
		http.Error(w, "Failed to create user", http.StatusInternalServerError)
		return
	}

	// Init settings
	_ = app.queries.InitSettings(r.Context())

	// Auto-login the user
	if err := app.session.RenewToken(r.Context()); err == nil {
		app.session.Put(r.Context(), config.SessionKeyUserID, int64(user.ID))
		app.session.Put(r.Context(), config.SessionKeyRole, user.Role)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	} else {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

// GET /force-reset
func (app *application) handleForceReset(w http.ResponseWriter, r *http.Request) {
	userID := app.session.GetInt64(r.Context(), config.SessionKeyUserID)
	user, err := app.queries.GetUser(r.Context(), userID)

	if err != nil || !user.ChangePasswordRequired.Bool {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	pages.ForceReset().Render(r.Context(), w)
}

// POST /force-reset
func (app *application) handleForceResetPost(w http.ResponseWriter, r *http.Request) {
	userID := app.session.GetInt64(r.Context(), config.SessionKeyUserID)

	newPw := r.FormValue("new_password")
	confirmPw := r.FormValue("confirm_password")

	if newPw != confirmPw {
		// TODO: maybe pass an error message here?
		// validation is handled in the force_reset.templ
		// so probably not needed.
		pages.ForceReset().Render(r.Context(), w)
		return
	}

	// Use auth helper
	hash, err := auth.HashPassword(newPw)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	// Update password AND clear password reset flag (handled by SQL)
	err = app.queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
		PasswordHash: hash,
		ID:           userID,
	})
	if err != nil {
		app.logger.Error("failed to update password", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	app.logger.Info("user completed forced password reset", "user_id", userID)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
