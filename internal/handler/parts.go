package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

// --- LIST & DETAIL ---

// GET /parts
func (h *Handler) HandlePartsList(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	search := r.URL.Query().Get("q")
	pageStr := r.URL.Query().Get("page")
	scroll := r.URL.Query().Get("scroll") == "true" // Check for infinite scroll flag

	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}

	// Limit: 20 items per load (good balance for speed vs requests)
	limit := 20
	offset := (page - 1) * limit

	var viewParts []pages.PartView
	var err error

	if search != "" {
		// FTS5 Search
		query := search + "*"
		rows, searchErr := h.Queries.SearchParts(r.Context(), db.SearchPartsParams{
			PartsFts: sql.NullString{String: query, Valid: true},
			Limit:    int64(limit),
			Offset:   int64(offset),
		})

		if searchErr != nil {
			err = searchErr
		} else {
			for _, row := range rows {
				viewParts = append(viewParts, pages.PartView{
					ID:          row.ID,
					Name:        row.Name,
					Description: row.Description,
					PartNumber:  row.PartNumber,
					ImagePath:   row.ImagePath,
					IsFavorite:  row.IsFavorite,
					UnitCost:    row.UnitCost,
					TotalStock:  row.TotalStock,
				})
			}
		}
	} else {
		// Standard List
		rows, listErr := h.Queries.ListParts(r.Context(), db.ListPartsParams{
			Limit:  int64(limit),
			Offset: int64(offset),
		})
		if listErr != nil {
			err = listErr
		} else {
			for _, row := range rows {
				viewParts = append(viewParts, pages.PartView{
					ID:          row.ID,
					Name:        row.Name,
					Description: row.Description,
					PartNumber:  row.PartNumber,
					ImagePath:   row.ImagePath,
					IsFavorite:  row.IsFavorite,
					UnitCost:    row.UnitCost,
					TotalStock:  row.TotalStock,
				})
			}
		}
	}

	if err != nil {
		h.Logger.Error("failed to fetch parts", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Render logic
	if scroll {
		// If infinite scroll, return JUST the new cards (appended to bottom)
		pages.PartCards(viewParts, search, page).Render(r.Context(), w)
	} else {
		// If full page load or search replacement, return the full wrapper
		pages.PartsList(user, viewParts, search, page).Render(r.Context(), w)
	}
}

// GET /parts/{id}
func (h *Handler) HandlePartDetail(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	p, err := h.Queries.GetPart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}

	stock, _ := h.Queries.GetPartAssignments(r.Context(), int64(id))
	links, _ := h.Queries.GetPartLinks(r.Context(), int64(id))
	docs, _ := h.Queries.GetPartDocs(r.Context(), int64(id))
	controllers, _ := h.Queries.GetControllers(r.Context())

	pages.PartDetail(user, p, stock, links, docs, controllers).Render(r.Context(), w)
}

// -----------------------------------------------------------
// CREATE & UPDATE
// -----------------------------------------------------------

// GET /parts/new
func (h *Handler) HandlePartsNew(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	allTags, _ := h.Tags.ListAllTags(r.Context())
	pages.PartCreate(user, allTags).Render(r.Context(), w)
}

// POST /parts
func (h *Handler) HandlePartsCreate(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(config.MaxUploadSizeParts) // 100MB
	if err != nil {
		http.Error(w, "Request too large", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	var tagList []string
	if tagsRaw := r.FormValue("tags"); tagsRaw != "" {
		tagList = strings.Split(tagsRaw, ",")
	}

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
		Tags:              tagList,
	}

	// Handle Image Upload
	file, header, err := r.FormFile("image")
	if err == nil {
		req.Image = &parts.DocUpload{
			File:   file,
			Header: header,
		}
	}

	// Process Links
	labels := r.PostForm["link_labels[]"]
	urls := r.PostForm["link_urls[]"]
	if len(labels) == len(urls) {
		for i, u := range urls {
			if u == "" {
				continue
			}
			req.Links = append(req.Links, parts.LinkDTO{
				Label: labels[i],
				URL:   u,
			})
		}
	}

	// Process Documents
	docs := r.MultipartForm.File["documents"]
	for _, fh := range docs {
		f, err := fh.Open()
		if err == nil {
			req.Documents = append(req.Documents, parts.DocUpload{
				File:   f,
				Header: fh,
			})
		}
	}

	newID, err := h.Parts.CreatePart(r.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "Part already exists (check barcode)", http.StatusConflict)
		} else {
			h.Logger.Error("failed to create part", "error", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
		}
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", newID), http.StatusSeeOther)
}

// GET /parts/{id}/edit
func (h *Handler) HandlePartEdit(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	p, err := h.Queries.GetPart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}

	links, _ := h.Queries.GetPartLinks(r.Context(), int64(id))
	docs, _ := h.Queries.GetPartDocs(r.Context(), int64(id))
	tags, _ := h.Queries.GetTagsForPart(r.Context(), int64(id))
	allTags, _ := h.Tags.ListAllTags(r.Context())

	pages.PartEdit(user, p, tags, allTags, links, docs).Render(r.Context(), w)
}

