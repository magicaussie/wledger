package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/categories"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/productdetails"
	"github.com/tuxedocurly/wledger/internal/qrcode"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/internal/wled"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

// --- LIST & DETAIL ---

// GET /parts
func (h *Handler) HandlePartsList(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	search := r.URL.Query().Get("q")
	pageStr := r.URL.Query().Get("page")
	binStr := r.URL.Query().Get("bin")
	scroll := r.URL.Query().Get("scroll") == "true"

	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}

	var binID *int64
	if binStr != "" {
		id, err := strconv.ParseInt(binStr, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid bin id", http.StatusBadRequest)
			return
		}
		if _, err := h.Queries.GetBin(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Bin not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load bin", http.StatusInternalServerError)
			}
			return
		}
		binID = &id
	}

	viewParts, err := h.Parts.ListParts(r.Context(), search, page, binID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch parts", http.StatusInternalServerError)
		return
	}

	// Render logic
	if scroll {
		// If infinite scroll, return JUST the new cards (appended to bottom)
		pages.PartCards(user, viewParts, search, page, binID).Render(r.Context(), w)
	} else {
		// If full page load or search replacement, return the full wrapper
		pages.PartsList(user, viewParts, search, page, binID).Render(r.Context(), w)
	}
}

// binScanPrefix and partScanPrefix identify scannable codes produced by
// WLEDger's printed QR labels so the scan router can route them precisely.
const (
	binScanPrefix             = "wledger:bin:"
	partScanPrefix            = "wledger:part:"
	partIDScanPrefix          = "wledger:part-id:"
	cabinetScanPrefix         = "wledger:cabinet:"
	drawerScanPrefix          = "wledger:drawer:"
	physicalCabinetScanPrefix = "wledger:physical-cabinet:"
	physicalDrawerScanPrefix  = "wledger:physical-drawer:"
)

// BinScanCode returns the code encoded into a bin QR label.
func BinScanCode(binID int64) string {
	return fmt.Sprintf("%s%d", binScanPrefix, binID)
}

// CabinetScanCode returns the code encoded into a cabinet QR label. A cabinet is
// a controller.
func CabinetScanCode(controllerID int64) string {
	return fmt.Sprintf("%s%d", cabinetScanPrefix, controllerID)
}

// DrawerScanCode returns the code encoded into a drawer QR label. A drawer is a
// container.
func DrawerScanCode(containerID int64) string {
	return fmt.Sprintf("%s%d", drawerScanPrefix, containerID)
}

// PhysicalCabinetScanCode returns a code scoped to a physical storage cabinet.
// Legacy wledger:cabinet codes continue to identify electrical controllers.
func PhysicalCabinetScanCode(cabinetID int64) string {
	return fmt.Sprintf("%s%d", physicalCabinetScanPrefix, cabinetID)
}

// PhysicalDrawerScanCode returns a code scoped to a physical storage drawer.
// Legacy wledger:drawer codes continue to identify electrical containers.
func PhysicalDrawerScanCode(drawerID int64) string {
	return fmt.Sprintf("%s%d", physicalDrawerScanPrefix, drawerID)
}

// PartScanCode uses the stable database identity rather than a barcode value,
// which may be ambiguous or absent. Existing wledger:part:<barcode> labels
// remain supported by HandleScan.
func PartScanCode(part db.Part) string {
	return fmt.Sprintf("%s%d", partIDScanPrefix, part.ID)
}

// GET /parts/{id}/qr returns a QR PNG label that resolves by stable part ID.
func (h *Handler) HandlePartQR(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid part id", http.StatusBadRequest)
		return
	}

	part, err := h.Queries.GetPart(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Part not found", http.StatusNotFound)
		} else {
			h.UIError.Respond(w, r, err, "Failed to load part", http.StatusInternalServerError)
		}
		return
	}

	png, err := qrcode.PNG(PartScanCode(part), qrScaleFromQuery(r))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate QR", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
}

