package handler

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/logger"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/wled"
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
	functionSettings := wled.DefaultFunctionSettings()
	functionError := ""

	// Fetch Admin Data (Only if User is Admin)
	// Viewers/Editors don't need this data since the template hides those sections
	if user.IsAdmin() {
		functionSettings, err = wled.LoadFunctionSettings(r.Context(), h.Queries)
		if err != nil {
			functionError = "Stored LED function settings are invalid or unavailable. Correct and save them, or reset to defaults. LED operations must not use these values."
			h.Logger.Error("failed to load LED function settings", "err", err)
		}
		settings, err = h.Settings.GetSettings(r.Context())
		if err != nil {
			h.Logger.Error("failed to get settings", "err", err)
		}

		users, err = h.Settings.ListUsers(r.Context())
		if err != nil {
			h.Logger.Error("failed to list users", "err", err)
			users = []db.ListUsersRow{}
		}
	} else {
		// Initialize empty for non-admins to satisfy signature
		users = []db.ListUsersRow{}
	}

	// Render with User object
	pages.Settings(user, settings, users, functionSettings, functionError).Render(r.Context(), w)
}

// POST /settings
func (h *Handler) HandleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid settings form", http.StatusBadRequest)
		return
	}
	var functionSettings wled.FunctionSettings
	if r.FormValue("led_action") != "" {
		if !auth.GetUserFromRequest(r).IsAdmin() {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		switch r.FormValue("led_action") {
		case "reset":
			functionSettings = wled.DefaultFunctionSettings()
		case "save":
			functionSettings = make(wled.FunctionSettings)
			for _, name := range wled.FunctionNames() {
				brightness, err := strconv.Atoi(r.FormValue("led_brightness_" + name))
				if err != nil {
					http.Error(w, "Invalid brightness for "+name, http.StatusBadRequest)
					return
				}
				functionSettings[name] = wled.FunctionStyle{Color: r.FormValue("led_color_" + name), Brightness: brightness}
			}
		default:
			http.Error(w, "Invalid LED settings action", http.StatusBadRequest)
			return
		}
		normalized, err := wled.NormalizeFunctionSettings(functionSettings)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// A separate form saves only LED functions, never unrelated settings.
		if err := wled.SaveFunctionSettings(r.Context(), h.Queries, normalized); err != nil {
			h.UIError.Respond(w, r, err, "Failed to save LED function settings", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	requireAuth := r.FormValue("require_auth") == "on"
	enableTimeout := r.FormValue("enable_timeout") == "on"
	enableDebugLogs := r.FormValue("enable_debug_logs") == "on"
	timeout, _ := strconv.Atoi(r.FormValue("locate_timeout"))

	colorLocate := r.FormValue("color_locate")
	colorOk := r.FormValue("color_ok")
	colorLow := r.FormValue("color_low")
	colorCritical := r.FormValue("color_critical")
	colorError := r.FormValue("color_error")
	// These controls now live in the separate LED function form. Saving general
	// settings must not reset pre-existing legacy colors before central adoption.
	if !r.PostForm.Has("color_locate") || !r.PostForm.Has("color_error") {
		current, err := h.Settings.GetSettings(r.Context())
		if err != nil {
			h.UIError.Respond(w, r, err, "Failed to preserve existing LED colors", http.StatusInternalServerError)
			return
		}
		if !r.PostForm.Has("color_locate") {
			colorLocate = current.ColorLocate.String
		}
		if !r.PostForm.Has("color_error") {
			colorError = current.ColorError.String
		}
	}

	// Supplier settings
	supplierCacheTTL := 96
	if v := r.FormValue("supplier_cache_ttl"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			supplierCacheTTL = parsed
		}
	}
	defaultCurrency := r.FormValue("default_currency")

	ctx := r.Context()

	err := h.Settings.UpdateSettings(ctx, settings.UpdateSettingsParams{
		RequireAuth:         requireAuth,
		LocateTimeout:       timeout,
		EnableLocateTimeout: enableTimeout,
		EnableDebugLogs:     enableDebugLogs,
		ColorLocate:         colorLocate,
		ColorOk:             colorOk,
		ColorLow:            colorLow,
		ColorCritical:       colorCritical,
		ColorError:          colorError,
		SupplierCacheTTL:    supplierCacheTTL,
		DefaultCurrency:     defaultCurrency,
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

	user, err := h.Settings.GetUser(r.Context(), userID)
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

	err = h.Settings.UpdateUserPassword(r.Context(), userID, newPw)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update password", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// POST /settings/users (Create User)
func (h *Handler) HandleUserCreate(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	role := r.FormValue("role")
	tempPw := r.FormValue("temp_password")

	_, err := h.Settings.CreateUser(r.Context(), settings.CreateUserParams{
		Email:    email,
		Role:     role,
		Password: tempPw,
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to create user", http.StatusInternalServerError)
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

	err := h.Settings.DeleteUser(r.Context(), int64(id))
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

	// Don't flag current user
	if int64(id) == currentUserID {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	err := h.Settings.ForceReset(r.Context(), int64(id))

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to force password reset", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("admin triggered force password reset", "target_id", id)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
