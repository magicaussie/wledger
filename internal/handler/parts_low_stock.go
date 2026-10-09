package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /parts/low-stock
func (h *Handler) HandleLowStock(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	parts, err := h.Parts.ListLowStock(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch low stock parts", http.StatusInternalServerError)
		return
	}

	pages.LowStock(user, parts).Render(r.Context(), w)
}

// GET /parts/low-stock/export
// Exports the reorder list as CSV using the same columns as the bulk import
// template, so the output is familiar and can be fed back into the importer.
func (h *Handler) HandleLowStockExport(w http.ResponseWriter, r *http.Request) {
	parts, err := h.Parts.ListLowStock(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch low stock parts", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment;filename=wledger_reorder_list.csv")

	cw := csv.NewWriter(w)
	defer cw.Flush()

	_ = cw.Write([]string{"Name", "Description", "Part Number", "Manufacturer", "Supplier", "Footprint", "Unit Cost", "Reorder Level", "Min Stock", "Barcode", "Quantity", "Tags", "Links", "Controller IP", "Segment ID", "LED Index"})
	for _, p := range parts {
		_ = cw.Write([]string{
			p.Name,
			p.Description.String,
			p.PartNumber.String,
			p.Manufacturer.String,
			p.Supplier.String,
			p.Footprint.String,
			fmt.Sprintf("%.2f", p.UnitCost.Float64),
			strconv.FormatInt(p.ReorderLevel, 10),
			strconv.FormatInt(p.MinStockThreshold, 10),
			p.BarcodeData.String,
			strconv.FormatInt(p.SuggestedOrder, 10),
			p.Tags.String,
			"", "", "", "",
		})
	}
}
