package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/web/pages"
)

// Physical storage browsing (M6) — read-only HTTP surface. Nothing here
// contacts WLED or a locate service, and nothing writes.

func (h *Handler) browseService() *storage.BrowseService {
	return storage.NewBrowseService(h.Queries)
}

// HandleStorageCabinets renders GET /storage/cabinets: every active physical
// cabinet with its active drawers.
func (h *Handler) HandleStorageCabinets(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	cabinets, err := h.browseService().ListCabinets(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load cabinets", http.StatusInternalServerError)
		return
	}

	pages.StorageCabinets(cabinets, user).Render(r.Context(), w)
}

// HandleStorageCabinet renders GET /storage/cabinets/{id}: one cabinet and its
// active drawers. An absent cabinet id is a 404; a non-numeric id is a 400.
func (h *Handler) HandleStorageCabinet(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, nil, "Invalid cabinet id", http.StatusBadRequest)
		return
	}

	cabinet, err := h.browseService().GetCabinet(r.Context(), id)
	if errors.Is(err, storage.ErrCabinetNotFound) {
		h.UIError.Respond(w, r, err, "Cabinet not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load cabinet", http.StatusInternalServerError)
		return
	}

	pages.StorageCabinet(cabinet, user).Render(r.Context(), w)
}

// HandleStorageDrawer renders GET /storage/drawers/{id}: one drawer, the bins
// physically attached to it and those bins' contents. An absent drawer id is a
// 404; a non-numeric id is a 400. An attachment that references a missing bin
// surfaces as a server error rather than being hidden.
func (h *Handler) HandleStorageDrawer(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, nil, "Invalid drawer id", http.StatusBadRequest)
		return
	}

	detail, err := h.browseService().GetDrawer(r.Context(), id)
	if errors.Is(err, storage.ErrDrawerNotFound) {
		h.UIError.Respond(w, r, err, "Drawer not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load drawer", http.StatusInternalServerError)
		return
	}

	pages.StorageDrawer(detail, user).Render(r.Context(), w)
}
