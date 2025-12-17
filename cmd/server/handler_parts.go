package main

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/images"
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

	// Process Main Fields
	name := r.FormValue("name")
	desc := r.FormValue("description")
	partNum := r.FormValue("part_number")
	manufacturer := r.FormValue("manufacturer")
	supplier := r.FormValue("supplier")
	barcode := r.FormValue("barcode_data")

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	// Handle Image Upload
	imagePath := ""
	file, header, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		fileName, err := images.ProcessUpload(file, header)
		if err == nil {
			// Prepend the web access path
			imagePath = "/uploads/images/" + fileName
		}
	}

	// Create Part
	newID, err := app.queries.CreatePart(r.Context(), db.CreatePartParams{
		Name:              name,
		Description:       sql.NullString{String: desc, Valid: desc != ""},
		PartNumber:        sql.NullString{String: partNum, Valid: partNum != ""},
		Manufacturer:      sql.NullString{String: manufacturer, Valid: manufacturer != ""},
		Supplier:          sql.NullString{String: supplier, Valid: supplier != ""},
		BarcodeData:       sql.NullString{String: barcode, Valid: barcode != ""},
		UnitCost:          sql.NullFloat64{Float64: cost, Valid: true},
		ReorderLevel:      sql.NullInt64{Int64: int64(reorder), Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: int64(minStock), Valid: true},
		ImagePath:         sql.NullString{String: imagePath, Valid: imagePath != ""},
	})

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "Part already exists (check barcode)", http.StatusConflict)
			return
		}
		app.logger.Error("failed to create part", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Process Links
	labels := r.PostForm["link_labels[]"]
	urls := r.PostForm["link_urls[]"]
	if len(labels) == len(urls) {
		for i, u := range urls {
			if u == "" {
				continue
			}
			_ = app.queries.CreatePartLink(r.Context(), db.CreatePartLinkParams{
				PartID: newID,
				Url:    u,
				Label:  sql.NullString{String: labels[i], Valid: labels[i] != ""},
			})
		}
	}

	// Process Documents
	docs := r.MultipartForm.File["documents"]
	for _, fh := range docs {
		f, err := fh.Open()
		if err != nil {
			continue
		}
		defer f.Close()

		savedWebPath, err := saveDocument(f, fh.Filename)
		if err == nil {
			_ = app.queries.CreatePartDoc(r.Context(), db.CreatePartDocParams{
				PartID:   newID,
				FilePath: savedWebPath,
				FileName: fh.Filename,
			})
		}
	}

	audit.Log(r.Context(), app.queries, "CREATE", "PART", newID, "Created part "+name, nil, nil)
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

	// Fetch old part to manage existing files
	oldPart, err := app.queries.GetPart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}

	r.ParseMultipartForm(20 << 20)

	// Image Update Logic
	newImagePath := oldPart.ImagePath.String
	file, header, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		fileName, err := images.ProcessUpload(file, header)
		if err == nil {
			// Set new path
			newImagePath = "/uploads/images/" + fileName

			// Clean up old image if it exists
			if oldPart.ImagePath.Valid {
				images.DeleteByWebPath(oldPart.ImagePath.String)
			}
		}
	}

	name := r.FormValue("name")
	desc := r.FormValue("description")
	partNum := r.FormValue("part_number")
	manufacturer := r.FormValue("manufacturer")
	supplier := r.FormValue("supplier")
	barcode := r.FormValue("barcode_data")
	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	err = app.queries.UpdatePart(r.Context(), db.UpdatePartParams{
		Name:              name,
		Description:       sql.NullString{String: desc, Valid: desc != ""},
		PartNumber:        sql.NullString{String: partNum, Valid: partNum != ""},
		Manufacturer:      sql.NullString{String: manufacturer, Valid: manufacturer != ""},
		Supplier:          sql.NullString{String: supplier, Valid: supplier != ""},
		BarcodeData:       sql.NullString{String: barcode, Valid: barcode != ""},
		UnitCost:          sql.NullFloat64{Float64: cost, Valid: true},
		ReorderLevel:      sql.NullInt64{Int64: int64(reorder), Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: int64(minStock), Valid: true},
		ImagePath:         sql.NullString{String: newImagePath, Valid: newImagePath != ""},
		ID:                int64(id),
	})

	if err != nil {
		app.logger.Error("failed to update part", "error", err)
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
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
			_ = app.queries.UpdatePartLink(r.Context(), db.UpdatePartLinkParams{
				Url:   existingUrls[i],
				Label: sql.NullString{String: existingLabels[i], Valid: existingLabels[i] != ""},
				ID:    int64(linkID),
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
			_ = app.queries.CreatePartLink(r.Context(), db.CreatePartLinkParams{
				PartID: int64(id),
				Url:    u,
				Label:  sql.NullString{String: labels[i], Valid: labels[i] != ""},
			})
		}
	}

	// Add Documents
	docs := r.MultipartForm.File["documents"]
	for _, fh := range docs {
		f, err := fh.Open()
		if err != nil {
			continue
		}
		defer f.Close()

		savedWebPath, err := saveDocument(f, fh.Filename)
		if err == nil {
			_ = app.queries.CreatePartDoc(r.Context(), db.CreatePartDocParams{
				PartID:   int64(id),
				FilePath: savedWebPath,
				FileName: fh.Filename,
			})
		}
	}

	audit.Log(r.Context(), app.queries, "UPDATE", "PART", int64(id), "Updated details", nil, nil)
	http.Redirect(w, r, fmt.Sprintf("/parts/%d", id), http.StatusSeeOther)
}

