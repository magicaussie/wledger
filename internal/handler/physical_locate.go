package handler

import (
	"context"
	"database/sql"
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
	if err := h.Locator.StopDrawer(r.Context(), drawerID, user.ID); err != nil {
		physicalLocateError(h, w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/storage/drawers/%d?stopped=1", drawerID), http.StatusSeeOther)
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
	case errors.Is(err, wled.ErrPhysicalBinMissing), errors.Is(err, wled.ErrNoDrawerLEDAllocation), errors.Is(err, wled.ErrLocateMappingUnverified), errors.Is(err, wled.ErrLocateTopology), errors.Is(err, wled.ErrLocateSessionStale), errors.Is(err, wled.ErrLocateSessionOwned), errors.Is(err, wled.ErrLocateSessionActive):
		status = http.StatusConflict
	case errors.Is(err, sql.ErrNoRows):
		status = http.StatusNotFound
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || r.Context().Err() != nil:
		status = http.StatusGatewayTimeout
	}
	h.UIError.Respond(w, r, err, "Drawer LED Locate failed: "+err.Error(), status)
}
