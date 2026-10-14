package productdetails

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/utils"
)

var (
	ErrInvalidDetails = errors.New("invalid product details")
	ErrDuplicateSKU   = errors.New("internal SKU already exists")
)

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

type ExtendedDetails struct {
	PartID              int64
	SKU                 string
	Brand               string
	Model               string
	Colour              string
	Material            string
	Finish              string
	Condition           string
	Notes               string
	LengthMM            sql.NullFloat64
	WidthMM             sql.NullFloat64
	HeightMM            sql.NullFloat64
	DepthMM             sql.NullFloat64
	DimensionUnit       string
	WeightG             sql.NullFloat64
	WeightUnit          string
	PurchasedFrom       string
	SupplierProductCode string
	PurchaseDate        sql.NullString
	PurchasePrice       sql.NullFloat64
	Currency            string
	ProductURL          string
	PurchaseNotes       string
}

func (d *ExtendedDetails) NormalizeAndValidate() error {
	d.SKU = strings.TrimSpace(d.SKU)
	d.Brand = strings.TrimSpace(d.Brand)
	d.Model = strings.TrimSpace(d.Model)
	d.Colour = strings.TrimSpace(d.Colour)
	d.Material = strings.TrimSpace(d.Material)
	d.Finish = strings.TrimSpace(d.Finish)
	d.Condition = strings.TrimSpace(d.Condition)
	d.Notes = strings.TrimSpace(d.Notes)
	d.PurchasedFrom = strings.TrimSpace(d.PurchasedFrom)
	d.SupplierProductCode = strings.TrimSpace(d.SupplierProductCode)
	d.ProductURL = strings.TrimSpace(d.ProductURL)
	d.PurchaseNotes = strings.TrimSpace(d.PurchaseNotes)
	d.Currency = strings.ToUpper(strings.TrimSpace(d.Currency))
	for _, value := range []struct {
		text string
		max  int
	}{
		{d.SKU, 80}, {d.Brand, 120}, {d.Model, 120}, {d.Colour, 80}, {d.Material, 120},
		{d.Finish, 120}, {d.Condition, 80}, {d.Notes, 4000}, {d.PurchasedFrom, 200},
		{d.SupplierProductCode, 120}, {d.PurchaseNotes, 4000}, {d.ProductURL, 2048},
	} {
		if len(value.text) > value.max {
			return ErrInvalidDetails
		}
	}
	if !positiveNumber(d.LengthMM) || !positiveNumber(d.WidthMM) || !positiveNumber(d.HeightMM) || !positiveNumber(d.DepthMM) ||
		!nonnegativeNumber(d.WeightG) || !nonnegativeNumber(d.PurchasePrice) {
		return ErrInvalidDetails
	}
	if d.DimensionUnit == "" {
		d.DimensionUnit = "mm"
	}
	if d.DimensionUnit != "mm" && d.DimensionUnit != "cm" && d.DimensionUnit != "in" {
		return ErrInvalidDetails
	}
	if d.WeightUnit == "" {
		d.WeightUnit = "g"
	}
	if d.WeightUnit != "g" && d.WeightUnit != "kg" && d.WeightUnit != "oz" && d.WeightUnit != "lb" {
		return ErrInvalidDetails
	}
	if d.Currency == "" {
		d.Currency = "AUD"
	}
	if !currencyCode.MatchString(d.Currency) {
		return ErrInvalidDetails
	}
	if d.PurchaseDate.Valid && d.PurchaseDate.String != "" {
		parsed, err := time.Parse("2006-01-02", d.PurchaseDate.String)
		if err != nil || parsed.Format("2006-01-02") != d.PurchaseDate.String {
			return ErrInvalidDetails
		}
	} else {
		d.PurchaseDate = sql.NullString{}
	}
	if d.ProductURL != "" {
		if _, ok := utils.SafeHTTPURL(d.ProductURL); !ok {
			return ErrInvalidDetails
		}
	}
	return nil
}

func (d ExtendedDetails) IsEmpty() bool {
	return d.SKU == "" && d.Brand == "" && d.Model == "" && d.Colour == "" && d.Material == "" && d.Finish == "" && d.Condition == "" && d.Notes == "" &&
		!d.LengthMM.Valid && !d.WidthMM.Valid && !d.HeightMM.Valid && !d.DepthMM.Valid && !d.WeightG.Valid && d.PurchasedFrom == "" && d.SupplierProductCode == "" &&
		!d.PurchaseDate.Valid && !d.PurchasePrice.Valid && d.ProductURL == "" && d.PurchaseNotes == ""
}