// POST /parts/{id}/delete
func (app *application) handlePartDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	partID := int64(id)

	// Fetch Part to clean up the Main Image
	p, err := app.queries.GetPart(r.Context(), partID)
	if err == nil && p.ImagePath.Valid {
		images.DeleteByWebPath(p.ImagePath.String)
	}

	// Fetch Docs to clean up Document Files
	docs, err := app.queries.GetPartDocs(r.Context(), partID)
	if err == nil {
		for _, doc := range docs {
			// doc.FilePath is like "/uploads/docs/datasheet.pdf"
			// convert it to the system path: "./app/uploads/docs/datasheet.pdf"
			if strings.HasPrefix(doc.FilePath, "/uploads/") {
				// "path/filepath" handles OS separators (slash vs backslash) automatically
				relativePath := filepath.Join("app", doc.FilePath)

				// remove the file
				removeErr := os.Remove(relativePath)
				if removeErr != nil {
					// log this but don't stop the deletion process
					app.logger.Warn("failed to delete orphaned document file", "path", relativePath, "error", removeErr)
				}
			}
		}
	}

	// Log and Delete from DB
	// The DB "ON DELETE CASCADE" will handle removing the rows from 'part_docs' and 'part_links'
	audit.Log(r.Context(), app.queries, "DELETE", "PART", partID, "Deleted part", nil, nil)
	_ = app.queries.DeletePart(r.Context(), partID)

	http.Redirect(w, r, "/parts", http.StatusSeeOther)
}

// -----------------------------------------------------------
// SUB-RESOURCES (HTMX)
// -----------------------------------------------------------

// DELETE /parts/links/{id}
func (app *application) handleLinkDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	_ = app.queries.DeletePartLink(r.Context(), int64(id))
	w.WriteHeader(http.StatusOK)
}

