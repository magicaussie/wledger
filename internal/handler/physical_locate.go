package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func (h *Handler) HandlePhysicalPartLocate(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	partID, partErr := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	binID, binErr := strconv.ParseInt(r.FormValue("bin_id"), 10, 64)
	if partErr != nil || binErr != nil || partID <= 0 || binID <= 0 {
		h.UIError.Respond(w, r, nil, "Select a valid inventory bin to locate", http.StatusBadRequest)
		return
	}
	if h.Locator == nil {
		h.UIError.Respond(w, r, nil, "Drawer LED Locate service is unavailable", http.StatusServiceUnavailable)
		return
	}
	session, err := h.Locator.LocateInventoryBin(r.Context(), partID, binID, user.ID)
	if err != nil {
		physicalLocateError(h, w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/storage/drawers/%d?bin_id=%d", session.DrawerID, session.BinID), http.StatusSeeOther)
}

func (h *Handler) HandlePhysicalDrawerLocate(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	drawerID, drawerErr := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	binID, binErr := strconv.ParseInt(r.FormValue("bin_id"), 10, 64)
	if drawerErr != nil || binErr != nil || drawerID <= 0 || binID <= 0 {
		h.UIError.Respond(w, r, nil, "Select a valid bin in this physical drawer", http.StatusBadRequest)
		return
	}
	if h.Locator == nil {
		h.UIError.Respond(w, r, nil, "Drawer LED Locate service is unavailable", http.StatusServiceUnavailable)
		return
	}
	session, err := h.Locator.LocateDrawerBin(r.Context(), drawerID, binID, user.ID)
	if err != nil {
		physicalLocateError(h, w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/storage/drawers/%d?bin_id=%d", session.DrawerID, session.BinID), http.StatusSeeOther)
}

func (h *Handler) HandlePhysicalDrawerStop(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	drawerID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || drawerID <= 0 {
		h.UIError.Respond(w, r, nil, "Invalid physical drawer id", http.StatusBadRequest)
		return
	}
	if h.Locator == nil {
		h.UIError.Respond(w, r, nil, "Drawer LED Locate service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := h.Locator.StopDrawerOperation(r.Context(), drawerID, user.ID, r.FormValue("operation_id")); err != nil {
		physicalLocateError(h, w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/storage/drawers/%d?stopped=1", drawerID), http.StatusSeeOther)
}

// HandleCabinetControllerOutputDiscovery performs a read-only WLED topology
// request for a controller that is currently assigned to this cabinet.
func (h *Handler) HandleCabinetControllerOutputDiscovery(w http.ResponseWriter, r *http.Request) {
	cabinetID, cabinetErr := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	controllerID, controllerErr := strconv.ParseInt(chi.URLParam(r, "controllerID"), 10, 64)
	if cabinetErr != nil || controllerErr != nil || cabinetID <= 0 || controllerID <= 0 {
		http.Error(w, `{"error":"invalid cabinet or controller id"}`, http.StatusBadRequest)
		return
	}
	members, err := h.Queries.ListCabinetControllers(r.Context(), cabinetID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.UIError.Respond(w, r, err, "Could not read cabinet controller assignments", http.StatusInternalServerError)
		return
	}
	assigned := false
	for _, member := range members {
		if member.ControllerID == controllerID {
			assigned = true
			break
		}
	}
	if !assigned {
		http.Error(w, `{"error":"controller is not assigned to this cabinet"}`, http.StatusNotFound)
		return
	}
	if h.Locator == nil {
		http.Error(w, `{"error":"read-only WLED output discovery is unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	layout, err := h.Locator.DiscoverPhysicalOutputs(r.Context(), controllerID)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(layout); err != nil {
		return
	}
}

func (h *Handler) HandleControllerLocateMappingSave(w http.ResponseWriter, r *http.Request) {
	controllerID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || controllerID <= 0 {
		h.UIError.Respond(w, r, nil, "Invalid controller id", http.StatusBadRequest)
		return
	}
	if h.Locator == nil {
		h.UIError.Respond(w, r, nil, "WLED mapping verification service is unavailable", http.StatusServiceUnavailable)
		return
	}
	verified := r.FormValue("locate_mapping_verified") == "verified" && r.FormValue("locate_ranges_reserved") == "reserved"
	if err := h.Locator.SaveControllerMappingVerification(r.Context(), controllerID, verified); err != nil {
		physicalLocateError(h, w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/hardware/%d/grid", controllerID), http.StatusSeeOther)
}

func physicalLocateError(h *Handler, w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, wled.ErrPhysicalOutputDisabled), errors.Is(err, wled.ErrPhysicalBinMissing), errors.Is(err, wled.ErrNoDrawerLEDAllocation), errors.Is(err, wled.ErrLocateMappingUnverified), errors.Is(err, wled.ErrLocateTopology), errors.Is(err, wled.ErrLocateSessionStale), errors.Is(err, wled.ErrLocateSessionOwned), errors.Is(err, wled.ErrLocateSessionActive):
		status = http.StatusConflict
	case errors.Is(err, sql.ErrNoRows):
		status = http.StatusNotFound
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || r.Context().Err() != nil:
		status = http.StatusGatewayTimeout
	}
	h.UIError.Respond(w, r, err, "Drawer LED Locate failed: "+err.Error(), status)
}
