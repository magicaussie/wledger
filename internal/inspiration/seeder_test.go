package inspiration

import (
	"context"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// setupTestDB creates an in memory DB and applies the schema using db.Migrate
func setupTestDB(t *testing.T) (*db.Queries, func()) {
	// Open in-memory DB
	// cache=shared ensures different connections see the same in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Apply migrations automatically
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	// Create queries helper
	q := db.New(conn)

	// return cleanup function
	return q, func() {
		conn.Close()
	}
}

func TestSeedTemplates(t *testing.T) {
	q, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Initialize Settings (Required for Seeder to check flag)
	err := q.InitSettings(ctx)
	if err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}

	svc := NewService(q)

	// Run Seeder First Time
	err = svc.SeedTemplates(ctx)
	if err != nil {
		t.Fatalf("failed to seed templates: %v", err)
	}

	// Verify templates inserted
	templates, err := q.GetAllInspirationTemplates(ctx)
	if err != nil {
		t.Fatalf("failed to get templates: %v", err)
	}
	if len(templates) != 6 {
		t.Errorf("expected 6 templates to be seeded, got %d", len(templates))
	}

	// Verify flag is set
	settings, err := q.GetSettings(ctx)
	if err != nil {
		t.Fatalf("failed to get settings: %v", err)
	}
	if !settings.InspirationSeedsApplied.Bool {
		t.Errorf("expected seeds applied flag to be true")
	}

	// User Deletes a Template
	err = q.DeleteInspirationTemplate(ctx, templates[0].ID)
	if err != nil {
		t.Fatalf("failed to delete template: %v", err)
	}

	countAfterDelete := len(templates) - 1

	// Run Seeder Second Time
	err = svc.SeedTemplates(ctx)
	if err != nil {
		t.Fatalf("failed to seed templates 2nd time: %v", err)
	}

	// Verify count matches after delete (should NOT re-seed)
	templatesAfter, err := q.GetAllInspirationTemplates(ctx)
	if err != nil {
		t.Fatalf("failed to get templates after 2nd seed: %v", err)
	}
	if len(templatesAfter) != countAfterDelete {
		t.Errorf("expected %d templates after 2nd seed, got %d. Seeder likely re-seeded!", countAfterDelete, len(templatesAfter))
	}
}
