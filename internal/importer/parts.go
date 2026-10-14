package importer

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/tuxedocurly/wledger/internal/productdetails"
)

// PartImportRow represents a single row from the CSV
type PartImportRow struct {
	RowNumber              int
	Name                   string
	Description            string
	PartNumber             string
	Manufacturer           string
	Supplier               string
	UnitCost               float64
	ReorderLevel           int
	MinStockThreshold      int
	Footprint              string
	BarcodeData            string
	InitialQuantity        int
	Tags                   []string
	Links                  []string
	ControllerIP           string
	SegmentID              *int
	LEDIndex               *int
	Details                productdetails.ExtendedDetails
	CategoryName           string
	CategorySpecifications map[string]json.RawMessage
}

// Validate checks the business rules for a single row
func (r PartImportRow) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("row %d: 'Name' is required", r.RowNumber)
	}
	if r.UnitCost < 0 {
		return fmt.Errorf("row %d: 'Unit Cost' cannot be negative", r.RowNumber)
	}
	if r.InitialQuantity < 0 {
		return fmt.Errorf("row %d: 'Quantity' cannot be negative", r.RowNumber)
	}
	if r.ReorderLevel < 0 {
		return fmt.Errorf("row %d: 'Reorder Level' cannot be negative", r.RowNumber)
	}
	if r.MinStockThreshold < 0 {
		return fmt.Errorf("row %d: 'Min Stock' cannot be negative", r.RowNumber)
	}
	if err := r.Details.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("row %d: invalid extended product details: %w", r.RowNumber, err)
	}
	if r.CategoryName == "" && len(r.CategorySpecifications) > 0 {
		return fmt.Errorf("row %d: category specifications require a category", r.RowNumber)
	}
	for _, link := range r.Links {
		u, err := url.ParseRequestURI(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("row %d: invalid URL: %s", r.RowNumber, link)
		}
	}

	// Location Validation
	hasIP := r.ControllerIP != ""
	hasSeg := r.SegmentID != nil
	hasLed := r.LEDIndex != nil

	if hasIP || hasSeg || hasLed {
		if !hasIP {
			return fmt.Errorf("row %d: 'Controller IP' is required when specifying location", r.RowNumber)
		}
		if !hasSeg {
			return fmt.Errorf("row %d: 'Segment ID' is required when specifying location", r.RowNumber)
		}
		if !hasLed {
			return fmt.Errorf("row %d: 'LED Index' is required when specifying location", r.RowNumber)
		}
	}

	return nil
}

