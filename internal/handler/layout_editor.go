package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
		layoutMutationError(w, r, id, fmt.Errorf("invalid form: %w", err))
		return
	}
	var input storage.LayoutInput
	if err = json.Unmarshal([]byte(r.FormValue("layout")), &input); err != nil {
		layoutMutationError(w, r, id, fmt.Errorf("invalid layout data: %w", err))
		return
	}
	if input.ID != id {
		layoutMutationError(w, r, id, errors.New("cabinet id mismatch"))
		return
	}
	err = h.layoutService().Save(r.Context(), input)
	if err != nil {
		layoutMutationError(w, r, id, err)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (h *Handler) HandleCabinetControllerSave(w http.ResponseWriter, r *http.Request) {
	cabinetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	controllerID, controllerErr := strconv.ParseInt(r.FormValue("controller_id"), 10, 64)
	revision, revisionErr := strconv.ParseInt(r.FormValue("electrical_revision"), 10, 64)
	if err != nil || controllerErr != nil || revisionErr != nil {
		http.Error(w, "invalid controller assignment request", http.StatusBadRequest)
		return
	}
	if err := h.layoutService().SaveCabinetController(r.Context(), cabinetID, revision, controllerID); err != nil {
		target := "/storage/cabinets/" + strconv.FormatInt(cabinetID, 10) + "/edit?controller_error=" + url.QueryEscape(err.Error())
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(cabinetID, 10)+"/edit?controller_saved=1", http.StatusSeeOther)
}

func (h *Handler) HandleDrawerLEDAllocationsSave(w http.ResponseWriter, r *http.Request) {
	cabinetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	drawerID, drawerErr := strconv.ParseInt(chi.URLParam(r, "drawerID"), 10, 64)
	if err != nil || drawerErr != nil {
		http.Error(w, "invalid cabinet or drawer id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		layoutMutationError(w, r, cabinetID, fmt.Errorf("invalid form: %w", err))
		return
	}
	revision, err := layoutRevision(r)
	if err != nil {
		layoutMutationError(w, r, cabinetID, errors.New("invalid electrical revision"))
		return
	}
	var allocations []storage.LEDAllocationInput
	if err := json.Unmarshal([]byte(r.FormValue("allocations")), &allocations); err != nil {
		layoutMutationError(w, r, cabinetID, fmt.Errorf("invalid LED allocation data: %w", err))
		return
	}
	if allocations == nil {
		allocations = []storage.LEDAllocationInput{}
	}
	if err := h.layoutService().SaveDrawerLEDAllocations(r.Context(), cabinetID, drawerID, revision, allocations); err != nil {
		target := "/storage/cabinets/" + strconv.FormatInt(cabinetID, 10) + "/edit?led_error=" + url.QueryEscape(err.Error())
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(cabinetID, 10)+"/edit?led_saved=1", http.StatusSeeOther)
}

func layoutRevision(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.FormValue("revision"), 10, 64)
}
func layoutMutationError(w http.ResponseWriter, r *http.Request, cabinetID int64, err error) {
	// Lifecycle requests originate in the editor. Preserve the actionable error
	// there instead of stranding the user on a raw HTTP error page.
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(cabinetID, 10)+"/edit?layout_error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
}
func (h *Handler) HandleLayoutCabinetArchive(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	id, e := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	rev, er := layoutRevision(r)
	if e != nil || er != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if er = h.layoutService().ArchiveCabinet(r.Context(), id, rev); er != nil {
		layoutMutationError(w, r, id, er)
		return
	}
	http.Redirect(w, r, "/storage/cabinets", http.StatusSeeOther)
}
func (h *Handler) HandleLayoutCabinetDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	id, e := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	rev, er := layoutRevision(r)
	if e != nil || er != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if er = h.layoutService().DeleteCabinet(r.Context(), id, rev); er != nil {
		layoutMutationError(w, r, id, er)
		return
	}
	http.Redirect(w, r, "/storage/cabinets", http.StatusSeeOther)
}
func (h *Handler) HandleLayoutDrawerArchive(w http.ResponseWriter, r *http.Request) {
	h.handleDrawerLifecycle(w, r, true)
}
func (h *Handler) HandleLayoutDrawerDelete(w http.ResponseWriter, r *http.Request) {
	h.handleDrawerLifecycle(w, r, false)
}
func (h *Handler) handleDrawerLifecycle(w http.ResponseWriter, r *http.Request, archive bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	cab, e := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	drawer, de := strconv.ParseInt(chi.URLParam(r, "drawerID"), 10, 64)
	rev, re := layoutRevision(r)
	if e != nil || de != nil || re != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	var err error
	if archive {
		err = h.layoutService().ArchiveDrawer(r.Context(), cab, drawer, rev)
	} else {
		err = h.layoutService().DeleteDrawer(r.Context(), cab, drawer, rev)
	}
	if err != nil {
		layoutMutationError(w, r, cab, err)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(cab, 10)+"/edit", http.StatusSeeOther)
}
func (h *Handler) HandleLayoutBinDetach(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", 400)
		return
	}
	cab, e := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	bin, be := strconv.ParseInt(chi.URLParam(r, "binID"), 10, 64)
	rev, re := layoutRevision(r)
	if e != nil || be != nil || re != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	if err := h.layoutService().DetachBinPlacement(r.Context(), cab, bin, rev); err != nil {
		layoutMutationError(w, r, cab, err)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(cab, 10)+"/edit", http.StatusSeeOther)
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
