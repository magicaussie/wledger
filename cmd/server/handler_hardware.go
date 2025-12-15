package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /hardware
func (app *application) handleHardwareList(w http.ResponseWriter, r *http.Request) {
	// Get User
	user := auth.GetUserFromRequest(r)

	controllers, err := app.queries.GetControllers(r.Context())
	if err != nil {
		app.logger.Error("failed to fetch hardware", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Pass User to Template
	pages.Hardware(user, controllers).Render(r.Context(), w)
}

// POST /hardware
func (app *application) handleHardwareCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	ip := r.FormValue("ip_address")

	// Ensure IP is not empty
	if ip == "" {
		app.logger.Error("attempted to create controller with empty IP")
		http.Error(w, "IP Address is required", http.StatusBadRequest)
		return
	}

	portStr := r.FormValue("port")
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 80
	}

	_, err := app.queries.CreateController(r.Context(), db.CreateControllerParams{
		Name:      name,
		IpAddress: ip,
		Port:      sql.NullInt64{Int64: int64(port), Valid: true},
	})

	if err != nil {
		app.logger.Error("failed to create controller", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), app.queries, "CREATE", "HARDWARE", 0, "Added controller "+name, nil, nil)
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/{id}/delete
func (app *application) handleHardwareDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	err := app.queries.DeleteController(r.Context(), int64(id))
	if err != nil {
		app.logger.Error("failed to delete controller", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), app.queries, "DELETE", "HARDWARE", int64(id), "Deleted controller", nil, nil)
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// GET /hardware/{id}/status
func (app *application) handleHardwareStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	c, err := app.queries.GetController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	online, _ := app.wled.Ping(r.Context(), c.IpAddress)

	if online != c.IsOnline.Bool {
		_ = app.queries.UpdateControllerStatus(r.Context(), db.UpdateControllerStatusParams{
			IsOnline: sql.NullBool{Bool: online, Valid: true},
			ID:       c.ID,
		})
	}

	if online {
		w.Write([]byte(`<div class="badge badge-success gap-2">Online</div>`))
	} else {
		w.Write([]byte(`<div class="badge badge-error gap-2">Offline</div>`))
	}
}

// GET /hardware/{id}/grid
func (app *application) handleHardwareGrid(w http.ResponseWriter, r *http.Request) {
	// Get User
	user := auth.GetUserFromRequest(r)

	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	c, err := app.queries.GetController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	bins, err := app.queries.GetBinsByController(r.Context(), sql.NullInt64{Int64: int64(id), Valid: true})
	if err != nil {
		bins = []db.Bin{}
	}

	// Pass User to Template
	pages.HardwareGrid(user, c, bins).Render(r.Context(), w)
}

// Struct to parse the JSON from GridPainter
type gridCellData struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	LedIndex int    `json:"led_index"`
	Name     string `json:"name"`
}

