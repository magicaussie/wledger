package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /parts
func (app *application) handlePartsList(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	search := r.URL.Query().Get("q")
	pageStr := r.URL.Query().Get("page")
	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}
	limit := 20
	offset := (page - 1) * limit

	var viewParts []pages.PartView
	var err error

	if search != "" {
		// FTS5 Search
		query := search + "*"
		// Use specific return variables to avoid shadowing 'err'
		rows, searchErr := app.queries.SearchParts(r.Context(), sql.NullString{String: query, Valid: true})
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
		// Use a specific return variables to avoid shadowing 'err'
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

	pages.PartsList(user, viewParts, search, page).Render(r.Context(), w)
}

// GET /parts/new
func (app *application) handlePartsNew(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	pages.PartCreate(user).Render(r.Context(), w)
}

// POST /parts
func (app *application) handlePartsCreate(w http.ResponseWriter, r *http.Request) {
	// parse Multipart Form (10MB limit)
	// TODO: test the size limit. Potentially add client side validation
	// to improve UX
	err := r.ParseMultipartForm(10 << 20)
	if err != nil {
		http.Error(w, "File too large", http.StatusBadRequest)
		return
	}

	// Handle Image Upload
	imagePath := ""
	file, header, err := r.FormFile("image")
	if err == nil {
		defer file.Close()
		savedName, err := images.ProcessUpload(file, header)
		if err != nil {
			app.logger.Error("image processing failed", "error", err)
			http.Error(w, "Invalid image", http.StatusBadRequest)
			return
		}
		imagePath = savedName
	}

	// Extract Fields
	name := r.FormValue("name")
	desc := r.FormValue("description")
	partNum := r.FormValue("part_number")

	cost, _ := strconv.ParseFloat(r.FormValue("unit_cost"), 64)
	reorder, _ := strconv.Atoi(r.FormValue("reorder_level"))
	minStock, _ := strconv.Atoi(r.FormValue("min_stock"))

	// Save to DB
	newID, err := app.queries.CreatePart(r.Context(), db.CreatePartParams{
		Name:              name,
		Description:       sql.NullString{String: desc, Valid: desc != ""},
		PartNumber:        sql.NullString{String: partNum, Valid: partNum != ""},
		UnitCost:          sql.NullFloat64{Float64: cost, Valid: true},
		ReorderLevel:      sql.NullInt64{Int64: int64(reorder), Valid: true},
		MinStockThreshold: sql.NullInt64{Int64: int64(minStock), Valid: true},
		ImagePath:         sql.NullString{String: imagePath, Valid: imagePath != ""},
	})

	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "Part already exists", http.StatusConflict)
			return
		}
		app.logger.Error("failed to create part", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Audit Log
	auditPayload := map[string]any{
		"name": name,
		"id":   newID,
	}
	audit.Log(r.Context(), app.queries, "CREATE", "PART", newID, "Created part "+name, nil, auditPayload)

	// Redirect to Inventory
	http.Redirect(w, r, "/parts", http.StatusSeeOther)
}
