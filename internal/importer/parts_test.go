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