func ValidateExtendedDetails(ctx context.Context, database *sql.DB, details ExtendedDetails, excludePartID int64) error {
	if err := details.NormalizeAndValidate(); err != nil {
		return err
	}
	if details.SKU != "" {
		var exists int
		if err := database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM part_details WHERE sku = ? AND part_id <> ?)`, details.SKU, excludePartID).Scan(&exists); err != nil {
			return err
		}
		if exists != 0 {
			return ErrDuplicateSKU
		}
	}
	return nil
}

func SaveExtendedDetails(ctx context.Context, database *sql.DB, details ExtendedDetails) error {
	if details.PartID <= 0 {
		return ErrInvalidDetails
	}
	if err := ValidateExtendedDetails(ctx, database, details, details.PartID); err != nil {
		return err
	}
	if err := details.NormalizeAndValidate(); err != nil {
		return err
	}
	if details.IsEmpty() {
		_, err := database.ExecContext(ctx, `DELETE FROM part_details WHERE part_id = ?`, details.PartID)
		return err
	}
	_, err := database.ExecContext(ctx, `INSERT INTO part_details (
		part_id, sku, brand, model, colour, material, finish, condition, notes,
		length_mm, width_mm, height_mm, depth_mm, dimension_unit, weight_g, weight_unit, purchased_from,
		supplier_product_code, purchase_date, purchase_price, currency, product_url, purchase_notes
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(part_id) DO UPDATE SET
		sku=excluded.sku, brand=excluded.brand, model=excluded.model, colour=excluded.colour,
		material=excluded.material, finish=excluded.finish, condition=excluded.condition, notes=excluded.notes,
		length_mm=excluded.length_mm, width_mm=excluded.width_mm, height_mm=excluded.height_mm,
		depth_mm=excluded.depth_mm, dimension_unit=excluded.dimension_unit, weight_g=excluded.weight_g,
		weight_unit=excluded.weight_unit, purchased_from=excluded.purchased_from,
		supplier_product_code=excluded.supplier_product_code, purchase_date=excluded.purchase_date,
		purchase_price=excluded.purchase_price, currency=excluded.currency, product_url=excluded.product_url,
		purchase_notes=excluded.purchase_notes, updated_at=CURRENT_TIMESTAMP`,
		details.PartID, nullableText(details.SKU), details.Brand, details.Model, details.Colour, details.Material, details.Finish, details.Condition, details.Notes,
		nullableNumber(details.LengthMM), nullableNumber(details.WidthMM), nullableNumber(details.HeightMM), nullableNumber(details.DepthMM), details.DimensionUnit,
		nullableNumber(details.WeightG), details.WeightUnit, details.PurchasedFrom, details.SupplierProductCode, nullableText(details.PurchaseDate.String),
		nullableNumber(details.PurchasePrice), details.Currency, details.ProductURL, details.PurchaseNotes)
	return err
}

func GetExtendedDetails(ctx context.Context, database *sql.DB, partID int64) (ExtendedDetails, error) {
	var d ExtendedDetails
	d.PartID = partID
	err := database.QueryRowContext(ctx, `SELECT sku, brand, model, colour, material, finish, condition, notes, length_mm, width_mm, height_mm, depth_mm, dimension_unit, weight_g, weight_unit, purchased_from, supplier_product_code, purchase_date, purchase_price, currency, product_url, purchase_notes FROM part_details WHERE part_id = ?`, partID).Scan(
		&d.SKU, &d.Brand, &d.Model, &d.Colour, &d.Material, &d.Finish, &d.Condition, &d.Notes, &d.LengthMM, &d.WidthMM, &d.HeightMM, &d.DepthMM, &d.DimensionUnit, &d.WeightG, &d.WeightUnit,
		&d.PurchasedFrom, &d.SupplierProductCode, &d.PurchaseDate, &d.PurchasePrice, &d.Currency, &d.ProductURL, &d.PurchaseNotes)
	if errors.Is(err, sql.ErrNoRows) {
		return ExtendedDetails{PartID: partID, Currency: "AUD", DimensionUnit: "mm", WeightUnit: "g"}, nil
	}
	return d, err
}

func positiveNumber(value sql.NullFloat64) bool {
	return !value.Valid || (!math.IsNaN(value.Float64) && !math.IsInf(value.Float64, 0) && value.Float64 > 0)
}

func nonnegativeNumber(value sql.NullFloat64) bool {
	return !value.Valid || (!math.IsNaN(value.Float64) && !math.IsInf(value.Float64, 0) && value.Float64 >= 0)
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableNumber(value sql.NullFloat64) any {
	if !value.Valid {
		return nil
	}
	return value.Float64
}
