package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

func TestProductMeasurementUnitsMigrationPreservesLegacyValuesAndGuardsRollback(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "measurement-units.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	goose.SetBaseFS(schema.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	partID, err := db.NewStore(conn).CreatePart(ctx, db.CreatePartParams{Name: "Measured item"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO part_details (part_id, sku, length_mm, weight_g) VALUES (?, ?, ?, ?)`, partID, "MEASURED-1", 25.4, 12.5); err != nil {
		t.Fatal(err)
	}
	var dimensionUnit, weightUnit string
	if err := conn.QueryRowContext(ctx, `SELECT dimension_unit, weight_unit FROM part_details WHERE part_id = ?`, partID).Scan(&dimensionUnit, &weightUnit); err != nil {
		t.Fatal(err)
	}
	if dimensionUnit != "mm" || weightUnit != "g" {
		t.Fatalf("legacy record units = %q/%q, want mm/g", dimensionUnit, weightUnit)
	}
	if err := goose.DownTo(conn, ".", 22); err != nil {
		t.Fatalf("rollback default units: %v", err)
	}
	var sku string
	if err := conn.QueryRowContext(ctx, `SELECT sku FROM part_details WHERE part_id = ?`, partID).Scan(&sku); err != nil || sku != "MEASURED-1" {
		t.Fatalf("rollback changed legacy product details: sku=%q err=%v", sku, err)
	}
	if err := goose.UpTo(conn, ".", 23); err != nil {
		t.Fatalf("reapply units migration: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE part_details SET dimension_unit = 'in', weight_unit = 'lb' WHERE part_id = ?`, partID); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(conn, ".", 22); err == nil {
		t.Fatal("rollback discarded non-default measurement units")
	}
	if err := conn.QueryRowContext(ctx, `SELECT dimension_unit, weight_unit FROM part_details WHERE part_id = ?`, partID).Scan(&dimensionUnit, &weightUnit); err != nil {
		t.Fatal(err)
	}
	if dimensionUnit != "in" || weightUnit != "lb" {
		t.Fatalf("refused rollback changed saved units to %q/%q", dimensionUnit, weightUnit)
	}
}
