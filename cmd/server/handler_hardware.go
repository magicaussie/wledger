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
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /hardware
func (app *application) handleHardwareList(w http.ResponseWriter, r *http.Request) {
	controllers, err := app.queries.GetControllers(r.Context())
	if err != nil {
		app.logger.Error("failed to list controllers", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	pages.Hardware(controllers).Render(r.Context(), w)
}

// POST /hardware
func (app *application) handleHardwareCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	ip := r.FormValue("ip")

	// Create
	c, err := app.queries.CreateController(r.Context(), db.CreateControllerParams{
		Name:      name,
		IpAddress: ip,
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		app.logger.Error("failed to create controller", "error", err)
		http.Error(w, "Failed to save", http.StatusInternalServerError)
		return
	}

	// Audit
	audit.Log(r.Context(), app.queries, "CREATE", "CONTROLLER", c.ID, "Added controller "+name, nil, c)

	// Ping check async (fire and forget)
	go func() {
		online, _ := app.wled.Ping(r.Context(), ip)
		app.queries.UpdateControllerStatus(r.Context(), db.UpdateControllerStatusParams{
			IsOnline: sql.NullBool{Bool: online, Valid: true},
			ID:       c.ID,
		})
	}()

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/{id}/delete
func (app *application) handleHardwareDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	// Fetch for Audit
	old, _ := app.queries.GetController(r.Context(), int64(id))

	err := app.queries.DeleteController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Failed to delete", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), app.queries, "DELETE", "CONTROLLER", int64(id), "Deleted controller", old, nil)

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// GET /hardware/{id}/status
func (app *application) handleHardwareStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	c, err := app.queries.GetController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "", http.StatusNotFound)
		return
	}

	// Perform a live check
	// Note: On a larger scale, I wouldn't want to ping every time.
	// With home usage expected, (1-10 controllers), real-time ping should be fine
	// TODO: possibly implement a goroutine for this
	online, _ := app.wled.Ping(r.Context(), c.IpAddress)

	// Update DB with latest status
	app.queries.UpdateControllerStatus(r.Context(), db.UpdateControllerStatusParams{
		IsOnline: sql.NullBool{Bool: online, Valid: true},
		ID:       c.ID,
	})

	// Render just the badge
	if online {
		w.Write([]byte(`<div class="badge badge-success gap-2">Online</div>`))
	} else {
		w.Write([]byte(`<div class="badge badge-error gap-2">Offline</div>`))
	}
}

// GET /hardware/{id}/grid
func (app *application) handleHardwareGrid(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	// Fetch Controller
	c, err := app.queries.GetController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Controller not found", http.StatusNotFound)
		return
	}

	// Fetch Existing Bins
	bins, err := app.queries.GetBinsByController(r.Context(), sql.NullInt64{Int64: int64(id), Valid: true})
	if err != nil {
		// If no bins, just pass empty list
		bins = []db.Bin{}
	}

	pages.HardwareGrid(c, bins).Render(r.Context(), w)
}

// POST /hardware/{id}/grid
type GridCell struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	LedIndex int    `json:"led_index"`
	Name     string `json:"name"`
}

func (app *application) handleHardwareGridSave(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	// Parse JSON from hidden input
	gridData := r.FormValue("grid_data")

	var cells []GridCell
	if err := json.Unmarshal([]byte(gridData), &cells); err != nil {
		http.Error(w, "Invalid JSON data", http.StatusBadRequest)
		return
	}

	// Transaction
	tx, err := app.database.Begin()
	if err != nil {
		http.Error(w, "DB Error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	qtx := app.queries.WithTx(tx)

	err = qtx.DeleteBinsByController(r.Context(), sql.NullInt64{Int64: int64(id), Valid: true})
	if err != nil {
		http.Error(w, "Failed to clear old bins", http.StatusInternalServerError)
		return
	}

	// Insert new bins
	for _, cell := range cells {
		_, err := qtx.CreateBin(r.Context(), db.CreateBinParams{
			Name:         cell.Name,
			ControllerID: sql.NullInt64{Int64: int64(id), Valid: true},
			LedIndex:     sql.NullInt64{Int64: int64(cell.LedIndex), Valid: true},
			Width:        sql.NullInt64{Int64: 1, Valid: true},
			GridX:        sql.NullInt64{Int64: int64(cell.X), Valid: true},
			GridY:        sql.NullInt64{Int64: int64(cell.Y), Valid: true},
		})
		if err != nil {
			http.Error(w, "Failed to insert bin", http.StatusInternalServerError)
			return
		}
	}

	tx.Commit()

	// Update LED count on controller
	app.queries.UpdateControllerStatus(r.Context(), db.UpdateControllerStatusParams{
		IsOnline: sql.NullBool{Bool: true, Valid: true}, // Assume it works
		LedCount: int64(len(cells)),
		ID:       int64(id),
	})

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/{id}/locate?bin_id=X
func (app *application) handleHardwareLocate(w http.ResponseWriter, r *http.Request) {
	cidStr := chi.URLParam(r, "id")
	cid, _ := strconv.Atoi(cidStr)

	binID, _ := strconv.Atoi(r.URL.Query().Get("bin_id"))

	// Get Resources
	controller, err := app.queries.GetController(r.Context(), int64(cid))
	if err != nil {
		http.Error(w, "Controller not found", http.StatusNotFound)
		return
	}

	bin, err := app.queries.GetBin(r.Context(), int64(binID))
	if err != nil {
		http.Error(w, "Bin not found", http.StatusNotFound)
		return
	}

	settings, err := app.queries.GetSettings(r.Context())
	if err != nil {
		// Fallback defaults if settings fail for some reason
		settings.ColorLocate.String = "#0000FF" // Blue
	}

	// Determine LED Range
	// WLED uses 0-based indexing.
	ledIndex := int(bin.LedIndex.Int64)
	width := int(bin.Width.Int64)
	if width < 1 {
		width = 1
	}

	// Send Command
	// The WLED client handles the JSON structure
	err = app.wled.LightUp(r.Context(), controller.IpAddress, ledIndex, width, settings.ColorLocate.String)
	if err != nil {
		app.logger.Error("failed to locate bin", "error", err, "ip", controller.IpAddress)
		// don't return an HTTP error to the UI to avoid disrupting the user experience
		// log it instead
	}

	w.WriteHeader(http.StatusOK)
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
				// Now we will see this in the console!
				app.logger.Error("failed to clear controller", "name", ctrlName, "ip", ip, "error", err)
			} else {
				app.logger.Info("cleared controller", "name", ctrlName, "ip", ip)
			}
		}(c.Name, c.IpAddress)
	}

	w.WriteHeader(http.StatusOK)
}
