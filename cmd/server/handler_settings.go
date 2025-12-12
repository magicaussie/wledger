package main

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /settings
func (app *application) handleSettings(w http.ResponseWriter, r *http.Request) {
	s, err := app.queries.GetSettings(r.Context())
	if err != nil {
		app.logger.Error("failed to get settings", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	pages.Settings(s).Render(r.Context(), w)
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
