package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
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

type drawerPixelMappingPreviewPiece struct {
	ControllerID int64 `json:"controller_id"`
	StringID     int64 `json:"string_id"`
	CabinetStart int64 `json:"cabinet_start"`
	LocalStart   int64 `json:"local_start"`
	StringStart  int64 `json:"string_start"`
	Count        int64 `json:"count"`
}

type drawerPixelMappingPreviewAllocation struct {
	ID         int64                            `json:"id"`
	StartPixel int64                            `json:"start_pixel"`
	PixelCount int64                            `json:"pixel_count"`
	Pieces     []drawerPixelMappingPreviewPiece `json:"pieces"`
}

// HandleDrawerPixelMappingPreview returns the saved allocation's exact mapped
// physical pixels from the canonical cabinet topology engine. It is read-only.
func (h *Handler) HandleDrawerPixelMappingPreview(w http.ResponseWriter, r *http.Request) {
	cabinetID, cabinetErr := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	drawerID, drawerErr := strconv.ParseInt(chi.URLParam(r, "drawerID"), 10, 64)
	if cabinetErr != nil || drawerErr != nil || cabinetID <= 0 || drawerID <= 0 {
		http.Error(w, `{"error":"invalid cabinet or drawer id"}`, http.StatusBadRequest)
		return
	}
	layout, err := h.layoutService().Get(r.Context(), cabinetID)
	if errors.Is(err, storage.ErrLayoutNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.UIError.Respond(w, r, err, "Could not calculate the saved drawer pixel mapping", http.StatusInternalServerError)
		return
	}
	var drawer *storage.DrawerLayout
	for i := range layout.Drawers {
		if layout.Drawers[i].Drawer.ID == drawerID {
			drawer = &layout.Drawers[i]
			break
		}
	}
	if drawer == nil {
		http.NotFound(w, r)
		return
	}
	allocations := make([]drawerPixelMappingPreviewAllocation, 0, len(drawer.LedAllocations))
	for _, allocation := range drawer.LedAllocations {
		pieces, err := cabinetpixels.SplitTopologyRange(layout.Wiring, allocation.StartPixel, allocation.PixelCount)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("saved allocation #%d cannot be resolved: %v", allocation.ID, err)})
			return
		}
		preview := drawerPixelMappingPreviewAllocation{
			ID: allocation.ID, StartPixel: allocation.StartPixel, PixelCount: allocation.PixelCount,
			Pieces: make([]drawerPixelMappingPreviewPiece, len(pieces)),
		}
		for i, piece := range pieces {
			preview.Pieces[i] = drawerPixelMappingPreviewPiece{
				ControllerID: piece.ControllerID, StringID: piece.StringID, CabinetStart: piece.CabinetStart,
				LocalStart: piece.LocalStart, StringStart: piece.StringStart, Count: piece.Count,
			}
		}
		allocations = append(allocations, preview)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"cabinet_id": cabinetID, "drawer_id": drawerID, "electrical_revision": layout.Cabinet.ElectricalRevision, "allocations": allocations})
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

