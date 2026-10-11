package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/web/pages"
)

func (h *Handler) layoutService() *storage.LayoutService { return storage.NewLayoutService(h.Queries) }
func (h *Handler) HandleLayoutEditor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid cabinet id", 400)
		return
	}
	layout, err := h.layoutService().Get(r.Context(), id)
	if errors.Is(err, storage.ErrLayoutNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load physical layout", 500)
		return
	}
	pages.StorageLayoutEditor(layout, auth.GetUserFromRequest(r)).Render(r.Context(), w)
}
func (h *Handler) HandleLayoutSave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid cabinet id", 400)
		return
	}
	if err = r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	var input storage.LayoutInput
	if err = json.Unmarshal([]byte(r.FormValue("layout")), &input); err != nil {
		http.Error(w, "invalid layout data", 400)
		return
	}
	if input.ID != id {
		http.Error(w, "cabinet id mismatch", 400)
		return
	}
	err = h.layoutService().Save(r.Context(), input)
	if err != nil {
		status := 400
		if errors.Is(err, storage.ErrLayoutStale) {
			status = 409
		}
		if errors.Is(err, storage.ErrLayoutNotFound) {
			status = 404
		}
		http.Error(w, err.Error(), status)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
func (h *Handler) HandleLayoutCabinetCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	width, _ := strconv.ParseFloat(r.FormValue("width"), 64)
	height, _ := strconv.ParseFloat(r.FormValue("height"), 64)
	order, _ := strconv.ParseInt(r.FormValue("display_order"), 10, 64)
	id, err := h.layoutService().CreateCabinet(r.Context(), r.FormValue("name"), order, width, height)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
}
