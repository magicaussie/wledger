package db_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// setupTestDB creates an in memory DB and applies the schema
func setupTestDB(t *testing.T) (*db.Queries, func()) {
	// Open in-memory DB
	// cache=shared ensures different connections see the same in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Read schema files
	// assuming test runs from package directory, need to go up to the root
	schemaPath := "../../sql/schema"
	files := []string{"001_init.sql", "002_fts_triggers.sql"}

	for _, file := range files {
		context, err := os.ReadFile(filepath.Join(schemaPath, file))
		if err != nil {
			t.Fatalf("failed to read schema file %s: %v", file, err)
		}
		if _, err := conn.Exec(string(context)); err != nil {
			t.Fatalf("failed to apply schema %s: %v", file, err)
		}
	}

	// Create queries helper
	q := db.New(conn)

	// return celanup function
	return q, func() {
		conn.Close()
	}
}

func TestUserLifeCycle(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create user
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        "test@wledger.app",
		PasswordHash: "oh_my_gawd_so_secret",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}
	if user.Email != "test@wledger.app" {
		t.Errorf("expected email test@wledger.app, got %s", user.Email)
	}

	// Get user
	fetched, err := q.GetUserByEmail(ctx, "test@wledger.app")
	if err != nil {
		t.Fatalf("failed to get user: %v", err)
	}

	if fetched.ID != user.ID {
		t.Errorf("fetched ID %d does not match created ID %d", fetched.ID, user.ID)
	}

	// Count users
	count, err := q.CountUsers(ctx)
	if err != nil {
		t.Fatalf("failed to count users: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 user, got %d", count)
	}
}

func TestSettingsInit(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	//Init settings
	err := q.InitSettings(ctx)
	if err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}

	// Get settings
	settings, err := q.GetSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get settings: %v", err)
	}

	// Check default settings
	if settings.LocateTimeoutSeconds.Int64 != 10 { // default should be 10 seconds
		t.Errorf("expected default timeout 10, got %d", settings.LocateTimeoutSeconds.Int64)
	}
}

func TestCascadeDelete(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create Part
	part, err := q.CreatePart(ctx, db.CreatePartParams{
		Name: "Doom Part",
	})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}

	// Create Orphaned Assignment
	err = q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   part,
		BinID:    sql.NullInt64{Valid: false},
		Quantity: 100,
	})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	// Verify it exists
	stats, _ := q.GetDashboardStats(ctx)
	if stats.TotalItemsInStock != 100 {
		t.Fatalf("expected 100 items, got %d", stats.TotalItemsInStock)
	}

	// Delete Part
	err = q.DeletePart(ctx, part)
	if err != nil {
		t.Fatalf("delete part: %v", err)
	}

	// Verify Cascade
	stats, _ = q.GetDashboardStats(ctx)
	if stats.TotalItemsInStock != 0 {
		t.Errorf("CASCADE FAILED: expected 0 items, got %d", stats.TotalItemsInStock)
	}
}
