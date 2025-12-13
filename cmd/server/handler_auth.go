package main

import (
	"net/http"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
	"golang.org/x/crypto/bcrypt"
)

// GET /login
func (app *application) handleLogin(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to dashboard
	if app.session.Exists(r.Context(), "user_id") {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	pages.Login("").Render(r.Context(), w)
}

// POST /login
func (app *application) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	// find user
	user, err := app.queries.GetUserByEmail(r.Context(), email)
	if err != nil {
		// generic error message for security purposes
		pages.Login("Invalid email or password").Render(r.Context(), w)
		return
	}

	// check password
	if !auth.CheckPassword(password, user.PasswordHash) {
		pages.Login("Invalid email or password").Render(r.Context(), w)
		return
	}

	// login success
	// Renew token to prevent session fixation attacks
	if err := app.session.RenewToken(r.Context()); err != nil {
		http.Error(w, "Session error", http.StatusInternalServerError)
		return
	}

	// store user.ID in session
	app.session.Put(r.Context(), "user_id", int64(user.ID))

	// redirect home
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// POST /logout
func (app *application) handleLogout(w http.ResponseWriter, r *http.Request) {
	app.session.Destroy(r.Context())
	app.session.Remove(r.Context(), "user_id")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// GET /setup
func (app *application) handleSetup(w http.ResponseWriter, r *http.Request) {
	pages.Setup().Render(r.Context(), w)
}

// POST /setup
func (app *application) handleSetupPost(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	password := r.FormValue("password")

	// check count (to prevent potential race condition)
	count, _ := app.queries.CountUsers(r.Context())
	if count > 0 {
		http.Error(w, "Setup already completed", http.StatusForbidden)
		return
	}

	// hash password
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "Password error", http.StatusInternalServerError)
		return
	}

	// create admin user
	_, err = app.queries.CreateUser(r.Context(), db.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Role:         "admin",
	})
	if err != nil {
		http.Error(w, "Failed to create user", http.StatusInternalServerError)
		return
	}

	// init settings
	err = app.queries.InitSettings(r.Context())
	if err != nil {
		app.logger.Error("Failed to init settings", "error", err)
		// Continue anyway, it's not fatal
		// TODO: consider additional handling here
	}

	// redirect to /login
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// GET /force-reset
func (app *application) handleForceReset(w http.ResponseWriter, r *http.Request) {
	// Double check they actually need this, otherwise send to dashboard
	// (Prevents people from manually visiting this URL later)
	userID := app.session.GetInt64(r.Context(), "user_id")
	user, err := app.queries.GetUser(r.Context(), userID)

	if err != nil || !user.ChangePasswordRequired.Bool {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	pages.ForceReset().Render(r.Context(), w)
}

// POST /force-reset
func (app *application) handleForceResetPost(w http.ResponseWriter, r *http.Request) {
	userID := app.session.GetInt64(r.Context(), "user_id")

	newPw := r.FormValue("new_password")
	confirmPw := r.FormValue("confirm_password")

	if newPw != confirmPw {
		// ideally flash error
		pages.ForceReset().Render(r.Context(), w)
		return
	}

	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(newPw), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	// Update password AND clear the requirement flag
	err = app.queries.UpdateUserPassword(r.Context(), db.UpdateUserPasswordParams{
		PasswordHash: string(hashedBytes),
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
