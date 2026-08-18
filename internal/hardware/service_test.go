package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
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
	wClient := wled.NewClient()
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

	// Create Container
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Default",
		ControllerID: c.ID,
		SegmentID:    0,
	})

	// Add a bin
	store.CreateBin(ctx, db.CreateBinParams{
		Name:        "Bin 1",
		ContainerID: cont,
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

func TestService_SaveGridWidth(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":685,"width":15,"name":"Drawer 43"}]`
	configData := `[{"id":null,"name":"Default","segment_id":0,"position_index":0,"config":{"type":"linear","rows":1,"cols":16,"total":1024}}]`

	if _, err := s.SaveGrid(ctx, c.ID, gridData, configData); err != nil {
		t.Fatalf("SaveGrid failed: %v", err)
	}

	containers, err := s.GetContainers(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetContainers failed: %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}

	bins, err := s.GetBinsByController(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetBinsByController failed: %v", err)
	}
	if len(bins) != 1 {
		t.Fatalf("expected 1 bin, got %d", len(bins))
	}

	bin := bins[0]
	if !bin.Width.Valid || bin.Width.Int64 != 15 {
		t.Errorf("expected width 15, got %+v", bin.Width)
	}
	if !bin.LedIndex.Valid || bin.LedIndex.Int64 != 685 {
		t.Errorf("expected led_index 685, got %+v", bin.LedIndex)
	}
}

func TestService_SaveGridWidthClamped(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// Width 0 and missing width should both clamp to 1 (backward compatible).
	for name, gridData := range map[string]string{
		"zero-width":    `[{"container_index":0,"x":0,"y":0,"led_index":5,"width":0,"name":"B"}]`,
		"missing-width": `[{"container_index":0,"x":0,"y":0,"led_index":5,"name":"B"}]`,
	} {
		configData := `[{"id":null,"name":"Default","segment_id":0,"position_index":0,"config":{"type":"linear","rows":1,"cols":1,"total":16}}]`
		if _, err := s.SaveGrid(ctx, c.ID, gridData, configData); err != nil {
			t.Fatalf("%s: SaveGrid failed: %v", name, err)
		}

		bins, err := s.GetBinsByController(ctx, c.ID)
		if err != nil {
			t.Fatalf("%s: GetBinsByController failed: %v", name, err)
		}
		if len(bins) != 1 {
			t.Fatalf("%s: expected 1 bin, got %d", name, len(bins))
		}
		if !bins[0].Width.Valid || bins[0].Width.Int64 != 1 {
			t.Errorf("%s: expected clamped width 1, got %+v", name, bins[0].Width)
		}
	}
}

func TestService_ExportImportConfig_RoundTrip(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Shelf LEDs", IpAddress: "10.0.0.50", Port: sql.NullInt64{Int64: 21324, Valid: true}})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// Build a layout with a container and a multi-LED bin.
	if _, err := s.SaveGrid(ctx, c.ID,
		`[{"container_index":0,"x":0,"y":0,"led_index":685,"width":15,"name":"Drawer 43"}]`,
		`[{"id":null,"name":"Default","segment_id":0,"position_index":0,"config":{"type":"linear","rows":1,"cols":16,"total":700}}]`,
	); err != nil {
		t.Fatalf("SaveGrid failed: %v", err)
	}

	// Export.
	data, err := s.ExportConfig(ctx, c.ID)
	if err != nil {
		t.Fatalf("ExportConfig failed: %v", err)
	}
	var exported map[string]any
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("export is not valid json: %v", err)
	}
	ctrl := exported["controller"].(map[string]any)
	if ctrl["name"] != "Shelf LEDs" || ctrl["ip_address"] != "10.0.0.50" {
		t.Errorf("unexpected exported controller metadata %v", ctrl)
	}
	containers := exported["containers"].([]any)
	bins := exported["bins"].([]any)
	if len(containers) != 1 {
		t.Fatalf("expected 1 exported container, got %d", len(containers))
	}
	if len(bins) != 1 {
		t.Fatalf("expected 1 exported bin, got %d", len(bins))
	}
	bin := bins[0].(map[string]any)
	if int(bin["led_index"].(float64)) != 685 || int(bin["width"].(float64)) != 15 {
		t.Errorf("unexpected exported bin %v", bin)
	}

	// Import into a new controller (overriding IP).
	newName := "Imported Shelf"
	newData := string(data)
	newID, err := s.ImportConfig(ctx, newName, "10.0.0.99", 0, []byte(newData))
	if err != nil {
		t.Fatalf("ImportConfig failed: %v", err)
	}
	if newID == c.ID {
		t.Fatalf("expected a new controller, got same id %d", newID)
	}

	imported, err := s.GetController(ctx, newID)
	if err != nil {
		t.Fatalf("failed to get imported controller: %v", err)
	}
	if imported.Name != newName {
		t.Errorf("expected overridden name %q, got %q", newName, imported.Name)
	}
	if imported.IpAddress != "10.0.0.99" {
		t.Errorf("expected overridden ip %q, got %q", "10.0.0.99", imported.IpAddress)
	}

	bins2, err := s.GetBinsByController(ctx, newID)
	if err != nil {
		t.Fatalf("failed to get bins of imported controller: %v", err)
	}
	if len(bins2) != 1 {
		t.Fatalf("expected 1 imported bin, got %d", len(bins2))
	}
	if !bins2[0].Width.Valid || bins2[0].Width.Int64 != 15 {
		t.Errorf("expected imported width 15, got %+v", bins2[0].Width)
	}

	containers2, err := s.GetContainers(ctx, newID)
	if err != nil {
		t.Fatalf("failed to get containers of imported controller: %v", err)
	}
	if len(containers2) != 1 {
		t.Fatalf("expected 1 imported container, got %d", len(containers2))
	}
}

func TestService_ImportConfig_Invalid(t *testing.T) {
	s, _, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	if _, err := s.ImportConfig(ctx, "X", "1.1.1.1", 0, []byte("not json")); err == nil {
		t.Fatal("expected error for invalid json")
	}
	if _, err := s.ImportConfig(ctx, "X", "1.1.1.1", 0, []byte(`{"version":"99.9","controller":{"ip_address":"1.1.1.1"}}`)); err == nil {
		t.Fatal("expected error for unsupported version")
	}
}
