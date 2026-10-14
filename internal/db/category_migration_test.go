package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

func TestCategoryMigrationPreservesExistingInventoryAndBlocksDestructiveDown(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "category-migration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	goose.SetBaseFS(schema.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(conn, ".", 20); err != nil {
		t.Fatalf("migrate to previous schema: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO bins (id, name) VALUES (1, 'B1'); INSERT INTO parts (id, name) VALUES (1, 'Legacy item'); INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (1, 1, 1, 17)`); err != nil {
		t.Fatalf("seed pre-category inventory: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("upgrade category migration: %v", err)
	}
	var name string
	var quantity int64
	if err := conn.QueryRow(`SELECT p.name, a.quantity FROM parts p JOIN part_assignments a ON a.part_id = p.id WHERE p.id = 1`).Scan(&name, &quantity); err != nil {
		t.Fatalf("read preserved inventory: %v", err)
	}
	if name != "Legacy item" || quantity != 17 {
		t.Fatalf("legacy inventory changed: name=%q quantity=%d", name, quantity)
	}

	if _, err := conn.Exec(`INSERT INTO categories (id, name) VALUES (1, 'Tools')`); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(conn, ".", 20); err == nil {
		t.Fatal("migration rollback discarded category data")
	}
	var version int64
	if err := conn.QueryRowContext(context.Background(), `SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY id DESC LIMIT 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 21 {
		t.Fatalf("failed rollback changed schema version to %d, want 21", version)
	}
}