// qrScaleFromQuery reads an optional ?s= scale parameter, defaulting to 12.
func qrScaleFromQuery(r *http.Request) int {
	if s := r.URL.Query().Get("s"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return 12
}

// GET /parts/labels — renders a printable sheet of QR labels for every part
// that has a barcode. Attach the labels to the physical items / shelf so a
// scanner can find the matching part record.
func (h *Handler) HandleProductLabels(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	all, err := h.Queries.GetAllParts(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch parts", http.StatusInternalServerError)
		return
	}

	var parts []pages.ProductLabel
	for _, p := range all {
		barcode := strings.TrimSpace(p.BarcodeData.String)
		parts = append(parts, pages.ProductLabel{
			ID:      p.ID,
			Name:    p.Name,
			Barcode: barcode,
		})
	}

	pages.ProductLabels(user, parts).Render(r.Context(), w)
}

// GET /scan?q=<scanned> resolves validated physical and legacy identities to
// their existing browse pages. Legacy cabinet/drawer prefixes retain their
// controller/container meanings; unknown plain values remain manual searches.
func (h *Handler) HandleScan(w http.ResponseWriter, r *http.Request) {
	resolution, err := h.resolveScanCode(r, r.URL.Query().Get("q"))
	if err != nil {
		writeScanResolutionError(w, err)
		return
	}

	path := resolution.Path
	switch resolution.Type {
	case "bin":
		path = fmt.Sprintf("/parts?bin=%d", resolution.ID)
	case "physical-cabinet":
		path = fmt.Sprintf("/storage/cabinets/%d", resolution.ID)
	case "physical-drawer":
		path = fmt.Sprintf("/storage/drawers/%d", resolution.ID)
	case "legacy-controller":
		path = fmt.Sprintf("/hardware/%d/grid", resolution.ID)
	case "legacy-container":
		path = fmt.Sprintf("/drawers/%d", resolution.ID)
	case "part":
		path = fmt.Sprintf("/parts/%d", resolution.ID)
	case "category":
		path = fmt.Sprintf("/categories/%d", resolution.ID)
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func scanIdentityID(code, prefix string) (int64, bool) {
	value := strings.TrimPrefix(code, prefix)
	id, err := strconv.ParseInt(value, 10, 64)
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

// GET /parts/{id}
func (h *Handler) HandlePartDetail(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	detail, err := h.Parts.GetPartDetail(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Part not found", http.StatusNotFound)
		return
	}

	category, categoryErr := categories.NewService(h.Database).ForPart(r.Context(), int64(id))
	if categoryErr != nil && !errors.Is(categoryErr, categories.ErrNotFound) {
		h.UIError.Respond(w, r, categoryErr, "Failed to load product category", http.StatusInternalServerError)
		return
	}
	specifications, err := categories.NewService(h.Database).Specifications(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load product specifications", http.StatusInternalServerError)
		return
	}
	productDetails, err := productdetails.GetExtendedDetails(r.Context(), h.Database, int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load extended product details", http.StatusInternalServerError)
		return
	}
	pages.PartDetail(user, detail.Part, detail.Stock, detail.Links, detail.Docs, detail.Controllers, category, specifications, productDetails).Render(r.Context(), w)
}

// -----------------------------------------------------------
// CREATE & UPDATE
// -----------------------------------------------------------

// GET /parts/new
func (h *Handler) HandlePartsNew(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	allTags, _ := h.Tags.ListAllTags(r.Context())
	allCategories, selectedID, attributes, values, err := h.categoryFormData(r, 0)
	if err != nil {
		categoryError(h, w, r, err, "Failed to load category specifications")
		return
	}
	pages.PartCreate(user, allTags, allCategories, selectedID, attributes, values, productdetails.ExtendedDetails{Currency: "AUD"}).Render(r.Context(), w)
}

// POST /parts
func (h *Handler) HandlePartsCreate(w http.ResponseWriter, r *http.Request) {
	// ParseMultipartForm's maxMemory is only a memory threshold; cap the whole
	// request body so an oversized upload cannot be spooled without bound.
	if !limitRequestBody(w, r, config.MaxUploadSizeParts) {
		return
	}
	req, err := h.parsePartCreateRequest(r)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to parse form", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	productDetails, err := parsePartDetails(r, 0)
	if err != nil {
		productDetailsError(h, w, r, err)
		return
	}
	if err := productdetails.ValidateExtendedDetails(r.Context(), h.Database, productDetails, 0); err != nil {
		productDetailsError(h, w, r, err)
		return
	}

	selectedID, specifications, err := h.parsePartCategory(r)
	if err != nil {
		categoryError(h, w, r, err, "Invalid product category or specifications")
		return
	}
	if err := categories.NewService(h.Database).ValidateSpecifications(r.Context(), categoryIDValue(selectedID), specifications); err != nil {
		categoryError(h, w, r, err, "Invalid product specifications")
		return
	}

	newID, err := h.Parts.CreatePart(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, parts.ErrInvalidLinkURL):
			h.UIError.Respond(w, r, err, "Invalid link URL: only http:// and https:// links are allowed", http.StatusBadRequest)
		case errors.Is(err, parts.ErrReservedBarcode):
			h.UIError.Respond(w, r, err, "Barcode values starting with wledger: are reserved for internal scan identities", http.StatusBadRequest)
		case strings.Contains(err.Error(), "UNIQUE constraint failed"):
			h.UIError.Respond(w, r, err, "Part already exists (check barcode)", http.StatusConflict)
		default:
			h.UIError.Respond(w, r, err, "Failed to create part", http.StatusInternalServerError)
		}
		return
	}
	productDetails.PartID = newID
	if err := productdetails.SaveExtendedDetails(r.Context(), h.Database, productDetails); err != nil {
		productDetailsError(h, w, r, err)
		return
	}
	if err := categories.NewService(h.Database).SetProductCategory(r.Context(), newID, selectedID, specifications); err != nil {
		categoryError(h, w, r, err, "Failed to save product category and specifications")
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", newID), http.StatusSeeOther)
}

// GET /parts/{id}/edit
func (h *Handler) HandlePartEdit(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	detail, err := h.Parts.GetPartDetail(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Part not found", http.StatusNotFound)
		return
	}

	tags, _ := h.Queries.GetTagsForPart(r.Context(), int64(id))
	allTags, _ := h.Tags.ListAllTags(r.Context())
	allCategories, selectedID, attributes, values, err := h.categoryFormData(r, int64(id))
	if err != nil {
		categoryError(h, w, r, err, "Failed to load category specifications")
		return
	}

	productDetails, err := productdetails.GetExtendedDetails(r.Context(), h.Database, int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load extended product details", http.StatusInternalServerError)
		return
	}
	pages.PartEdit(user, detail.Part, tags, allTags, detail.Links, detail.Docs, allCategories, selectedID, attributes, values, productDetails).Render(r.Context(), w)
}

// POST /parts/{id}/update
func (h *Handler) HandlePartUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	// ParseMultipartForm's maxMemory is only a memory threshold; cap the whole
	// request body so an oversized upload cannot be spooled without bound.
	if !limitRequestBody(w, r, config.MaxUploadSizeParts) {
		return
	}
	req, err := h.parsePartUpdateRequest(r, int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to parse form", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	productDetails, err := parsePartDetails(r, int64(id))
	if err != nil {
		productDetailsError(h, w, r, err)
		return
	}
	if err := productdetails.ValidateExtendedDetails(r.Context(), h.Database, productDetails, int64(id)); err != nil {
		productDetailsError(h, w, r, err)
		return
	}

	selectedID, specifications, err := h.parsePartCategory(r)
	if err != nil {
		categoryError(h, w, r, err, "Invalid product category or specifications")
		return
	}
	if err := categories.NewService(h.Database).ValidateSpecifications(r.Context(), categoryIDValue(selectedID), specifications); err != nil {
		categoryError(h, w, r, err, "Invalid product specifications")
		return
	}

	err = h.Parts.UpdatePart(r.Context(), req)
	if err != nil {
		if errors.Is(err, parts.ErrInvalidLinkURL) {
			h.UIError.Respond(w, r, err, "Invalid link URL: only http:// and https:// links are allowed", http.StatusBadRequest)
			return
		}
		if errors.Is(err, parts.ErrReservedBarcode) {
			h.UIError.Respond(w, r, err, "Barcode values starting with wledger: are reserved for internal scan identities", http.StatusBadRequest)
			return
		}
		h.UIError.Respond(w, r, err, "Failed to update part", http.StatusInternalServerError)
		return
	}
	productDetails.PartID = int64(id)
	if err := productdetails.SaveExtendedDetails(r.Context(), h.Database, productDetails); err != nil {
		productDetailsError(h, w, r, err)
		return
	}
	if err := categories.NewService(h.Database).SetProductCategory(r.Context(), int64(id), selectedID, specifications); err != nil {
		categoryError(h, w, r, err, "Failed to save product category and specifications")
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", id), http.StatusSeeOther)
}

// POST /parts/{id}/delete
func (h *Handler) HandlePartDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	partID := int64(id)

	err := h.Parts.DeletePart(r.Context(), partID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete part", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/parts", http.StatusSeeOther)
}

// DELETE /parts/bulk
func (h *Handler) HandlePartsBulkDelete(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.UIError.Respond(w, r, err, "Failed to parse form", http.StatusBadRequest)
		return
	}

	idStrings := r.Form["ids"]
	if len(idStrings) == 0 {
		// Try to parse from JSON if form is empty (HTMX can send both depending on config)
		h.Logger.Warn("no IDs provided for bulk delete")
	}

	var ids []int64
	for _, s := range idStrings {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		h.UIError.Respond(w, r, nil, "No parts selected", http.StatusBadRequest)
		return
	}

	err := h.Parts.DeleteParts(r.Context(), ids)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete parts", http.StatusInternalServerError)
		return
	}

	// For HTMX response:
	// Redirect to /parts to ensure a clean state
	w.Header().Set("HX-Redirect", "/parts")
	w.WriteHeader(http.StatusOK)
}

// -----------------------------------------------------------
// SUB-RESOURCES (HTMX)
// -----------------------------------------------------------

// DELETE /parts/links/{id}
func (h *Handler) HandleLinkDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	err := h.Documents.DeleteLink(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to delete part link", "err", err, "link_id", id)
	}
	w.WriteHeader(http.StatusOK)
}

