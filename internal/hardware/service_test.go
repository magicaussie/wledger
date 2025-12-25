package hardware

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func setupTest(t *testing.T) (Service, db.Store, *sql.DB) {
	dbConn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	store := db.NewStore(dbConn)
	wClient := wled.New()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	return NewService(store, wClient, logger), store, dbConn
}

func TestService_ListControllers(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	_, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Test",
		IpAddress: "1.1.1.1",
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	list, err := s.ListControllers(ctx)
	if err != nil {
		t.Errorf("failed to list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 controller, got %d", len(list))
	}
}

func TestService_DeleteController(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	c, _ := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Test",
		IpAddress: "1.1.1.1",
	})

	// Add a bin
	store.CreateBin(ctx, db.CreateBinParams{
		Name:         "Bin 1",
		ControllerID: sql.NullInt64{Int64: c.ID, Valid: true},
	})

	err := s.DeleteController(ctx, c.ID)
	if err != nil {
		t.Errorf("failed to delete: %v", err)
	}

	// Verify cascade
	var count int
	dbConn.QueryRow("SELECT count(*) FROM bins").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 bins, got %d", count)
	}
}
