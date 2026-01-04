package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
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
	w.Write([]byte("Name,Description,Part Number,Manufacturer,Supplier,Unit Cost,Reorder Level,Min Stock,Barcode,Quantity,Tags,Links\n"))
	w.Write([]byte("Example Part,10k Resistor,R-10k,Vishay,DigiKey,0.05,50,10,12345678,100,Resistor|SMD,https://example.com/resistor\n"))
}

// POST /parts/import
func (h *Handler) HandlePartsImport(w http.ResponseWriter, r *http.Request) {
	// Authorization
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
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

	count := 0
	err = h.Queries.ExecTx(r.Context(), func(q db.Querier) error {
		for _, row := range rows {
			// Insert Part
			partID, err := q.CreatePart(r.Context(), db.CreatePartParams{
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
				returnErr := fmt.Errorf("Row %d Error: %v", row.RowNumber, err)
				if strings.Contains(err.Error(), "UNIQUE constraint") {
					returnErr = fmt.Errorf("Row %d Error: Duplicate Barcode '%s'", row.RowNumber, row.BarcodeData)
				}
				return returnErr
			}

			// Sync Tags
			if len(row.Tags) > 0 {
				if err := h.Tags.SyncTags(r.Context(), q, partID, row.Tags); err != nil {
					h.Logger.Error("failed to sync tags during import", "err", err, "row", row.RowNumber)
					return fmt.Errorf("Row %d Error saving tags: %v", row.RowNumber, err)
				}
			}

			// Add Links
			for _, linkURL := range row.Links {
				label := ""
				if u, err := url.Parse(linkURL); err == nil {
					label = u.Hostname()
				}
				if err := h.Documents.AddLink(r.Context(), q, partID, linkURL, label); err != nil {
					h.Logger.Error("failed to add link during import", "err", err, "row", row.RowNumber, "url", linkURL)
					return fmt.Errorf("Row %d Error saving link %s: %v", row.RowNumber, linkURL, err)
				}
			}

			// Insert Orphaned Stock if Quantity > 0
			if row.InitialQuantity > 0 {
				err = q.CreatePartAssignment(r.Context(), db.CreatePartAssignmentParams{
					PartID:   partID,
					BinID:    sql.NullInt64{Valid: false}, // Orphaned
					Quantity: int64(row.InitialQuantity),
				})
				if err != nil {
					h.Logger.Error("failed to create part assignment during import", "err", err, "row", row.RowNumber)
					return fmt.Errorf("Row %d Error saving stock: %v", row.RowNumber, err)
				}
			}
			count++
		}

		// Audit Log
		audit.Log(r.Context(), q, "IMPORT", "PARTS", 0, fmt.Sprintf("Bulk imported %d parts", count), nil, nil)
		return nil
	})

	if err != nil {
		components.ImportResult(false, err.Error(), nil).Render(r.Context(), w)
		return
	}

	// Success Response
	msg := fmt.Sprintf("Successfully imported %d parts.", count)
	components.ImportResult(true, msg, nil).Render(r.Context(), w)
}