// ParsePartsCSV reads a CSV stream and returns typed rows or errors
func ParsePartsCSV(input io.Reader) ([]PartImportRow, error) {
	reader := csv.NewReader(input)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	// Read Header
	headers, err := reader.Read()
	if err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("empty file")
		}
		return nil, fmt.Errorf("failed to read headers: %w", err)
	}

	// Map headers to column indices
	headerMap := make(map[string]int)
	for i, h := range headers {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(h), " ", ""))
		normalized = strings.ReplaceAll(normalized, "_", "") // "part_number" -> "partnumber"
		headerMap[normalized] = i
	}

	// Verify required header
	if _, ok := headerMap["name"]; !ok {
		return nil, fmt.Errorf("missing required column: 'Name' (Did you include the header row?)")
	}

	var rows []PartImportRow
	rowNum := 1 // Header is 1

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("row %d: parsing error: %w", rowNum+1, err)
		}
		rowNum++

		row := PartImportRow{RowNumber: rowNum}

		// Helper to get string value
		getString := func(key string) string {
			if idx, ok := headerMap[key]; ok && idx < len(record) {
				return strings.TrimSpace(record[idx])
			}
			return ""
		}

		// Helper to find which alias is present
		findColumn := func(aliases ...string) string {
			for _, key := range aliases {
				if _, ok := headerMap[key]; ok {
					return key
				}
			}
			return ""
		}

		// Helper to get float from a specific known key
		parseColumnFloat := func(key string) (float64, error) {
			s := getString(key)
			if s == "" {
				return 0, nil
			}
			// Sanitize
			s = strings.TrimPrefix(s, "$")
			s = strings.ReplaceAll(s, ",", "")
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid number for '%s': %s", key, s)
			}
			return f, nil
		}

		// Helper to get int from a specific known key
		parseColumnInt := func(key string) (int, error) {
			s := getString(key)
			if s == "" {
				return 0, nil
			}
			s = strings.ReplaceAll(s, ",", "")
			i, err := strconv.Atoi(s)
			if err != nil {
				f, fErr := strconv.ParseFloat(s, 64)
				if fErr == nil {
					return int(f), nil
				}
				return 0, fmt.Errorf("invalid integer for '%s': %s", key, s)
			}
			return i, nil
		}

		// Map Fields
		row.Name = getString("name")
		row.Description = getString("description")

		if key := findColumn("partnumber", "mpn"); key != "" {
			row.PartNumber = getString(key)
		}

		row.Manufacturer = getString("manufacturer")
		row.Supplier = getString("supplier")
		row.CategoryName = getString("category")
		if raw := getString("categoryspecifications"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &row.CategorySpecifications); err != nil {
				return nil, fmt.Errorf("row %d: invalid category specifications JSON: %w", rowNum, err)
			}
			if row.CategorySpecifications == nil {
				return nil, fmt.Errorf("row %d: category specifications must be a JSON object", rowNum)
			}
		}
		row.BarcodeData = getString("barcode")
		row.Details.SKU = getString("sku")
		row.Details.Brand = getString("brand")
		row.Details.Model = getString("model")
		row.Details.Colour = getString("colour")
		row.Details.Material = getString("material")
		row.Details.Finish = getString("finish")
		row.Details.Condition = getString("condition")
		row.Details.DimensionUnit = getString("dimensionunit")
		row.Details.WeightUnit = getString("weightunit")
		if key := findColumn("notes", "productnotes"); key != "" {
			row.Details.Notes = getString(key)
		}
		row.Details.PurchasedFrom = getString("purchasedfrom")
		row.Details.SupplierProductCode = getString("supplierproductcode")
		row.Details.ProductURL = getString("producturl")
		row.Details.PurchaseNotes = getString("purchasenotes")
		row.Details.Currency = getString("currency")
		if value := getString("purchasedate"); value != "" {
			row.Details.PurchaseDate = sql.NullString{String: value, Valid: true}
		}

		if key := findColumn("footprint", "package", "fp"); key != "" {
			row.Footprint = getString(key)
		}

		// Numeric Fields
		for _, field := range []struct {
			aliases []string
			target  *sql.NullFloat64
		}{
			{[]string{"lengthmm", "length"}, &row.Details.LengthMM},
			{[]string{"widthmm", "width"}, &row.Details.WidthMM},
			{[]string{"heightmm", "height"}, &row.Details.HeightMM},
			{[]string{"depthmm", "depth"}, &row.Details.DepthMM},
			{[]string{"weightg", "weight"}, &row.Details.WeightG},
			{[]string{"purchaseprice"}, &row.Details.PurchasePrice},
		} {
			if key := findColumn(field.aliases...); key != "" && getString(key) != "" {
				value, err := parseColumnFloat(key)
				if err != nil {
					return nil, fmt.Errorf("row %d: %w", rowNum, err)
				}
				*field.target = sql.NullFloat64{Float64: value, Valid: true}
			}
		}

		if key := findColumn("unitcost", "cost"); key != "" {
			if val, err := parseColumnFloat(key); err != nil {
				return nil, fmt.Errorf("row %d: %w", rowNum, err)
			} else {
				row.UnitCost = val
			}
		}

		if key := findColumn("reorderlevel", "reorder"); key != "" {
			if val, err := parseColumnInt(key); err != nil {
				return nil, fmt.Errorf("row %d: %w", rowNum, err)
			} else {
				row.ReorderLevel = val
			}
		}

		if key := findColumn("minstock", "min"); key != "" {
			if val, err := parseColumnInt(key); err != nil {
				return nil, fmt.Errorf("row %d: %w", rowNum, err)
			} else {
				row.MinStockThreshold = val
			}
		}

		if key := findColumn("quantity", "qty", "stock"); key != "" {
			if val, err := parseColumnInt(key); err != nil {
				return nil, fmt.Errorf("row %d: %w", rowNum, err)
			} else {
				row.InitialQuantity = val
			}
		}

		// Location Fields
		if key := findColumn("controllerip", "ipaddress", "ip"); key != "" {
			row.ControllerIP = getString(key)
		}

		if key := findColumn("segmentid", "segment", "seg"); key != "" {
			s := getString(key)
			if s != "" {
				if val, err := parseColumnInt(key); err != nil {
					return nil, fmt.Errorf("row %d: %w", rowNum, err)
				} else {
					row.SegmentID = &val
				}
			}
		}

		if key := findColumn("ledindex", "led", "binindex", "bin"); key != "" {
			s := getString(key)
			if s != "" {
				if val, err := parseColumnInt(key); err != nil {
					return nil, fmt.Errorf("row %d: %w", rowNum, err)
				} else {
					row.LEDIndex = &val
				}
			}
		}

		// Parse Multi-value fields (Tags & Links)
		if key := findColumn("tags"); key != "" {
			s := getString(key)
			if s != "" {
				parts := strings.Split(s, "|")
				for _, p := range parts {
					trimmed := strings.TrimSpace(p)
					if trimmed != "" {
						row.Tags = append(row.Tags, trimmed)
					}
				}
			}
		}

		if key := findColumn("links", "urls"); key != "" {
			s := getString(key)
			if s != "" {
				parts := strings.Split(s, "|")
				for _, p := range parts {
					trimmed := strings.TrimSpace(p)
					if trimmed != "" {
						row.Links = append(row.Links, trimmed)
					}
				}
			}
		}

		// Normalize optional typed product details before validating and storing.
		if err := row.Details.NormalizeAndValidate(); err != nil {
			return nil, fmt.Errorf("row %d: invalid extended product details: %w", row.RowNumber, err)
		}
		// Validate Domain Rules
		if err := row.Validate(); err != nil {
			return nil, err
		}

		rows = append(rows, row)
	}

	return rows, nil
}
