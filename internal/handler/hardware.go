package handler

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
func (h *Handler) HandleHardwareList(w http.ResponseWriter, r *http.Request) {
	// Get User
	user := auth.GetUserFromRequest(r)

	controllers, err := h.Queries.GetControllers(r.Context())
	if err != nil {
		h.Logger.Error("failed to fetch hardware", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Pass User to Template
	pages.Hardware(user, controllers).Render(r.Context(), w)
}

// POST /hardware
func (h *Handler) HandleHardwareCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	ip := r.FormValue("ip_address")

	// Ensure IP is not empty
	if ip == "" {
		h.Logger.Error("attempted to create controller with empty IP")
		http.Error(w, "IP Address is required", http.StatusBadRequest)
		return
	}

	portStr := r.FormValue("port")
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 80
	}

	controller, err := h.Queries.CreateController(r.Context(), db.CreateControllerParams{
		Name:      name,
		IpAddress: ip,
		Port:      sql.NullInt64{Int64: int64(port), Valid: true},
	})

	if err != nil {
		h.Logger.Error("failed to create controller", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	summary := map[string]any{
		"id":         controller.ID,
		"name":       controller.Name,
		"ip_address": controller.IpAddress,
	}
	audit.Log(r.Context(), h.Queries, "CREATE", "HARDWARE", controller.ID, "Added controller "+name, nil, summary)
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/{id}/delete
func (h *Handler) HandleHardwareDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	// Fetch before delete
	c, err := h.Queries.GetController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Controller not found", http.StatusNotFound)
		return
	}

	tx, err := h.Database.Begin()
	if err != nil {
		h.Logger.Error("failed to start transaction", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	qtx := h.Queries.WithTx(tx)

	// Delete Bins First (Manual Cascade)
	err = qtx.DeleteBinsByController(r.Context(), sql.NullInt64{Int64: int64(id), Valid: true})
	if err != nil {
		h.Logger.Error("failed to delete controller bins", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Delete Controller
	err = qtx.DeleteController(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to delete controller", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		h.Logger.Error("failed to commit transaction", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	summary := map[string]any{
		"id":         c.ID,
		"name":       c.Name,
		"ip_address": c.IpAddress,
	}
	audit.Log(r.Context(), h.Queries, "DELETE", "HARDWARE", int64(id), "Deleted controller", summary, nil)
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// GET /hardware/{id}/status
func (h *Handler) HandleHardwareStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	c, err := h.Queries.GetController(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to fetch controller for status check", "err", err, "id", id)
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	online, _ := h.WLED.Ping(r.Context(), c.IpAddress)

	if online != c.IsOnline.Bool {
		err := h.Queries.UpdateControllerStatus(r.Context(), db.UpdateControllerStatusParams{
			IsOnline: sql.NullBool{Bool: online, Valid: true},
			ID:       c.ID,
		})
		if err != nil {
			h.Logger.Error("failed to update controller online status", "err", err, "id", c.ID, "online", online)
		}
	}

	if online {
		w.Write([]byte(`<div class="badge badge-success gap-2">Online</div>`))
	} else {
		w.Write([]byte(`<div class="badge badge-error gap-2">Offline</div>`))
	}
}

// GET /hardware/{id}/grid
func (h *Handler) HandleHardwareGrid(w http.ResponseWriter, r *http.Request) {
	// Get User
	user := auth.GetUserFromRequest(r)

	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	c, err := h.Queries.GetController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	bins, err := h.Queries.GetBinsByController(r.Context(), sql.NullInt64{Int64: int64(id), Valid: true})
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
func (h *Handler) HandleHardwareGridSave(w http.ResponseWriter, r *http.Request) {
	controllerID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	ctx := r.Context()

	// Parse Payload
	gridDataJSON := r.FormValue("grid_data")
	var newCells []gridCellData
	if err := json.Unmarshal([]byte(gridDataJSON), &newCells); err != nil {
		h.Logger.Error("failed to parse grid json", "err", err)
		http.Error(w, "Invalid Grid JSON", http.StatusBadRequest)
		return
	}

	tx, err := h.Database.Begin()
	if err != nil {
		h.Logger.Error("failed to start transaction for grid save", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	qtx := h.Queries.WithTx(tx)

	// Fetch Existing Bins
	existingBins, err := qtx.GetBinsByController(ctx, sql.NullInt64{Int64: int64(controllerID), Valid: true})
	if err != nil {
		h.Logger.Error("failed to fetch existing bins", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Build Map for Diffing: [LedIndex] -> Bin
	existingMap := make(map[int64]db.Bin)
	for _, b := range existingBins {
		if b.LedIndex.Valid {
			existingMap[b.LedIndex.Int64] = b
		}
	}

	maxLedIndex := 0

	// Process Incoming Grid Data
	for _, cell := range newCells {
		if cell.LedIndex > maxLedIndex {
			maxLedIndex = cell.LedIndex
		}

		ledIdx := int64(cell.LedIndex)

		if _, exists := existingMap[ledIdx]; exists {
			// UPDATE EXISTING
			// By using UpsertBin here, the Name/Grid position is updated for this LED Index.
			// Note: UpsertBin relies on UNIQUE(controller_id, led_index)
			err := qtx.UpsertBin(ctx, db.UpsertBinParams{
				Name:         cell.Name,
				ControllerID: sql.NullInt64{Int64: int64(controllerID), Valid: true},
				LedIndex:     sql.NullInt64{Int64: ledIdx, Valid: true},
				Width:        sql.NullInt64{Int64: 1, Valid: true},
				GridX:        sql.NullInt64{Int64: int64(cell.X), Valid: true},
				GridY:        sql.NullInt64{Int64: int64(cell.Y), Valid: true},
			})
			if err != nil {
				h.Logger.Error("failed to update bin", "led", ledIdx, "err", err)
				http.Error(w, "Save failed", http.StatusInternalServerError)
				return
			}
			// Remove from map to mark as "kept"
			delete(existingMap, ledIdx)

		} else {
			// --- INSERT NEW ---
			_, err := qtx.CreateBin(ctx, db.CreateBinParams{
				Name:         cell.Name,
				ControllerID: sql.NullInt64{Int64: int64(controllerID), Valid: true},
				LedIndex:     sql.NullInt64{Int64: ledIdx, Valid: true},
				Width:        sql.NullInt64{Int64: 1, Valid: true},
				GridX:        sql.NullInt64{Int64: int64(cell.X), Valid: true},
				GridY:        sql.NullInt64{Int64: int64(cell.Y), Valid: true},
			})
			if err != nil {
				h.Logger.Error("failed to create bin", "led", ledIdx, "err", err)
				http.Error(w, "Save failed", http.StatusInternalServerError)
				return
			}
		}
	}

	// Handle Deletions (Orphan Logic)
	// Any bins remaining in existingMap were NOT in the new payload.
	// Delete them. The DB Schema (ON DELETE SET NULL) Handles orphaning stock automatically.
	for _, binToDelete := range existingMap {
		err := qtx.DeleteBinByLed(ctx, db.DeleteBinByLedParams{
			ControllerID: sql.NullInt64{Int64: int64(controllerID), Valid: true},
			LedIndex:     binToDelete.LedIndex,
		})
		if err != nil {
			h.Logger.Error("failed to delete removed bin", "id", binToDelete.ID, "err", err)
			// Continue deleting others even if one fails
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
			h.Logger.Error("failed to update controller config", "err", err, "id", controllerID)
			http.Error(w, "Config update failed", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "Commit failed", http.StatusInternalServerError)
		return
	}

	audit.Log(ctx, h.Queries, "UPDATE", "HARDWARE", int64(controllerID), "Updated LED Grid Layout",
		map[string]any{"led_count": len(existingBins)}, // old state
		map[string]any{"led_count": maxLedIndex + 1})   // new state

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/off
func (h *Handler) HandleGlobalOff(w http.ResponseWriter, r *http.Request) {
	controllers, err := h.Queries.GetControllers(r.Context())
	if err != nil {
		h.Logger.Error("failed to list controllers", "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	h.Logger.Info("triggering global off", "controllers", len(controllers))

	for _, c := range controllers {
		go func(ctrlName, ip string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := h.WLED.Clear(ctx, ip)
			if err != nil {
				h.Logger.Error("failed to clear controller", "name", ctrlName, "ip", ip, "err", err)
			} else {
				h.Logger.Info("cleared controller", "name", ctrlName, "ip", ip)
			}
		}(c.Name, c.IpAddress)
	}

	w.WriteHeader(http.StatusOK)
}

// POST /hardware/{id}/locate
func (h *Handler) HandleHardwareLocate(w http.ResponseWriter, r *http.Request) {
	cidStr := chi.URLParam(r, "id")
	cid, _ := strconv.Atoi(cidStr)
	binID, _ := strconv.Atoi(r.URL.Query().Get("bin_id"))

	// Fetch Settings (Needed for Color and Timeout config)
	settings, err := h.Queries.GetSettings(r.Context())
	if err != nil {
		h.Logger.Warn("failed to fetch settings for locate, using defaults", "err", err)
		// Fallback defaults if DB fails
		settings.ColorLocate.String = "#0000FF"
		settings.EnableLocateTimeout.Bool = false
		settings.LocateTimeoutSeconds.Int64 = 0
	}

	// Retrieve Hardware Details
	controller, err := h.Queries.GetController(r.Context(), int64(cid))
	if err != nil {
		h.Logger.Error("locate failed: controller not found", "cid", cid)
		http.Error(w, "Controller not found", http.StatusNotFound)
		return
	}

	bin, err := h.Queries.GetBin(r.Context(), int64(binID))
	if err != nil {
		h.Logger.Error("locate failed: bin not found", "binID", binID)
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
	err = h.WLED.LightUp(r.Context(), controller.IpAddress, ledIndex, width, settings.ColorLocate.String)
	if err != nil {
		// Don't return error to client, just log it
		h.Logger.Error("failed to locate bin", "err", err, "ip", controller.IpAddress)
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
			_ = h.WLED.LightUp(ctx, ip, idx, count, "#000000")
		}(controller.IpAddress, ledIndex, width, timeoutDuration)
	}

	w.WriteHeader(http.StatusOK)
}
