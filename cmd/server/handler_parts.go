package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

// --- LIST & DETAIL ---

// GET /parts
func (app *application) handlePartsList(w http.ResponseWriter, r *http.Request) {
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
		rows, searchErr := app.queries.SearchParts(r.Context(), db.SearchPartsParams{
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
		rows, listErr := app.queries.ListParts(r.Context(), db.ListPartsParams{
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
		app.logger.Error("failed to fetch parts", "error", err)
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
func (app *application) handlePartDetail(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	p, err := app.queries.GetPart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}

	stock, _ := app.queries.GetPartAssignments(r.Context(), int64(id))
	links, _ := app.queries.GetPartLinks(r.Context(), int64(id))
	docs, _ := app.queries.GetPartDocs(r.Context(), int64(id))
	controllers, _ := app.queries.GetControllers(r.Context())

	pages.PartDetail(user, p, stock, links, docs, controllers).Render(r.Context(), w)
}

// -----------------------------------------------------------
// CREATE & UPDATE
// -----------------------------------------------------------

// GET /parts/new
func (app *application) handlePartsNew(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	pages.PartCreate(user).Render(r.Context(), w)
}

// POST /parts
func (app *application) handlePartsCreate(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(20 << 20) // 20MB limit
	if err != nil {
		http.Error(w, "Request too large", http.StatusBadRequest)
		return
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
	}

	// Handle Image Upload
	file, header, err := r.FormFile("image")
	if err == nil {
		req.Image = &parts.DocUpload{
			File:   file,
			Header: header,
		}
		defer file.Close()
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
			defer f.Close()
		}
	}

	newID, err := app.parts.CreatePart(r.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "Part already exists (check barcode)", http.StatusConflict)
		} else {
			app.logger.Error("failed to create part", "error", err)
			http.Error(w, "Database error", http.StatusInternalServerError)
		}
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", newID), http.StatusSeeOther)
}

// GET /parts/{id}/edit
func (app *application) handlePartEdit(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	p, err := app.queries.GetPart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}

	links, _ := app.queries.GetPartLinks(r.Context(), int64(id))
	docs, _ := app.queries.GetPartDocs(r.Context(), int64(id))

	pages.PartEdit(user, p, links, docs).Render(r.Context(), w)
}

// POST /parts/{id}/update
func (app *application) handlePartUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	err := r.ParseMultipartForm(20 << 20)
	if err != nil {
		http.Error(w, "Request too large", http.StatusBadRequest)
		return
	}

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

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
	}

	// Handle Image Upload
	file, header, err := r.FormFile("image")
	if err == nil {
		req.Image = &parts.DocUpload{
			File:   file,
			Header: header,
		}
		defer file.Close()
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
			defer f.Close()
		}
	}

	err = app.parts.UpdatePart(r.Context(), req)
	if err != nil {
		app.logger.Error("failed to update part", "error", err)
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", id), http.StatusSeeOther)
}

// POST /parts/{id}/delete
func (app *application) handlePartDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	partID := int64(id)

	err := app.parts.DeletePart(r.Context(), partID)
	if err != nil {
		app.logger.Error("failed to delete part", "error", err)
		http.Error(w, "Delete failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/parts", http.StatusSeeOther)
}

// -----------------------------------------------------------
// SUB-RESOURCES (HTMX)
// -----------------------------------------------------------

// DELETE /parts/links/{id}
func (app *application) handleLinkDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_ = app.parts.DeleteLink(r.Context(), int64(id))
	w.WriteHeader(http.StatusOK)
}

// DELETE /parts/docs/{id}
func (app *application) handleDocDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_ = app.parts.DeleteDoc(r.Context(), int64(id))
	w.WriteHeader(http.StatusOK)
}

// -----------------------------------------------------------
// STOCK & BINS
// -----------------------------------------------------------

func (app *application) handleBinOptions(w http.ResponseWriter, r *http.Request) {
	cid, _ := strconv.Atoi(r.URL.Query().Get("controller_id"))
	bins, err := app.queries.GetBinsByController(r.Context(), sql.NullInt64{Int64: int64(cid), Valid: true})
	if err != nil {
		components.BinOptions([]db.Bin{}).Render(r.Context(), w)
		return
	}
	components.BinOptions(bins).Render(r.Context(), w)
}

func (app *application) handlePartAssign(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	binID, _ := strconv.Atoi(r.FormValue("bin_id"))
	qty, _ := strconv.Atoi(r.FormValue("quantity"))

	if binID == 0 || qty <= 0 {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	err := app.parts.AssignStock(r.Context(), parts.AssignStockRequest{
		PartID:   int64(partID),
		BinID:    int64(binID),
		Quantity: qty,
	})

	if err != nil {
		app.logger.Error("failed to assign stock", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (app *application) handlePartStockMove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))
	targetBinID, _ := strconv.Atoi(r.FormValue("bin_id"))

	if targetBinID == 0 {
		http.Error(w, "Invalid target bin", http.StatusBadRequest)
		return
	}

	err := app.parts.MoveStock(r.Context(), parts.MoveStockRequest{
		PartID:       int64(partID),
		AssignmentID: int64(assignmentID),
		TargetBinID:  int64(targetBinID),
	})

	if err != nil {
		app.logger.Error("failed to move stock", "error", err)
		http.Error(w, "Move failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (app *application) handlePartStockRemove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))

	err := app.parts.RemoveStock(r.Context(), parts.RemoveStockRequest{
		PartID:       int64(partID),
		AssignmentID: int64(assignmentID),
	})

	if err != nil {
		app.logger.Error("failed to remove stock", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}