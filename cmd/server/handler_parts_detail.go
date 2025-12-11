package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /parts/{id}
func (app *application) handlePartDetail(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	// Get Part
	p, err := app.queries.GetPart(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Part not found", http.StatusNotFound)
		return
	}

	// Get Stock Locations
	stock, err := app.queries.GetPartAssignments(r.Context(), int64(id))
	if err != nil {
		// Log but don't fail, just show empty
		app.logger.Error("failed to get stock", "error", err)
		stock = []db.GetPartAssignmentsRow{}
	}

	// Get Controllers (for the Add form)
	controllers, _ := app.queries.GetControllers(r.Context())

	pages.PartDetail(p, stock, controllers).Render(r.Context(), w)
}

// GET /parts/bins_options?controller_id=X
func (app *application) handleBinOptions(w http.ResponseWriter, r *http.Request) {
	cidStr := r.URL.Query().Get("controller_id")
	cid, _ := strconv.Atoi(cidStr)

	bins, err := app.queries.GetBinsByController(r.Context(), sql.NullInt64{Int64: int64(cid), Valid: true})
	if err != nil {
		// Return empty options
		pages.BinOptions([]db.Bin{}).Render(r.Context(), w)
		return
	}

	pages.BinOptions(bins).Render(r.Context(), w)
}

// POST /parts/{id}/assign
func (app *application) handlePartAssign(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	partID, _ := strconv.Atoi(idStr)

	binID, _ := strconv.Atoi(r.FormValue("bin_id"))
	qty, _ := strconv.Atoi(r.FormValue("quantity"))

	if binID == 0 || qty <= 0 {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	// Check if assignment exists
	// ignore the error here; if it fails, assume the row doesn't exist (sql.ErrNoRows)
	_, err := app.queries.GetAssignmentID(r.Context(), db.GetAssignmentIDParams{
		PartID: int64(partID),
		BinID:  int64(binID),
	})

	if err == nil {
		// 2Update Existing
		err = app.queries.UpdatePartAssignmentQuantity(r.Context(), db.UpdatePartAssignmentQuantityParams{
			Quantity: int64(qty),
			PartID:   int64(partID),
			BinID:    int64(binID),
		})
	} else {
		// Create New
		err = app.queries.CreatePartAssignment(r.Context(), db.CreatePartAssignmentParams{
			PartID:   int64(partID),
			BinID:    int64(binID),
			Quantity: int64(qty),
		})
	}

	if err != nil {
		app.logger.Error("failed to assign stock", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), app.queries, "STOCK_ADD", "PART", int64(partID), "Added stock", nil, map[string]int{"qty": qty, "bin": binID})

	http.Redirect(w, r, "/parts/"+idStr, http.StatusSeeOther)
}

// POST /parts/{id}/stock/{bin_id}/delete
func (app *application) handlePartStockRemove(w http.ResponseWriter, r *http.Request) {
	partID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	binID, _ := strconv.Atoi(chi.URLParam(r, "bin_id"))

	err := app.queries.DeletePartAssignment(r.Context(), db.DeletePartAssignmentParams{
		PartID: int64(partID),
		BinID:  int64(binID),
	})

	if err != nil {
		app.logger.Error("failed to remove stock assignment", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Audit Log
	audit.Log(r.Context(), app.queries, "STOCK_REMOVE", "PART", int64(partID), "Removed bin assignment", nil, map[string]int{"bin": binID})

	// Redirect to refresh total stock counts
	http.Redirect(w, r, fmt.Sprintf("/parts/%d", partID), http.StatusSeeOther)
}