// DELETE /parts/docs/{id}
func (app *application) handleDocDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))

	// Fetch info to get filepath
	doc, err := app.queries.GetPartDoc(r.Context(), int64(id))
	if err == nil {
		// Delete actual file from disk
		// doc.FilePath is like "/uploads/docs/foo.pdf"
		// convert to "./app/uploads/docs/foo.pdf"
		relativePath := "." + strings.Replace(doc.FilePath, "/uploads", "/app/uploads", 1)

		err := os.Remove(relativePath)
		if err != nil {
			app.logger.Warn("failed to delete doc file", "path", relativePath, "error", err)
		}
	}

	// Delete DB Row
	_ = app.queries.DeletePartDoc(r.Context(), int64(id))
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

	nullBinID := sql.NullInt64{Int64: int64(binID), Valid: true}

	_, err := app.queries.GetAssignmentID(r.Context(), db.GetAssignmentIDParams{
		PartID: int64(partID),
		BinID:  nullBinID,
	})

	if err == nil {
		err = app.queries.UpdatePartAssignmentQuantity(r.Context(), db.UpdatePartAssignmentQuantityParams{
			Quantity: int64(qty),
			PartID:   int64(partID),
			BinID:    nullBinID,
		})
	} else {
		err = app.queries.CreatePartAssignment(r.Context(), db.CreatePartAssignmentParams{
			PartID:   int64(partID),
			BinID:    nullBinID,
			Quantity: int64(qty),
		})
	}

	if err != nil {
		app.logger.Error("failed to assign stock", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), app.queries, "STOCK_ADD", "PART", int64(partID), "Added stock", nil, nil)
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

	ctx := r.Context()

	// Get Source Assignment
	source, err := app.queries.GetAssignment(ctx, int64(assignmentID))
	if err != nil {
		http.Error(w, "Source assignment not found", http.StatusNotFound)
		return
	}

	// Check if Target exists
	targetID, err := app.queries.GetAssignmentID(ctx, db.GetAssignmentIDParams{
		PartID: int64(partID),
		BinID:  sql.NullInt64{Int64: int64(targetBinID), Valid: true},
	})

	if err == nil {
		// Merge Path
		// Target Exists. Add Source Qty to Target Qty.
		target, _ := app.queries.GetAssignment(ctx, targetID) // Fetch current qty
		newQty := target.Quantity + source.Quantity

		// Update Target
		err = app.queries.UpdatePartAssignmentQuantity(ctx, db.UpdatePartAssignmentQuantityParams{
			Quantity: newQty,
			PartID:   int64(partID),
			BinID:    sql.NullInt64{Int64: int64(targetBinID), Valid: true},
		})
		if err != nil {
			app.logger.Error("failed to update target stock", "error", err)
			http.Error(w, "Merge failed", http.StatusInternalServerError)
			return
		}

		// Delete Source
		err = app.queries.DeleteAssignment(ctx, int64(assignmentID))
		if err != nil {
			app.logger.Error("failed to delete source stock after merge", "error", err)
		}

		audit.Log(ctx, app.queries, "STOCK_MERGE", "PART", int64(partID), fmt.Sprintf("Merged stock into bin %d", targetBinID), nil, nil)

	} else {
		// Move Path
		// Target does not exist. Just update the Bin ID.
		err = app.queries.ReassignPartAssignment(ctx, db.ReassignPartAssignmentParams{
			BinID: sql.NullInt64{Int64: int64(targetBinID), Valid: true},
			ID:    int64(assignmentID),
		})
		if err != nil {
			app.logger.Error("failed to move stock", "error", err)
			http.Error(w, "Move failed", http.StatusInternalServerError)
			return
		}

		audit.Log(ctx, app.queries, "STOCK_MOVE", "PART", int64(partID), fmt.Sprintf("Moved stock to bin %d", targetBinID), nil, nil)
	}

	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

func (app *application) handlePartStockRemove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	assignmentID, _ := strconv.Atoi(chi.URLParam(r, "assignment_id"))

	err := app.queries.DeleteAssignment(r.Context(), int64(assignmentID))

	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), app.queries, "STOCK_REMOVE", "PART", int64(partID), "Removed stock", nil, nil)
	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}

// -----------------------------------------------------------
// HELPERS
// -----------------------------------------------------------

func saveDocument(src io.Reader, filename string) (string, error) {
	// UPDATED: Save to ./app/uploads/docs
	uploadDir := "./app/uploads/docs"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		return "", err
	}

	// Generate unique name
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	// Sanitize filename roughly to avoid filesystem issues
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, base)

	cleanName := fmt.Sprintf("%s_%d%s", base, time.Now().Unix(), ext)
	destPath := filepath.Join(uploadDir, cleanName)

	dst, err := os.Create(destPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}

	// Return web-accessible path
	return "/uploads/docs/" + cleanName, nil
}