// POST /hardware/{id}/grid
func (app *application) handleHardwareGridSave(w http.ResponseWriter, r *http.Request) {
	controllerID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	ctx := r.Context()

	// Parse Payload
	gridDataJSON := r.FormValue("grid_data")
	var newCells []gridCellData
	if err := json.Unmarshal([]byte(gridDataJSON), &newCells); err != nil {
		app.logger.Error("failed to parse grid json", "error", err)
		http.Error(w, "Invalid Grid JSON", http.StatusBadRequest)
		return
	}

	tx, err := app.database.Begin()
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	qtx := app.queries.WithTx(tx)

	// TODO: REVISIT THIS
	// Delete ALL bins for this controller
	// This removes all bins and (via cascading delete) creates a clean slate.
	// It also removes all bin/inventory assignments from all parts when this happens.
	// I need to figure out a more elegant solution.
	err = qtx.DeleteBinsByController(ctx, sql.NullInt64{Int64: int64(controllerID), Valid: true})
	if err != nil {
		app.logger.Error("failed to wipe existing bins", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Insert New Bins (Fresh Start)
	maxLedIndex := 0
	for _, cell := range newCells {
		if cell.LedIndex > maxLedIndex {
			maxLedIndex = cell.LedIndex
		}

		// use CreateBin (INSERT) because the table is empty for this controller now.
		// No Upsert needed.
		_, err := qtx.CreateBin(ctx, db.CreateBinParams{
			Name:         cell.Name,
			ControllerID: sql.NullInt64{Int64: int64(controllerID), Valid: true},
			LedIndex:     sql.NullInt64{Int64: int64(cell.LedIndex), Valid: true},
			Width:        sql.NullInt64{Int64: 1, Valid: true},
			GridX:        sql.NullInt64{Int64: int64(cell.X), Valid: true},
			GridY:        sql.NullInt64{Int64: int64(cell.Y), Valid: true},
		})
		if err != nil {
			app.logger.Error("failed to create bin", "name", cell.Name, "error", err)
			http.Error(w, "Failed to save layout", http.StatusInternalServerError)
			return
		}
	}

	// Update Controller Config
	configJSON := r.FormValue("config_data")
	if configJSON != "" {
		err := qtx.UpdateControllerConfig(ctx, db.UpdateControllerConfigParams{
			ConfigJson: sql.NullString{String: configJSON, Valid: true},
			LedCount:   int64(maxLedIndex + 1),
			ID:         int64(controllerID),
		})
		if err != nil {
			http.Error(w, "Config update failed", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Commit failed", http.StatusInternalServerError)
		return
	}

	audit.Log(ctx, app.queries, "UPDATE", "HARDWARE", int64(controllerID), "Reset LED Grid Layout", nil, nil)
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/off
func (app *application) handleGlobalOff(w http.ResponseWriter, r *http.Request) {
	controllers, err := app.queries.GetControllers(r.Context())
	if err != nil {
		app.logger.Error("failed to list controllers", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	app.logger.Info("triggering global off", "controllers", len(controllers))

	for _, c := range controllers {
		go func(ctrlName, ip string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := app.wled.Clear(ctx, ip)
			if err != nil {
				app.logger.Error("failed to clear controller", "name", ctrlName, "ip", ip, "error", err)
			} else {
				app.logger.Info("cleared controller", "name", ctrlName, "ip", ip)
			}
		}(c.Name, c.IpAddress)
	}

	w.WriteHeader(http.StatusOK)
}

// POST /hardware/{id}/locate
func (app *application) handleHardwareLocate(w http.ResponseWriter, r *http.Request) {
	cidStr := chi.URLParam(r, "id")
	cid, _ := strconv.Atoi(cidStr)
	binID, _ := strconv.Atoi(r.URL.Query().Get("bin_id"))

	// Fetch Settings First (needed for Auth check AND WLED config)
	settings, err := app.queries.GetSettings(r.Context())
	if err != nil {
		// Fallback defaults if DB fails
		settings.ColorLocate.String = "#0000FF"
		settings.EnableLocateTimeout.Bool = false
		settings.LocateTimeoutSeconds.Int64 = 0
		// Assume secure default for auth if DB fails
		settings.RequireAuthForRead.Bool = true
	}

	// Check Authorization
	user := auth.GetUserFromRequest(r)
	allowed := false

	if user.IsAuthenticated() {
		// Authenticated users (Viewers, Editors, Admins) can always locate
		allowed = true
	} else {
		// Guest: Only allow if RequireAuthForRead is FALSE
		if !settings.RequireAuthForRead.Bool {
			allowed = true
		}
	}

	if !allowed {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Retrieve Hardware Details
	controller, err := app.queries.GetController(r.Context(), int64(cid))
	if err != nil {
		app.logger.Error("locate failed: controller not found", "cid", cid)
		http.Error(w, "Controller not found", http.StatusNotFound)
		return
	}

	bin, err := app.queries.GetBin(r.Context(), int64(binID))
	if err != nil {
		app.logger.Error("locate failed: bin not found", "binID", binID)
		http.Error(w, "Bin not found", http.StatusNotFound)
		return
	}

	// Calculate LED Positions
	ledIndex := int(bin.LedIndex.Int64)
	width := int(bin.Width.Int64)
	if width < 1 {
		width = 1
	}

	// Trigger WLED
	err = app.wled.LightUp(r.Context(), controller.IpAddress, ledIndex, width, settings.ColorLocate.String)
	if err != nil {
		// Don't return error to client, just log it
		app.logger.Error("failed to locate bin", "error", err, "ip", controller.IpAddress)
	}

	// Handle Auto-Off Timer
	if settings.EnableLocateTimeout.Bool && settings.LocateTimeoutSeconds.Int64 > 0 {
		timeoutDuration := time.Duration(settings.LocateTimeoutSeconds.Int64) * time.Second

		go func(ip string, idx, count int, duration time.Duration) {
			time.Sleep(duration)
			// Create a new context since the request context will be cancelled
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Turn off (using black)
			_ = app.wled.LightUp(ctx, ip, idx, count, "#000000")
		}(controller.IpAddress, ledIndex, width, timeoutDuration)
	}

	w.WriteHeader(http.StatusOK)
}
