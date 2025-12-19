package main

import (
	"database/sql"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestNewDashboardViewModel(t *testing.T) {
	// Mock Data
	rows := []db.GetDashboardGridRow{
		// Controller B: Bin (1,1) - Critical
		{
			ControllerID:      2,
			ControllerName:    "Controller B",
			BinID:             10,
			BinName:           "Bin 10",
			GridX:             sql.NullInt64{Int64: 1, Valid: true},
			GridY:             sql.NullInt64{Int64: 1, Valid: true},
			PartID:            sql.NullInt64{Int64: 100, Valid: true},
			Quantity:          sql.NullInt64{Int64: 0, Valid: true},
			MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true}, // 0 <= 5 -> Critical
		},
		// Controller A: Bin (0,0) - OK
		{
			ControllerID:      1,
			ControllerName:    "Controller A",
			BinID:             1,
			BinName:           "Bin 1",
			GridX:             sql.NullInt64{Int64: 0, Valid: true},
			GridY:             sql.NullInt64{Int64: 0, Valid: true},
			PartID:            sql.NullInt64{Int64: 101, Valid: true},
			Quantity:          sql.NullInt64{Int64: 10, Valid: true},
			MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true}, // 10 > 5 -> OK
		},
		// Controller A: Bin (0,1) - Low (GridY 1 comes after GridY 0)
		{
			ControllerID:      1,
			ControllerName:    "Controller A",
			BinID:             2,
			BinName:           "Bin 2",
			GridX:             sql.NullInt64{Int64: 0, Valid: true},
			GridY:             sql.NullInt64{Int64: 1, Valid: true},
			PartID:            sql.NullInt64{Int64: 102, Valid: true},
			Quantity:          sql.NullInt64{Int64: 6, Valid: true},
			MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true},
			ReorderLevel:      sql.NullInt64{Int64: 10, Valid: true}, // 6 <= 10 -> Low
		},
	}

	// Execute
	vm := newDashboardViewModel(rows)

	// Assertions

	// Controller Sorting (A before B)
	if len(vm) != 2 {
		t.Fatalf("Expected 2 controllers, got %d", len(vm))
	}
	if vm[0].Name != "Controller A" {
		t.Errorf("Expected Controller A first, got %s", vm[0].Name)
	}
	if vm[1].Name != "Controller B" {
		t.Errorf("Expected Controller B second, got %s", vm[1].Name)
	}

	// Bin Sorting for Controller A (GridY 0 before GridY 1)
	binsA := vm[0].Bins
	if len(binsA) != 2 {
		t.Fatalf("Expected 2 bins for Controller A, got %d", len(binsA))
	}
	if binsA[0].GridY != 0 {
		t.Errorf("Expected first bin to be at Y=0, got %d", binsA[0].GridY)
	}
	if binsA[1].GridY != 1 {
		t.Errorf("Expected second bin to be at Y=1, got %d", binsA[1].GridY)
	}

	// Status Calculation
	// Bin 1 (OK)
	if len(binsA[0].Statuses) != 1 || binsA[0].Statuses[0] != "ok" {
		t.Errorf("Expected Bin 1 status 'ok', got %v", binsA[0].Statuses)
	}
	// Bin 2 (Low)
	if len(binsA[1].Statuses) != 1 || binsA[1].Statuses[0] != "low" {
		t.Errorf("Expected Bin 2 status 'low', got %v", binsA[1].Statuses)
	}
	// Bin 10 (Critical) - Controller B
	binsB := vm[1].Bins
	if len(binsB[0].Statuses) != 1 || binsB[0].Statuses[0] != "critical" {
		t.Errorf("Expected Bin 10 status 'critical', got %v", binsB[0].Statuses)
	}
}
