package db_test

import (
	"context"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestMigrate(t *testing.T) {
	// Open in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer conn.Close()

	// Apply migrations
	err = db.Migrate(conn)
	if err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	// Verify that the users table was created
	q := db.New(conn)
	ctx := context.Background()
	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("failed to count users (table might not exist): %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 users, got %d", count)
	}

	// Verify that goose migration table exists
	var tableName string
	err = conn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='goose_db_version'").Scan(&tableName)
	if err != nil {
		t.Fatalf("failed to find goose_db_version table (migrations might not have been tracked): %v", err)
	}

	// Run migration again to verify idempotency
	err = db.Migrate(conn)
	if err != nil {
		t.Fatalf("failed to migrate second time (idempotency failure): %v", err)
	}
}
