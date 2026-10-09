package handler

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /drawers/{id} — the drawer (container) view. Scanning a drawer QR lands
// here; the page highlights the drawer's LEDs on load.
func (h *Handler) HandleDrawerDetail(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, nil, "Invalid drawer id", http.StatusBadRequest)
		return
	}

	drawer, err := h.Queries.GetContainer(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Drawer not found", http.StatusNotFound)
		return
	}

	cabinet, err := h.Queries.GetController(r.Context(), drawer.ControllerID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Cabinet not found", http.StatusNotFound)
		return
	}

	bins, err := h.Queries.GetBinsByContainer(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch bins", http.StatusInternalServerError)
		return
	}

	drawerBins := make([]pages.DrawerBin, 0, len(bins))
	for _, b := range bins {
		contents, _ := h.Queries.GetBinContents(r.Context(), sql.NullInt64{Int64: b.ID, Valid: true})
		drawerBins = append(drawerBins, pages.DrawerBin{
			ID:       b.ID,
			Name:     b.Name,
			LedIndex: b.LedIndex,
			Width:    b.Width,
			Contents: contents,
		})
	}

	pages.DrawerDetail(user, drawer, cabinet, drawerBins).Render(r.Context(), w)
}

// POST /drawers/{id}/locate — highlights the drawer's LEDs.
func (h *Handler) HandleDrawerLocate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, nil, "Invalid drawer id", http.StatusBadRequest)
		return
	}

	drawer, err := h.Queries.GetContainer(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Drawer not found", http.StatusNotFound)
		return
	}

	if err := h.WLED.LocateDrawer(r.Context(), drawer.ControllerID, id); err != nil {
		h.UIError.Respond(w, r, err, "Locate failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
