package importer

import (
	"strings"
	"testing"
)

func TestParsePartsCSV(t *testing.T) {
	tests := []struct {
		name        string
		csvContent  string
		expectError bool
		check       func([]PartImportRow) bool
	}{
		{
			name: "Valid Simple CSV",
			csvContent: `Name,Description,Unit Cost,Quantity
Resistor,10k Ohm,0.05,100
Capacitor,10uF,0.10,50`,
			expectError: false,
			check: func(rows []PartImportRow) bool {
				return len(rows) == 2 && rows[0].Name == "Resistor" && rows[0].InitialQuantity == 100 && rows[1].UnitCost == 0.10
			},
		},
		{
			name:        "Missing Name Header",
			csvContent:  `Description,Quantity` + "\n" + `Test,10`,
			expectError: true,
			check:       nil,
		},
		{
			name: "Missing Name Value",
			csvContent: `Name,Quantity
,10`,
			expectError: true, // Validate() should catch empty name
			check:       nil,
		},
		{
			name: "Invalid Number Type",
			csvContent: `Name,Quantity
Resistor,Ten`,
			expectError: true, // Parse int error
			check:       nil,
		},
		{
			name: "Negative Cost",
			csvContent: `Name,Unit Cost
BadPart,-5.00`,
			expectError: true, // Validate() rule
			check:       nil,
		},
		{
			name: "Alternate Headers",
			csvContent: `Name,MPN,Qty,Cost
Chip,NE555,10,$1.50`,
			expectError: false,
			check: func(rows []PartImportRow) bool {
				return rows[0].PartNumber == "NE555" && rows[0].InitialQuantity == 10 && rows[0].UnitCost == 1.50
			},
		},
		{
			name:        "Empty File",
			csvContent:  ``,
			expectError: true,
			check:       nil,
		},
		{
			name: "With Tags and Links",
			csvContent: `Name,Tags,Links
Microcontroller,IC|SMD,http://example.com/datasheet|http://example.com/product`,
			expectError: false,
			check: func(rows []PartImportRow) bool {
				return len(rows) == 1 && len(rows[0].Tags) == 2 && rows[0].Tags[0] == "IC" && len(rows[0].Links) == 2
			},
		},
		{
			name: "Extended General Product Fields",
			csvContent: `Name,SKU,Brand,Model,Colour,Material,Finish,Condition,Length mm,Width mm,Height mm,Depth mm,Dimension Unit,Weight g,Weight Unit,Notes,Purchased From,Supplier Product Code,Purchase Date,Purchase Price,Currency,Product URL,Purchase Notes
Bolt,BOLT-M8-35,Acme,M8x35,Silver,Steel,Zinc,New,35,8,8,35,in,12.5,lb,Reusable fastener,Local shop,AC-835,2026-01-15,0.42,AUD,https://example.com/bolt,Receipt 42`,
			expectError: false,
			check: func(rows []PartImportRow) bool {
				d := rows[0].Details
				return d.SKU == "BOLT-M8-35" && d.Brand == "Acme" && d.Model == "M8x35" && d.LengthMM.Valid && d.LengthMM.Float64 == 35 && d.DimensionUnit == "in" && d.WeightG.Valid && d.WeightG.Float64 == 12.5 && d.WeightUnit == "lb" && d.Currency == "AUD" && d.PurchaseDate.String == "2026-01-15" && d.SupplierProductCode == "AC-835"
			},
		},
		{
			name:        "Invalid Product Date",
			csvContent:  "Name,Purchase Date\nBolt,not-a-date",
			expectError: true,
		},
		{
			name: "With Location Data",
			csvContent: `Name,Controller IP,Segment ID,LED Index
Led,192.168.1.100,2,5`,
			expectError: false,
			check: func(rows []PartImportRow) bool {
				return len(rows) == 1 && rows[0].ControllerIP == "192.168.1.100" && *rows[0].SegmentID == 2 && *rows[0].LEDIndex == 5
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reader := strings.NewReader(tc.csvContent)

			rows, err := ParsePartsCSV(reader)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if tc.check != nil && !tc.check(rows) {
					t.Errorf("Validation check failed for rows: %+v", rows)
				}
			}
		})
	}
}
