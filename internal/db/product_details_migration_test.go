package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

func TestProductDetailsMigrationRefusesLossyRollback(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "product-details.db"))
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
	partID, err := db.NewStore(conn).CreatePart(context.Background(), db.CreatePartParams{Name: "Measured item"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO part_details (part_id, sku, brand, length_mm) VALUES (?, ?, ?, ?)`, partID, "MEASURED-1", "Workshop", 25.4); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(conn, ".", 21); err == nil {
		t.Fatal("rollback dropped nonempty product details")
	}
	var version int64
	if err := conn.QueryRowContext(context.Background(), `SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY id DESC LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 22 {
		t.Fatalf("failed rollback changed schema version to %d, want 22", version)
	}
	var sku string
	if err := conn.QueryRow(`SELECT sku FROM part_details WHERE part_id = ?`, partID).Scan(&sku); err != nil || sku != "MEASURED-1" {
		t.Fatalf("rollback damaged product details: sku=%q err=%v", sku, err)
	}
}
