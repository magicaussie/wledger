package handler

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/categories"
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
	writer := csv.NewWriter(w)
	header := []string{"Name", "Description", "Part Number", "Manufacturer", "Supplier", "Footprint", "Unit Cost", "Reorder Level", "Min Stock", "Barcode", "Quantity", "Tags", "Links", "Controller IP", "Segment ID", "LED Index", "SKU", "Brand", "Model", "Colour", "Material", "Finish", "Condition", "Length", "Width", "Height", "Depth", "Dimension Unit", "Weight", "Weight Unit", "Notes", "Purchased From", "Supplier Product Code", "Purchase Date", "Purchase Price", "Currency", "Product URL", "Purchase Notes", "Category", "Category Specifications"}
	row := make([]string, len(header))
	row[0], row[1], row[2], row[3], row[4], row[5] = "Example Part", "Resistor 10-K", "MPN123", "TI", "DigiKey", "0805"
	row[6], row[7], row[8], row[9], row[10] = "0.05", "50", "10", "12345678", "100"
	row[11], row[12], row[27], row[29], row[39] = "Resistor|SMD", "https://example.com/resistor", "mm", "g", "{}"
	_ = writer.Write(header)
	_ = writer.Write(row)
	writer.Flush()
}

// POST /parts/import
func (h *Handler) HandlePartsImport(w http.ResponseWriter, r *http.Request) {
	// Authorization
	user := auth.GetUserFromRequest(r)
	if !user.CanWrite() {
		h.UIError.Respond(w, r, nil, "Unauthorized", http.StatusForbidden)
		return
	}

	// ParseMultipartForm's maxMemory is only a memory threshold; this applies a
	// hard limit to the complete multipart request body.
	if !limitRequestBody(w, r, config.MaxUploadSizeImport) {
		return
	}
	err := r.ParseMultipartForm(config.MaxUploadSizeImport)
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
	categoryRows, err := resolveImportedCategories(r.Context(), h.Database, rows)
	if err != nil {
		components.ImportResult(false, err.Error(), nil).Render(r.Context(), w)
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

		for rowIndex, row := range rows {
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

			categoryData := categoryRows[rowIndex]
			if categoryData.ID > 0 {
				if err := q.CreatePartCategory(r.Context(), db.CreatePartCategoryParams{PartID: partID, CategoryID: categoryData.ID}); err != nil {
					return fmt.Errorf("Row %d Error saving category assignment: %w", row.RowNumber, err)
				}
				for _, spec := range categoryData.Specifications {
					value := db.CreatePartAttributeValueParams{PartID: partID, AttributeID: spec.AttributeID}
					if spec.Text != nil {
						value.ValueText = sql.NullString{String: *spec.Text, Valid: true}
					}
					if spec.Number != nil {
						value.ValueNumber = sql.NullFloat64{Float64: *spec.Number, Valid: true}
					}
					if spec.Boolean != nil {
						value.ValueBoolean = sql.NullBool{Bool: *spec.Boolean, Valid: true}
					}
					if err := q.CreatePartAttributeValue(r.Context(), value); err != nil {
						return fmt.Errorf("Row %d Error saving category specification: %w", row.RowNumber, err)
					}
				}
			}

			if !row.Details.IsEmpty() {
				details := row.Details
				details.PartID = partID
				if err := details.NormalizeAndValidate(); err != nil {
					return fmt.Errorf("Row %d Error: invalid product details: %w", row.RowNumber, err)
				}
				if err := q.CreatePartDetails(r.Context(), db.CreatePartDetailsParams{
					PartID: partID, Sku: sql.NullString{String: details.SKU, Valid: details.SKU != ""}, Brand: details.Brand, Model: details.Model,
					Colour: details.Colour, Material: details.Material, Finish: details.Finish, Condition: details.Condition, Notes: details.Notes,
					LengthMm: details.LengthMM, WidthMm: details.WidthMM, HeightMm: details.HeightMM, DepthMm: details.DepthMM,
					DimensionUnit: details.DimensionUnit, WeightG: details.WeightG, WeightUnit: details.WeightUnit,
					PurchasedFrom: details.PurchasedFrom, SupplierProductCode: details.SupplierProductCode, PurchaseDate: details.PurchaseDate,
					PurchasePrice: details.PurchasePrice, Currency: details.Currency, ProductUrl: details.ProductURL, PurchaseNotes: details.PurchaseNotes,
				}); err != nil {
					return fmt.Errorf("Row %d Error saving extended product details: %w", row.RowNumber, err)
				}
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

type importedCategory struct {
	ID             int64
	Specifications []categories.Specification
}

func resolveImportedCategories(ctx context.Context, database *sql.DB, rows []importer.PartImportRow) ([]importedCategory, error) {
	resolved := make([]importedCategory, len(rows))
	needsCategories := false
	for _, row := range rows {
		if strings.TrimSpace(row.CategoryName) != "" || len(row.CategorySpecifications) > 0 {
			needsCategories = true
			break
		}
	}
	if !needsCategories {
		return resolved, nil
	}

	service := categories.NewService(database)
	all, err := service.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("load categories for import: %w", err)
	}
	byName := make(map[string]categories.Category, len(all))
	for _, item := range all {
		byName[strings.ToLower(item.Name)] = item
	}
	attributeCache := make(map[int64]map[string]categories.Attribute)
	for index, row := range rows {
		name := strings.TrimSpace(row.CategoryName)
		if name == "" {
			if len(row.CategorySpecifications) > 0 {
				return nil, fmt.Errorf("row %d: category specifications require a category", row.RowNumber)
			}
			continue
		}
		category, ok := byName[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("row %d: unknown category %q; create it before importing products", row.RowNumber, name)
		}
		attributes, ok := attributeCache[category.ID]
		if !ok {
			listed, err := service.ListAttributes(ctx, category.ID)
			if err != nil {
				return nil, fmt.Errorf("row %d: load category specifications: %w", row.RowNumber, err)
			}
			attributes = make(map[string]categories.Attribute, len(listed))
			for _, attribute := range listed {
				attributes[strings.ToLower(attribute.Name)] = attribute
			}
			attributeCache[category.ID] = attributes
		}
		keys := make([]string, 0, len(row.CategorySpecifications))
		for key := range row.CategorySpecifications {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		specifications := make([]categories.Specification, 0, len(keys))
		for _, key := range keys {
			attribute, exists := attributes[strings.ToLower(strings.TrimSpace(key))]
			if !exists {
				return nil, fmt.Errorf("row %d: category %q has no specification %q", row.RowNumber, category.Name, key)
			}
			raw := row.CategorySpecifications[key]
			specification := categories.Specification{AttributeID: attribute.ID}
			switch attribute.DataType {
			case categories.TextType:
				var value string
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, fmt.Errorf("row %d: specification %q must be a JSON string", row.RowNumber, key)
				}
				specification.Text = &value
			case categories.NumberType:
				var value float64
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, fmt.Errorf("row %d: specification %q must be a JSON number", row.RowNumber, key)
				}
				specification.Number = &value
			case categories.BooleanType:
				var value bool
				if err := json.Unmarshal(raw, &value); err != nil {
					return nil, fmt.Errorf("row %d: specification %q must be a JSON boolean", row.RowNumber, key)
				}
				specification.Boolean = &value
			default:
				return nil, fmt.Errorf("row %d: category specification %q has an unsupported type", row.RowNumber, key)
			}
			specifications = append(specifications, specification)
		}
		if err := service.ValidateSpecifications(ctx, category.ID, specifications); err != nil {
			return nil, fmt.Errorf("row %d: invalid specifications for category %q: %w", row.RowNumber, category.Name, err)
		}
		resolved[index] = importedCategory{ID: category.ID, Specifications: specifications}
	}
	return resolved, nil
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
				ContainerID: sql.NullInt64{Int64: c.ID, Valid: true},
				LedIndex:    sql.NullInt64{Int64: ledIndex - c.LedStart, Valid: true},
			})
		}
		return 0, sql.ErrNoRows
	default:
		return 0, fmt.Errorf("LED coordinate space %q is unresolved", space)
	}
}