func (h *Handler) HandlePhysicalBinCreate(w http.ResponseWriter, r *http.Request) {
	cabinetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	drawerID, drawerErr := strconv.ParseInt(chi.URLParam(r, "drawerID"), 10, 64)
	if err != nil || drawerErr != nil || cabinetID <= 0 || drawerID <= 0 {
		http.Error(w, "invalid physical bin request", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid physical bin request", http.StatusBadRequest)
		return
	}
	parseFloat := func(name string) (float64, error) {
		return strconv.ParseFloat(strings.TrimSpace(r.FormValue(name)), 64)
	}
	revision, revisionErr := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	x, xErr := parseFloat("x")
	y, yErr := parseFloat("y")
	width, widthErr := parseFloat("width")
	height, heightErr := parseFloat("height")
	if revisionErr != nil || xErr != nil || yErr != nil || widthErr != nil || heightErr != nil {
		layoutMutationError(w, r, cabinetID, errors.New("invalid bin placement fields"))
		return
	}
	binID, err := h.layoutService().CreatePhysicalBin(r.Context(), cabinetID, drawerID, revision, r.FormValue("name"), x, y, width, height)
	if err != nil {
		layoutMutationError(w, r, cabinetID, err)
		return
	}
	target := fmt.Sprintf("/storage/cabinets/%d/edit?drawer_id=%d&bin_id=%d", cabinetID, drawerID, binID)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *Handler) HandlePhysicalBinMove(w http.ResponseWriter, r *http.Request) {
	cabinetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	binID, binErr := strconv.ParseInt(chi.URLParam(r, "binID"), 10, 64)
	if err != nil || binErr != nil || cabinetID <= 0 || binID <= 0 {
		http.Error(w, "invalid physical bin move request", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid physical bin move request", http.StatusBadRequest)
		return
	}
	targetDrawerID, drawerErr := strconv.ParseInt(r.FormValue("target_drawer_id"), 10, 64)
	revision, revisionErr := strconv.ParseInt(r.FormValue("revision"), 10, 64)
	x, xErr := strconv.ParseFloat(strings.TrimSpace(r.FormValue("x")), 64)
	y, yErr := strconv.ParseFloat(strings.TrimSpace(r.FormValue("y")), 64)
	if drawerErr != nil || revisionErr != nil || xErr != nil || yErr != nil {
		layoutMutationError(w, r, cabinetID, errors.New("invalid bin move fields"))
		return
	}
	if err := h.layoutService().MovePhysicalBin(r.Context(), cabinetID, binID, targetDrawerID, revision, x, y); err != nil {
		layoutMutationError(w, r, cabinetID, err)
		return
	}
	target := fmt.Sprintf("/storage/cabinets/%d/edit?drawer_id=%d&bin_id=%d", cabinetID, targetDrawerID, binID)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *Handler) HandleCabinetControllerSave(w http.ResponseWriter, r *http.Request) {
	cabinetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	revision, revisionErr := strconv.ParseInt(r.FormValue("electrical_revision"), 10, 64)
	if err != nil || revisionErr != nil || r.ParseForm() != nil {
		http.Error(w, "invalid controller assignment request", http.StatusBadRequest)
		return
	}
	values := r.Form["controller_ids"]
	if len(values) == 0 && r.FormValue("controller_id") != "" {
		values = []string{r.FormValue("controller_id")}
	}
	controllerIDs := make([]int64, 0, len(values))
	for _, value := range values {
		id, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil {
			http.Error(w, "invalid controller assignment request", http.StatusBadRequest)
			return
		}
		if id > 0 {
			controllerIDs = append(controllerIDs, id)
		}
	}
	confirmMappingImpact := r.FormValue("confirm_mapping_impact") == "confirm"
	save := func() error {
		return h.layoutService().SaveCabinetControllersConfirmed(r.Context(), cabinetID, revision, controllerIDs, confirmMappingImpact)
	}
	var saveErr error
	if h.Locator != nil {
		saveErr = h.Locator.WithoutActiveSession(r.Context(), save)
	} else {
		saveErr = save()
	}
	if saveErr != nil {
		target := "/storage/cabinets/" + strconv.FormatInt(cabinetID, 10) + "/edit?controller_error=" + url.QueryEscape(saveErr.Error())
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
	controllerText := strings.TrimSpace(r.FormValue("controller_id"))
	controllerID := int64(0)
	if controllerText != "" {
		controllerID, err = strconv.ParseInt(controllerText, 10, 64)
		if err != nil || controllerID < 0 {
			http.Error(w, "invalid controller id", http.StatusBadRequest)
			return
		}
	}
	save := func() error {
		if r.FormValue("coordinate_space") == "cabinet" || controllerText == "" {
			return h.layoutService().SaveDrawerLEDAllocations(r.Context(), cabinetID, drawerID, revision, allocations)
		}
		return h.layoutService().SaveDrawerLEDAllocationsForController(r.Context(), cabinetID, drawerID, revision, controllerID, allocations)
	}
	var saveErr error
	if h.Locator != nil {
		saveErr = h.Locator.WithoutActiveSession(r.Context(), save)
	} else {
		saveErr = save()
	}
	if saveErr != nil {
		target := "/storage/cabinets/" + strconv.FormatInt(cabinetID, 10) + "/edit?led_error=" + url.QueryEscape(saveErr.Error())
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(cabinetID, 10)+"/edit?led_saved=1", http.StatusSeeOther)
}

func (h *Handler) HandleCabinetWiringSave(w http.ResponseWriter, r *http.Request) {
	cabinetID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || cabinetID <= 0 {
		http.Error(w, "invalid cabinet id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		layoutMutationError(w, r, cabinetID, fmt.Errorf("invalid wiring form: %w", err))
		return
	}
	var input storage.CabinetWiringInput
	if err := json.Unmarshal([]byte(r.FormValue("wiring")), &input); err != nil {
		layoutMutationError(w, r, cabinetID, fmt.Errorf("invalid cabinet wiring data: %w", err))
		return
	}
	revision, err := strconv.ParseInt(r.FormValue("electrical_revision"), 10, 64)
	if err != nil {
		layoutMutationError(w, r, cabinetID, errors.New("invalid electrical revision"))
		return
	}
	input.Revision = revision
	confirmMappingImpact := r.FormValue("confirm_mapping_impact") == "confirm"
	save := func() error {
		return h.layoutService().SaveCabinetWiring(r.Context(), cabinetID, input, confirmMappingImpact)
	}
	var saveErr error
	if h.Locator != nil {
		saveErr = h.Locator.WithoutActiveSession(r.Context(), save)
	} else {
		saveErr = save()
	}
	if saveErr != nil {
		target := "/storage/cabinets/" + strconv.FormatInt(cabinetID, 10) + "/edit?wiring_error=" + url.QueryEscape(saveErr.Error())
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/storage/cabinets/"+strconv.FormatInt(cabinetID, 10)+"/edit?wiring_saved=1", http.StatusSeeOther)
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
