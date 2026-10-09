package handler

import (
	"context"
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
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/web/components"
)

// GET /parts/import/template
func (h *Handler) HandlePartsImportTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment;filename=wledger_import_template.csv")
	w.Write([]byte("Name,Description,Part Number,Manufacturer,Supplier,Footprint,Unit Cost,Reorder Level,Min Stock,Barcode,Quantity,Tags,Links,Controller IP,Segment ID,LED Index\n"))
	w.Write([]byte("Example Part,Resistor 10-K,MPN123,TI,DigiKey,0805,0.05,50,10,12345678,100,Resistor|SMD,https://example.com/resistor,192.168.1.100,0,5\n"))
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
		// Read the coordinate space once so every row is resolved against the same
		// consistent state.
		space, err := ledspace.Current(r.Context(), q)
		if err != nil {
			return fmt.Errorf("failed to read LED coordinate space: %w", err)
		}

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
				Footprint:         sql.NullString{String: row.Footprint, Valid: row.Footprint != ""},
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

			count++

			// Resolve Bin ID if location is provided
			var binID sql.NullInt64
			if row.ControllerIP != "" {
				resolvedBinID, err := resolveBinLocation(r.Context(), q, space, row.ControllerIP, int64(*row.SegmentID), int64(*row.LEDIndex))
				if err != nil {
					h.Logger.Error("failed to resolve bin location during import", "err", err, "row", row.RowNumber, "ip", row.ControllerIP, "seg", *row.SegmentID, "led", *row.LEDIndex)
					if err == sql.ErrNoRows {
						return fmt.Errorf("Row %d Error: Location not found (IP: %s, Seg: %d, LED: %d)", row.RowNumber, row.ControllerIP, *row.SegmentID, *row.LEDIndex)
					}
					return fmt.Errorf("Row %d Error resolving location: %v", row.RowNumber, err)
				}
				binID = sql.NullInt64{Int64: resolvedBinID, Valid: true}
			}

			// Insert Stock if Quantity > 0
			if row.InitialQuantity > 0 {
				err = q.CreatePartAssignment(r.Context(), db.CreatePartAssignmentParams{
					PartID:   partID,
					BinID:    binID,
					Quantity: int64(row.InitialQuantity),
				})
				if err != nil {
					h.Logger.Error("failed to create part assignment during import", "err", err, "row", row.RowNumber)
					return fmt.Errorf("Row %d Error saving stock: %v", row.RowNumber, err)
				}
			}
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

// resolveBinLocation resolves a CSV location (controller IP, segment, and a
// segment-relative LED index) to a bin ID in the active coordinate space.
//
// The CSV import contract is always segment-relative. In a segment-relative
// database the index is matched directly. In a drawer-relative database the
// segment-relative index is first located within the owning drawer's allocation
// and converted to a drawer-relative index before the lookup, so the CSV
// contract is preserved without exposing drawer-relative indices to the user.
func resolveBinLocation(ctx context.Context, q db.Querier, space, ip string, segmentID, ledIndex int64) (int64, error) {
	switch space {
	case ledspace.Segment:
		return q.GetBinByLocation(ctx, db.GetBinByLocationParams{
			IpAddress: ip,
			SegmentID: segmentID,
			LedIndex:  sql.NullInt64{Int64: ledIndex, Valid: true},
		})
	case ledspace.Drawer:
		ctrl, err := q.GetControllerByIP(ctx, ip)
		if err != nil {
			return 0, err
		}
		containers, err := q.GetContainersByController(ctx, ctrl.ID)
		if err != nil {
			return 0, err
		}
		for _, c := range containers {
			if c.SegmentID != segmentID || c.LedCount <= 0 {
				continue
			}
			if ledIndex < c.LedStart || ledIndex >= c.LedStart+c.LedCount {
				continue
			}
			return q.GetBinByContainerAndLed(ctx, db.GetBinByContainerAndLedParams{
				ContainerID: c.ID,
				LedIndex:    sql.NullInt64{Int64: ledIndex - c.LedStart, Valid: true},
			})
		}
		return 0, sql.ErrNoRows
	default:
		return 0, fmt.Errorf("LED coordinate space %q is unresolved", space)
	}
}