// DELETE /parts/docs/{id}
func (h *Handler) HandleDocDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	err := h.Documents.DeleteDocument(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to delete part doc", "err", err, "doc_id", id)
	}
	w.WriteHeader(http.StatusOK)
}

// -----------------------------------------------------------
// STOCK & BINS
// -----------------------------------------------------------

func (h *Handler) HandleBinOptions(w http.ResponseWriter, r *http.Request) {
	cid, _ := strconv.Atoi(r.URL.Query().Get("controller_id"))

	containers, err := h.Queries.GetContainersByController(r.Context(), int64(cid))
	if err != nil {
		h.Logger.Error("failed to fetch containers", "err", err, "controller_id", cid)
		components.BinOptions([]db.Bin{}).Render(r.Context(), w)
		return
	}

	var allBins []db.Bin
	for _, c := range containers {
		bins, err := h.Queries.GetBinsByContainer(r.Context(), sql.NullInt64{Int64: c.ID, Valid: true})
		if err == nil {
			allBins = append(allBins, bins...)
		}
	}

	components.BinOptions(allBins).Render(r.Context(), w)
}

func (h *Handler) HandleBinPicker(w http.ResponseWriter, r *http.Request) {
	cidStr := r.URL.Query().Get("controller_id")
	cid, _ := strconv.ParseInt(cidStr, 10, 64)

	if cid == 0 {
		h.Logger.Error("HandleBinPicker: invalid or missing controller_id")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	grid, err := h.Dashboard.GetGridByController(r.Context(), cid)
	if err != nil {
		h.Logger.Error("failed to fetch bin picker grid", "err", err, "controller_id", cid)
		// Return empty state component if not found or error
		components.BinPickerGrid(components.DashboardController{}).Render(r.Context(), w)
		return
	}

	components.BinPickerGrid(*grid).Render(r.Context(), w)
}

func (h *Handler) HandlePartAssign(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	binID, _ := strconv.Atoi(r.FormValue("bin_id"))
	qty, _ := strconv.Atoi(r.FormValue("quantity"))

	if binID == 0 || qty <= 0 {
		h.UIError.Respond(w, r, nil, "Invalid input: positive quantity and valid bin required", http.StatusBadRequest)
		return
	}

	err := h.Stock.AssignStock(r.Context(), stock.AssignStockRequest{
		PartID:   int64(partID),
		BinID:    int64(binID),
		Quantity: qty,
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to assign stock", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockMove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))
	targetBinID, _ := strconv.Atoi(r.FormValue("bin_id"))

	if targetBinID == 0 {
		h.UIError.Respond(w, r, nil, "Invalid target bin", http.StatusBadRequest)
		return
	}

	err := h.Stock.MoveStock(r.Context(), stock.MoveStockRequest{
		PartID:       int64(partID),
		AssignmentID: int64(assignmentID),
		TargetBinID:  int64(targetBinID),
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to move stock", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockRemove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))

	err := h.Stock.RemoveStock(r.Context(), stock.RemoveStockRequest{
		PartID:       int64(partID),
		AssignmentID: int64(assignmentID),
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to remove stock", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockAdjust(w http.ResponseWriter, r *http.Request) {
	partID, partErr := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	assignmentID, assignmentErr := strconv.ParseInt(chi.URLParam(r, "assignment_id"), 10, 64)
	delta, deltaErr := strconv.Atoi(r.URL.Query().Get("delta"))

	if partErr != nil || assignmentErr != nil || deltaErr != nil || partID <= 0 || assignmentID <= 0 || delta == 0 {
		h.UIError.Respond(w, r, nil, "Invalid stock adjustment", http.StatusBadRequest)
		return
	}

	err := h.Stock.AdjustStock(r.Context(), stock.AdjustStockRequest{
		PartID:       partID,
		AssignmentID: assignmentID,
		Delta:        delta,
	})
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to adjust stock", http.StatusInternalServerError)
		return
	}

	// If HTMX request, render just the row
	if r.Header.Get("HX-Request") == "true" {
		stock, err := h.Queries.GetPartAssignments(r.Context(), int64(partID))
		if err != nil {
			http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
			return
		}

		var targetRow db.GetPartAssignmentsRow
		found := false
		for _, s := range stock {
			if s.ID == int64(assignmentID) {
				targetRow = s
				found = true
				break
			}
		}

		if !found {
			http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
			return
		}

		controllers, _ := h.Queries.GetControllers(r.Context())
		user := auth.GetUserFromRequest(r)

		pages.StockRow(int64(partID), targetRow, user, controllers).Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

// POST /parts/{id}/favorite
func (h *Handler) HandlePartFavorite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, nil, "Invalid part id", http.StatusBadRequest)
		return
	}

	isFavorite, err := h.Parts.ToggleFavorite(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to update favorite", http.StatusInternalServerError)
		return
	}

	// HTMX: swap the button in place.
	if r.Header.Get("HX-Request") == "true" {
		components.PartFavoriteButton(id, isFavorite, true).Render(r.Context(), w)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", id), http.StatusSeeOther)
}

// POST /parts/{id}/locate
func (h *Handler) HandlePartLocate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	err := h.WLED.LocatePart(r.Context(), int64(id))
	if err != nil {
		if errors.Is(err, wled.ErrCoordinateSpaceUnresolved) || errors.Is(err, wled.ErrHardwareBusy) || errors.Is(err, wled.ErrPhysicalOutputDisabled) {
			h.UIError.Respond(w, r, err, "Locate unavailable: "+err.Error(), http.StatusConflict)
			return
		}
		h.UIError.Respond(w, r, err, "Locate failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// --- HELPERS ---

func parsePartDetails(r *http.Request, partID int64) (productdetails.ExtendedDetails, error) {
	parseNumber := func(name string) (sql.NullFloat64, error) {
		raw := strings.TrimSpace(r.FormValue(name))
		if raw == "" {
			return sql.NullFloat64{}, nil
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return sql.NullFloat64{}, productdetails.ErrInvalidDetails
		}
		return sql.NullFloat64{Float64: value, Valid: true}, nil
	}
	details := productdetails.ExtendedDetails{
		PartID: partID, SKU: r.FormValue("sku"), Brand: r.FormValue("brand"), Model: r.FormValue("model"),
		Colour: r.FormValue("colour"), Material: r.FormValue("material"), Finish: r.FormValue("finish"), Condition: r.FormValue("condition"),
		DimensionUnit: strings.TrimSpace(r.FormValue("dimension_unit")), WeightUnit: strings.TrimSpace(r.FormValue("weight_unit")),
		Notes: r.FormValue("product_notes"), PurchasedFrom: r.FormValue("purchased_from"), SupplierProductCode: r.FormValue("supplier_product_code"),
		Currency: r.FormValue("currency"), ProductURL: r.FormValue("product_url"), PurchaseNotes: r.FormValue("purchase_notes"),
	}
	if raw := strings.TrimSpace(r.FormValue("purchase_date")); raw != "" {
		details.PurchaseDate = sql.NullString{String: raw, Valid: true}
	}
	for _, field := range []struct {
		name   string
		target *sql.NullFloat64
	}{
		{"length_mm", &details.LengthMM}, {"width_mm", &details.WidthMM}, {"height_mm", &details.HeightMM},
		{"depth_mm", &details.DepthMM}, {"weight_g", &details.WeightG}, {"purchase_price", &details.PurchasePrice},
	} {
		value, err := parseNumber(field.name)
		if err != nil {
			return productdetails.ExtendedDetails{}, err
		}
		*field.target = value
	}
	if err := details.NormalizeAndValidate(); err != nil {
		return productdetails.ExtendedDetails{}, err
	}
	return details, nil
}

func productDetailsError(h *Handler, w http.ResponseWriter, r *http.Request, err error) {
	status, message := http.StatusInternalServerError, "Failed to save extended product details"
	if errors.Is(err, productdetails.ErrDuplicateSKU) {
		status, message = http.StatusConflict, "Internal SKU is already assigned to another product"
	} else if errors.Is(err, productdetails.ErrInvalidDetails) {
		status, message = http.StatusBadRequest, "Check product details, measurement values, date, currency and URL"
	}
	h.UIError.Respond(w, r, err, message, status)
}

func (h *Handler) parsePartCreateRequest(r *http.Request) (parts.CreatePartRequest, error) {
	err := r.ParseMultipartForm(config.MaxUploadSizeParts)
	if err != nil {
		return parts.CreatePartRequest{}, err
	}

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	req := parts.CreatePartRequest{
		Name:              r.FormValue("name"),
		Description:       r.FormValue("description"),
		PartNumber:        r.FormValue("part_number"),
		Manufacturer:      r.FormValue("manufacturer"),
		Supplier:          r.FormValue("supplier"),
		BarcodeData:       r.FormValue("barcode_data"),
		UnitCost:          cost,
		ReorderLevel:      reorder,
		MinStockThreshold: minStock,
		Footprint:         r.FormValue("footprint"),
		Tags:              h.parseFormTags(r.FormValue("tags")),
		Links:             h.parseFormLinks(r, ""),
		Documents:         h.parseFormDocuments(r),
	}

	// Handle Image
	if file, header, err := r.FormFile("image"); err == nil {
		req.Image = &parts.DocUpload{File: file, Header: header}
	}

	return req, nil
}

func (h *Handler) parsePartUpdateRequest(r *http.Request, id int64) (parts.UpdatePartRequest, error) {
	err := r.ParseMultipartForm(config.MaxUploadSizeParts)
	if err != nil {
		return parts.UpdatePartRequest{}, err
	}

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	req := parts.UpdatePartRequest{
		ID:                id,
		Name:              r.FormValue("name"),
		Description:       r.FormValue("description"),
		PartNumber:        r.FormValue("part_number"),
		Manufacturer:      r.FormValue("manufacturer"),
		Supplier:          r.FormValue("supplier"),
		BarcodeData:       r.FormValue("barcode_data"),
		UnitCost:          cost,
		ReorderLevel:      reorder,
		MinStockThreshold: minStock,
		Footprint:         r.FormValue("footprint"),
		RemoveImage:       r.FormValue("remove_image") == "true",
		Tags:              h.parseFormTags(r.FormValue("tags")),
		ExistingLinks:     h.parseFormLinks(r, "existing_"),
		NewLinks:          h.parseFormLinks(r, ""),
		NewDocuments:      h.parseFormDocuments(r),
	}

	// Handle Image
	if file, header, err := r.FormFile("image"); err == nil {
		req.Image = &parts.DocUpload{File: file, Header: header}
	}

	return req, nil
}

func (h *Handler) categoryFormData(r *http.Request, partID int64) ([]categories.Category, int64, []categories.Attribute, []categories.AttributeValue, error) {
	svc := categories.NewService(h.Database)
	all, err := svc.List(r.Context(), "")
	if err != nil {
		return nil, 0, nil, nil, err
	}
	selectedID := int64(0)
	originalID := int64(0)
	if partID > 0 {
		current, err := svc.ForPart(r.Context(), partID)
		if err == nil {
			selectedID, originalID = current.ID, current.ID
		} else if !errors.Is(err, categories.ErrNotFound) {
			return nil, 0, nil, nil, err
		}
	}
	if r.URL.Query().Has("category_id") {
		raw := strings.TrimSpace(r.URL.Query().Get("category_id"))
		if raw == "" {
			selectedID = 0
		} else {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return nil, 0, nil, nil, categories.ErrInvalid
			}
			selectedID = id
		}
	}
	if selectedID == 0 {
		return all, 0, nil, nil, nil
	}
	if _, err := svc.Get(r.Context(), selectedID); err != nil {
		return nil, 0, nil, nil, err
	}
	attributes, err := svc.ListAttributes(r.Context(), selectedID)
	if err != nil {
		return nil, 0, nil, nil, err
	}
	var values []categories.AttributeValue
	if partID > 0 && selectedID == originalID {
		values, err = svc.Specifications(r.Context(), partID)
		if err != nil {
			return nil, 0, nil, nil, err
		}
	}
	return all, selectedID, attributes, values, nil
}

func (h *Handler) parsePartCategory(r *http.Request) (*int64, []categories.Specification, error) {
	raw := strings.TrimSpace(r.FormValue("category_id"))
	if raw == "" {
		return nil, nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return nil, nil, categories.ErrInvalid
	}
	svc := categories.NewService(h.Database)
	if _, err := svc.Get(r.Context(), id); err != nil {
		return nil, nil, err
	}
	attributes, err := svc.ListAttributes(r.Context(), id)
	if err != nil {
		return nil, nil, err
	}
	var specifications []categories.Specification
	for _, attribute := range attributes {
		value := r.FormValue(fmt.Sprintf("spec_%d", attribute.ID))
		spec := categories.Specification{AttributeID: attribute.ID}
		switch attribute.DataType {
		case categories.TextType:
			if value == "" && !attribute.Required {
				continue
			}
			spec.Text = &value
		case categories.NumberType:
			if value == "" {
				if attribute.Required {
					continue
				}
				continue
			}
			number, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, nil, categories.ErrInvalidValue
			}
			spec.Number = &number
		case categories.BooleanType:
			if value == "" {
				if attribute.Required {
					continue
				}
				continue
			}
			boolean, err := strconv.ParseBool(value)
			if err != nil {
				return nil, nil, categories.ErrInvalidValue
			}
			spec.Boolean = &boolean
		default:
			return nil, nil, categories.ErrInvalidValue
		}
		specifications = append(specifications, spec)
	}
	return &id, specifications, nil
}

func categoryIDValue(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

func (h *Handler) parseFormTags(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

func (h *Handler) parseFormLinks(r *http.Request, prefix string) []parts.LinkDTO {
	var links []parts.LinkDTO
	ids := r.PostForm[prefix+"link_ids[]"]
	labels := r.PostForm[prefix+"link_labels[]"]
	urls := r.PostForm[prefix+"link_urls[]"]

	for i, u := range urls {
		if u == "" {
			continue
		}
		link := parts.LinkDTO{URL: u}
		if i < len(labels) {
			link.Label = labels[i]
		}
		if i < len(ids) {
			lid, _ := strconv.Atoi(ids[i])
			link.ID = int64(lid)
		}
		links = append(links, link)
	}
	return links
}

func (h *Handler) parseFormDocuments(r *http.Request) []parts.DocUpload {
	var uploads []parts.DocUpload
	if r.MultipartForm == nil || r.MultipartForm.File == nil {
		return nil
	}

	files := r.MultipartForm.File["documents"]
	for _, fh := range files {
		f, err := fh.Open()
		if err == nil {
			uploads = append(uploads, parts.DocUpload{
				File:   f,
				Header: fh,
			})
		}
	}
	return uploads
}
