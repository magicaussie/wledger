package main

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /parts/{id}/edit
func (app *application) handlePartEdit(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	p, err := app.queries.GetPart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}

	pages.PartEdit(user, p).Render(r.Context(), w)
}

// POST /parts/{id}/edit
func (app *application) handlePartUpdate(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	// get existing data and keep old image if needed
	oldPart, err := app.queries.GetPart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}

	// Parse Form
	r.ParseMultipartForm(10 << 20)

	// Handle Image Logic
	newImagePath := oldPart.ImagePath.String
	file, header, err := r.FormFile("image")
	if err == nil {
		// New file uploaded
		defer file.Close()
		savedName, err := images.ProcessUpload(file, header)
		if err == nil {
			newImagePath = savedName
			// Delete old image to save space
			images.Delete(oldPart.ImagePath.String)
		}
	}

	// Extract Fields
	name := r.FormValue("name")
	desc := r.FormValue("description")
	partNum := r.FormValue("part_number")
	barcode := r.FormValue("barcode_data")

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	// Update DB
	err = app.queries.UpdatePart(r.Context(), db.UpdatePartParams{
		Name:              name,
		Description:       sql.NullString{String: desc, Valid: desc != ""},
		PartNumber:        sql.NullString{String: partNum, Valid: partNum != ""},
		Manufacturer:      oldPart.Manufacturer, // Keep existing for now as UI didn't expose it
		Supplier:          oldPart.Supplier,     // Keep existing
		UnitCost:          sql.NullFloat64{Float64: cost, Valid: true},
		ReorderLevel:      sql.NullInt64{Int64: int64(reorder), Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: int64(minStock), Valid: true},
		BarcodeData:       sql.NullString{String: barcode, Valid: barcode != ""},
		ImagePath:         sql.NullString{String: newImagePath, Valid: newImagePath != ""},
		ID:                int64(id),
	})

	if err != nil {
		app.logger.Error("failed to update part", "error", err)
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
	}

	// Audit
	audit.Log(r.Context(), app.queries, "UPDATE", "PART", int64(id), "Updated part details", nil, nil)

	http.Redirect(w, r, "/parts/"+idStr, http.StatusSeeOther)
}

// POST /parts/{id}/delete
func (app *application) handlePartDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	// Audit before delete
	audit.Log(r.Context(), app.queries, "DELETE", "PART", int64(id), "Deleted part", nil, nil)

	err := app.queries.DeletePart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Delete failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/parts", http.StatusSeeOther)
}