// POST /parts/{id}/update
func (h *Handler) HandlePartUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	err := r.ParseMultipartForm(config.MaxUploadSizeParts)
	if err != nil {
		http.Error(w, "Request too large", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	var tagList []string
	if tagsRaw := r.FormValue("tags"); tagsRaw != "" {
		tagList = strings.Split(tagsRaw, ",")
	}

	req := parts.UpdatePartRequest{
		ID:                int64(id),
		Name:              r.FormValue("name"),
		Description:       r.FormValue("description"),
		PartNumber:        r.FormValue("part_number"),
		Manufacturer:      r.FormValue("manufacturer"),
		Supplier:          r.FormValue("supplier"),
		BarcodeData:       r.FormValue("barcode_data"),
		UnitCost:          cost,
		ReorderLevel:      reorder,
		MinStockThreshold: minStock,
		Tags:              tagList,
	}

	// Handle Image Upload
	file, header, err := r.FormFile("image")
	if err == nil {
		req.Image = &parts.DocUpload{
			File:   file,
			Header: header,
		}
	}

	// Update Existing Links
	existingIDs := r.PostForm["existing_link_ids[]"]
	existingLabels := r.PostForm["existing_link_labels[]"]
	existingUrls := r.PostForm["existing_link_urls[]"]

	if len(existingIDs) == len(existingLabels) && len(existingIDs) == len(existingUrls) {
		for i, idStr := range existingIDs {
			linkID, _ := strconv.Atoi(idStr)
			if linkID == 0 || existingUrls[i] == "" {
				continue
			}
			req.ExistingLinks = append(req.ExistingLinks, parts.LinkDTO{
				ID:    int64(linkID),
				Label: existingLabels[i],
				URL:   existingUrls[i],
			})
		}
	}

	// Add Links
	labels := r.PostForm["link_labels[]"]
	urls := r.PostForm["link_urls[]"]
	if len(labels) == len(urls) {
		for i, u := range urls {
			if u == "" {
				continue
			}
			req.NewLinks = append(req.NewLinks, parts.LinkDTO{
				Label: labels[i],
				URL:   u,
			})
		}
	}

	// Add Documents
	docs := r.MultipartForm.File["documents"]
	for _, fh := range docs {
		f, err := fh.Open()
		if err == nil {
			req.NewDocuments = append(req.NewDocuments, parts.DocUpload{
				File:   f,
				Header: fh,
			})
		}
	}

	err = h.Parts.UpdatePart(r.Context(), req)
	if err != nil {
		h.Logger.Error("failed to update part", "error", err)
		http.Error(w, "Update failed", http.StatusInternalServerError)
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
		h.Logger.Error("failed to delete part", "error", err)
		http.Error(w, "Delete failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/parts", http.StatusSeeOther)
}

// -----------------------------------------------------------
// SUB-RESOURCES (HTMX)
// -----------------------------------------------------------

// DELETE /parts/links/{id}
func (h *Handler) HandleLinkDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_ = h.Parts.DeleteLink(r.Context(), int64(id))
	w.WriteHeader(http.StatusOK)
}

// DELETE /parts/docs/{id}
func (h *Handler) HandleDocDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_ = h.Parts.DeleteDoc(r.Context(), int64(id))
	w.WriteHeader(http.StatusOK)
}

// -----------------------------------------------------------
// STOCK & BINS
// -----------------------------------------------------------

