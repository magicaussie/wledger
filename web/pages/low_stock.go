package pages

import "database/sql"

// LowStockPartView is a part that is at or below its reorder level, used by the
// Low Stock / Reorder page.
type LowStockPartView struct {
	ID                int64
	Name              string
	Description       sql.NullString
	PartNumber        sql.NullString
	Manufacturer      sql.NullString
	Supplier          sql.NullString
	Footprint         sql.NullString
	UnitCost          sql.NullFloat64
	BarcodeData       sql.NullString
	Tags              sql.NullString
	ImagePath         sql.NullString
	TotalStock        int64
	ReorderLevel      int64
	MinStockThreshold int64
	SuggestedOrder    int64
}

// IsCritical reports whether the part is at or below its minimum stock
// threshold, a stricter tier than merely being at/below the reorder level.
func (v LowStockPartView) IsCritical() bool {
	return v.MinStockThreshold > 0 && v.TotalStock <= v.MinStockThreshold
}

// SupplierQuery returns the best term to search suppliers with: the part number
// if present, otherwise the part name.
func (v LowStockPartView) SupplierQuery() string {
	if v.PartNumber.Valid && v.PartNumber.String != "" {
		return v.PartNumber.String
	}
	return v.Name
}
