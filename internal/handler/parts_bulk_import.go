package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/importer"
	"github.com/tuxedocurly/wledger/web/components"
)

// GET /parts/import/template
func (h *Handler) HandlePartsImportTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment;filename=wledger_import_template.csv")
	w.Write([]byte("Name,Description,Part Number,Manufacturer,Supplier,Unit Cost,Reorder Level,Min Stock,Barcode,Quantity\n"))
	w.Write([]byte("Example Part,10k Resistor,R-10k,Vishay,DigiKey,0.05,50,10,12345678,100\n"))
}

// POST /parts/import
func (h *Handler) HandlePartsImport(w http.ResponseWriter, r *http.Request) {
	// Authorization
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.Logger.Warn("unauthorized parts import attempt", "user_id", user.ID, "ip", r.RemoteAddr)
		http.Error(w, "Unauthorized", http.StatusForbidden)
		return
	}

	// Parse Form
	err := r.ParseMultipartForm(config.MaxUploadSizeImport) // 100 MB
	if err != nil {
		h.Logger.Error("failed to parse multipart form for parts import", "err", err)
		components.ImportResult(false, "Failed to parse form: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	defer r.MultipartForm.RemoveAll()

	// Get Input (File takes precedence over text)
	var rows []importer.PartImportRow

	file, _, err := r.FormFile("file")
	if err == nil {
		defer file.Close()
		rows, err = importer.ParsePartsCSV(file)
	} else {
		// Try raw text
		raw := r.FormValue("raw_text")
		if strings.TrimSpace(raw) == "" {
			components.ImportResult(false, "No data provided. Upload a file or paste text.", nil).Render(r.Context(), w)
			return
		}
		rows, err = importer.ParsePartsCSV(strings.NewReader(raw))
	}

	if err != nil {
		components.ImportResult(false, "Parsing Error: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	if len(rows) == 0 {
		components.ImportResult(false, "No valid rows found in input.", nil).Render(r.Context(), w)
		return
	}

	// Database Transaction
	tx, err := h.Database.Begin()
	if err != nil {
		h.Logger.Error("failed to start transaction for parts import", "err", err)
		components.ImportResult(false, "Database error: "+err.Error(), nil).Render(r.Context(), w)
		return
	}
	defer tx.Rollback()
	qtx := h.Queries.WithTx(tx)

	count := 0
	for _, row := range rows {
		// Insert Part
		partID, err := qtx.CreatePart(r.Context(), db.CreatePartParams{
			Name:              row.Name,
			Description:       sql.NullString{String: row.Description, Valid: row.Description != ""},
			PartNumber:        sql.NullString{String: row.PartNumber, Valid: row.PartNumber != ""},
			Manufacturer:      sql.NullString{String: row.Manufacturer, Valid: row.Manufacturer != ""},
			Supplier:          sql.NullString{String: row.Supplier, Valid: row.Supplier != ""},
			UnitCost:          sql.NullFloat64{Float64: row.UnitCost, Valid: true},
			ReorderLevel:      sql.NullInt64{Int64: int64(row.ReorderLevel), Valid: true},
			MinStockThreshold: sql.NullInt64{Int64: int64(row.MinStockThreshold), Valid: true},
			BarcodeData:       sql.NullString{String: row.BarcodeData, Valid: row.BarcodeData != ""},
		})

		if err != nil {
			h.Logger.Error("failed to create part during import", "err", err, "row", row.RowNumber)
			returnErr := fmt.Sprintf("Row %d Error: %v", row.RowNumber, err)
			if strings.Contains(err.Error(), "UNIQUE constraint") {
				returnErr = fmt.Sprintf("Row %d Error: Duplicate Barcode '%s'", row.RowNumber, row.BarcodeData)
			}
			components.ImportResult(false, returnErr, nil).Render(r.Context(), w)
			return
		}

		// Insert Orphaned Stock if Quantity > 0
		if row.InitialQuantity > 0 {
			err = qtx.CreatePartAssignment(r.Context(), db.CreatePartAssignmentParams{
				PartID:   partID,
				BinID:    sql.NullInt64{Valid: false}, // Orphaned
				Quantity: int64(row.InitialQuantity),
			})
			if err != nil {
				h.Logger.Error("failed to create part assignment during import", "err", err, "row", row.RowNumber)
				components.ImportResult(false, fmt.Sprintf("Row %d Error saving stock: %v", row.RowNumber, err), nil).Render(r.Context(), w)
				return
			}
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		h.Logger.Error("failed to commit transaction for parts import", "err", err)
		components.ImportResult(false, "Commit failed: "+err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Audit Log
	audit.Log(r.Context(), h.Queries, "IMPORT", "PARTS", 0, fmt.Sprintf("Bulk imported %d parts", count), nil, nil)

	// Success Response
	msg := fmt.Sprintf("Successfully imported %d parts.", count)
	components.ImportResult(true, msg, nil).Render(r.Context(), w)
}