func (h *Handler) HandleBinOptions(w http.ResponseWriter, r *http.Request) {
	cid, _ := strconv.Atoi(r.URL.Query().Get("controller_id"))
	bins, err := h.Queries.GetBinsByController(r.Context(), sql.NullInt64{Int64: int64(cid), Valid: true})
	if err != nil {
		components.BinOptions([]db.Bin{}).Render(r.Context(), w)
		return
	}
	components.BinOptions(bins).Render(r.Context(), w)
}

func (h *Handler) HandlePartAssign(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	binID, _ := strconv.Atoi(r.FormValue("bin_id"))
	qty, _ := strconv.Atoi(r.FormValue("quantity"))

	if binID == 0 || qty <= 0 {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	err := h.Parts.AssignStock(r.Context(), parts.AssignStockRequest{
		PartID:   int64(partID),
		BinID:    int64(binID),
		Quantity: qty,
	})

	if err != nil {
		h.Logger.Error("failed to assign stock", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockMove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))
	targetBinID, _ := strconv.Atoi(r.FormValue("bin_id"))

	if targetBinID == 0 {
		http.Error(w, "Invalid target bin", http.StatusBadRequest)
		return
	}

	err := h.Parts.MoveStock(r.Context(), parts.MoveStockRequest{
		PartID:       int64(partID),
		AssignmentID: int64(assignmentID),
		TargetBinID:  int64(targetBinID),
	})

	if err != nil {
		h.Logger.Error("failed to move stock", "error", err)
		http.Error(w, "Move failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockRemove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))

	err := h.Parts.RemoveStock(r.Context(), parts.RemoveStockRequest{
		PartID:       int64(partID),
		AssignmentID: int64(assignmentID),
	})

	if err != nil {
		h.Logger.Error("failed to remove stock", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (h *Handler) HandlePartStockAdjust(w http.ResponseWriter, r *http.Request) {
	// partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))
	delta, _ := strconv.Atoi(r.URL.Query().Get("delta"))

	if delta == 0 {
		http.Error(w, "Invalid delta", http.StatusBadRequest)
		return
	}

	err := h.Parts.AdjustStock(r.Context(), int64(assignmentID), delta)
	if err != nil {
		h.Logger.Error("failed to adjust stock", "error", err)
		http.Error(w, "Adjustment failed", http.StatusInternalServerError)
		return
	}

	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))

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

// POST /parts/{id}/locate
func (h *Handler) HandlePartLocate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	// Fetch Settings (Needed for Color and Timeout config)
	settings, err := h.Queries.GetSettings(r.Context())
	if err != nil {
		// Fallback defaults if DB fails
		settings.ColorLocate.String = "#0000FF"
		settings.EnableLocateTimeout.Bool = false
		settings.LocateTimeoutSeconds.Int64 = 0
	}

	// Get all assignments
	assignments, err := h.Queries.GetPartAssignments(r.Context(), int64(id))
	if err != nil {
		h.Logger.Error("failed to get part assignments", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	foundAny := false
	for _, a := range assignments {
		if !a.ControllerIp.Valid || a.ControllerIp.String == "" || !a.LedIndex.Valid {
			continue
		}

		foundAny = true
		ledIndex := int(a.LedIndex.Int64)
		width := int(a.Width.Int64)
		if width < 1 {
			width = 1
		}

		// Trigger WLED
		err := h.WLED.LightUp(r.Context(), a.ControllerIp.String, ledIndex, width, settings.ColorLocate.String)
		if err != nil {
			h.Logger.Error("failed to locate bin", "error", err, "ip", a.ControllerIp.String)
		}

		// Handle Auto-Off Timer
		if settings.EnableLocateTimeout.Bool && settings.LocateTimeoutSeconds.Int64 > 0 {
			timeoutDuration := time.Duration(settings.LocateTimeoutSeconds.Int64) * time.Second

			go func(ip string, idx, count int, duration time.Duration) {
				time.Sleep(duration)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				_ = h.WLED.LightUp(ctx, ip, idx, count, "#000000")
			}(a.ControllerIp.String, ledIndex, width, timeoutDuration)
		}
	}

	if !foundAny {
		// No valid assignments found to locate, move on
	}

	w.WriteHeader(http.StatusOK)
}
