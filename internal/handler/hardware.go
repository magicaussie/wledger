package handler

import (
	"database/sql"
	"net/http"
	"strconv"

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

	controllers, err := h.Hardware.ListControllers(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch hardware list", http.StatusInternalServerError)
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
		h.UIError.Respond(w, r, nil, "IP Address is required", http.StatusBadRequest)
		return
	}

	portStr := r.FormValue("port")
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 80
	}

	controller, err := h.Hardware.CreateController(r.Context(), db.CreateControllerParams{
		Name:      name,
		IpAddress: ip,
		Port:      sql.NullInt64{Int64: int64(port), Valid: true},
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to create controller", http.StatusInternalServerError)
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

	// Fetch before delete for logging
	c, err := h.Hardware.GetController(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
		return
	}

	err = h.Hardware.DeleteController(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete hardware", http.StatusInternalServerError)
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

	online, err := h.Hardware.UpdateStatus(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
		return
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

	c, err := h.Hardware.GetController(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
		return
	}

	bins, err := h.Hardware.GetBinsByController(r.Context(), int64(id))
	if err != nil {
		bins = []db.Bin{}
	}

	// Pass User to Template
	pages.HardwareGrid(user, c, bins).Render(r.Context(), w)
}

// Struct to parse the JSON from GridPainter
// TODO: deprecate
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
	configJSON := r.FormValue("config_data")

	// Fetch old count for logging before update
	var oldLedCount int
	existingBins, err := h.Hardware.GetBinsByController(ctx, int64(controllerID))
	if err == nil {
		oldLedCount = len(existingBins)
	}

	newLedCount, err := h.Hardware.SaveGrid(ctx, int64(controllerID), gridDataJSON, configJSON)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to save grid layout", http.StatusInternalServerError)
		return
	}

	// Audit Log (Post-Service)
	audit.Log(ctx, h.Queries, "UPDATE", "HARDWARE", int64(controllerID), "Updated LED Grid Layout",
		map[string]any{"led_count": oldLedCount},
		map[string]any{"led_count": newLedCount})

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/off
func (h *Handler) HandleGlobalOff(w http.ResponseWriter, r *http.Request) {
	err := h.WLED.GlobalOff(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to trigger global off", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// POST /hardware/{id}/locate
func (h *Handler) HandleHardwareLocate(w http.ResponseWriter, r *http.Request) {
	cidStr := chi.URLParam(r, "id")
	cid, _ := strconv.Atoi(cidStr)
	binID, _ := strconv.Atoi(r.URL.Query().Get("bin_id"))

	err := h.WLED.LocateBin(r.Context(), int64(cid), int64(binID))
	if err != nil {
		h.UIError.Respond(w, r, err, "Locate failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
