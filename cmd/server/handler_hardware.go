package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

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
	// TODO: implement a routine for this
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
	// We need to write this query first! (See next step)
	// For now, let's assume GetBinsByController exists.
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

	// Transaction time!
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

	// 2. Insert new bins
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
