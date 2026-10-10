package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
)

func setupTest(t *testing.T) (Service, db.Store, *sql.DB) {
	t.Helper()
	// Use a per-test in-memory database so tests cannot contaminate each other
	// through the shared-cache namespace.
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dbConn, err := db.Open("file:" + name + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	store := db.NewStore(dbConn)
	return NewService(store), store, dbConn
}

func TestService_GetStats(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	_, _ = store.CreatePart(ctx, db.CreatePartParams{Name: "Test Part"})

	stats, err := s.GetStats(ctx)
	if err != nil {
		t.Fatalf("failed to get stats: %v", err)
	}

	if stats.TotalParts != 1 {
		t.Errorf("expected 1 part, got %d", stats.TotalParts)
	}
}

func TestService_GetGrid(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()

	ctx := context.Background()
	// Create Controller
	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl A", IpAddress: "1.1.1.1"})

	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Cont", ControllerID: c.ID, SegmentID: 0,
	})

	// Create Bin
	b, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "Bin 1", ContainerID: cont,
		GridX: sql.NullInt64{Int64: 0, Valid: true}, GridY: sql.NullInt64{Int64: 0, Valid: true},
	})
	// Create Part
	p, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Part 1", MinStockThreshold: sql.NullInt64{Int64: 10, Valid: true}})
	// Assign
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: p, BinID: sql.NullInt64{Int64: b, Valid: true}, Quantity: 5,
	})

	grid, err := s.GetGrid(ctx)
	if err != nil {
		t.Fatalf("failed to get grid: %v", err)
	}

	if len(grid) != 1 {
		t.Fatalf("expected 1 controller, got %d", len(grid))
	}

	if len(grid[0].Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(grid[0].Containers))
	}

	if len(grid[0].Containers[0].Bins) != 1 {
		t.Fatalf("expected 1 bin, got %d", len(grid[0].Containers[0].Bins))
	}

	if len(grid[0].Containers[0].Bins[0].Statuses) != 1 || grid[0].Containers[0].Bins[0].Statuses[0] != "critical" {
		t.Errorf("expected critical status, got %v", grid[0].Containers[0].Bins[0].Statuses)
	}
}

// TestService_GetGrid_ControllerWithoutContainers verifies that a controller with
// no containers is still returned. Previously the query started FROM bins and
// INNER JOINed containers/controllers, so such a controller disappeared entirely.
func TestService_GetGrid_ControllerWithoutContainers(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	if _, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Empty Controller", IpAddress: "10.0.0.1"}); err != nil {
		t.Fatalf("create controller: %v", err)
	}

	grid, err := s.GetGrid(ctx)
	if err != nil {
		t.Fatalf("GetGrid: %v", err)
	}
	if len(grid) != 1 {
		t.Fatalf("expected 1 controller, got %d", len(grid))
	}
	if grid[0].Name != "Empty Controller" {
		t.Errorf("expected controller name preserved, got %q", grid[0].Name)
	}
	if len(grid[0].Containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(grid[0].Containers))
	}
}

// TestService_GetGrid_ContainerWithoutMappedBins verifies a container whose bins
// are all unmapped (grid_x/grid_y NULL) is still shown, with no bins attached.
func TestService_GetGrid_ContainerWithoutMappedBins(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl", IpAddress: "10.0.0.2"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	cont, err := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Drawer", ControllerID: c.ID, SegmentID: 0})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	// Unmapped bin: no grid coordinates.
	if _, err := store.CreateBin(ctx, db.CreateBinParams{Name: "Unmapped", ContainerID: cont}); err != nil {
		t.Fatalf("create bin: %v", err)
	}

	grid, err := s.GetGrid(ctx)
	if err != nil {
		t.Fatalf("GetGrid: %v", err)
	}
	if len(grid) != 1 || len(grid[0].Containers) != 1 {
		t.Fatalf("expected 1 controller with 1 container, got %d controllers", len(grid))
	}
	if got := grid[0].Containers[0].Name; got != "Drawer" {
		t.Errorf("expected container name Drawer, got %q", got)
	}
	if len(grid[0].Containers[0].Bins) != 0 {
		t.Errorf("expected 0 mapped bins, got %d", len(grid[0].Containers[0].Bins))
	}
}

// TestService_GetGrid_MixedMappedAndUnmapped verifies only grid-mapped bins are
// rendered, while the container itself survives.
func TestService_GetGrid_MixedMappedAndUnmapped(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl", IpAddress: "10.0.0.3"})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Drawer", ControllerID: c.ID, SegmentID: 0})

	if _, err := store.CreateBin(ctx, db.CreateBinParams{
		Name: "Mapped", ContainerID: cont,
		GridX: sql.NullInt64{Int64: 0, Valid: true}, GridY: sql.NullInt64{Int64: 0, Valid: true},
	}); err != nil {
		t.Fatalf("create mapped bin: %v", err)
	}
	if _, err := store.CreateBin(ctx, db.CreateBinParams{Name: "Unmapped", ContainerID: cont}); err != nil {
		t.Fatalf("create unmapped bin: %v", err)
	}

	grid, err := s.GetGrid(ctx)
	if err != nil {
		t.Fatalf("GetGrid: %v", err)
	}
	if len(grid) != 1 || len(grid[0].Containers) != 1 {
		t.Fatalf("expected 1 controller with 1 container")
	}
	bins := grid[0].Containers[0].Bins
	if len(bins) != 1 {
		t.Fatalf("expected exactly 1 mapped bin, got %d", len(bins))
	}
	if bins[0].Name != "Mapped" {
		t.Errorf("expected the mapped bin, got %q", bins[0].Name)
	}
}

// TestService_GetGrid_MultiPartStatusesAndSort verifies a bin with several parts
// aggregates all of their statuses, and that bins are ordered by grid position.
func TestService_GetGrid_MultiPartStatusesAndSort(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl", IpAddress: "10.0.0.4"})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Drawer", ControllerID: c.ID, SegmentID: 0})

	// Bin at (1,0) with two parts: one ok, one critical.
	b10, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "B10", ContainerID: cont,
		GridX: sql.NullInt64{Int64: 1, Valid: true}, GridY: sql.NullInt64{Int64: 0, Valid: true},
	})
	okPart, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "OK Part", MinStockThreshold: sql.NullInt64{Int64: 1, Valid: true}})
	critPart, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "Crit Part", MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true}})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: okPart, BinID: sql.NullInt64{Int64: b10, Valid: true}, Quantity: 10})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: critPart, BinID: sql.NullInt64{Int64: b10, Valid: true}, Quantity: 0})

	// Bin at (0,0) with one ok part.
	b00, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "B00", ContainerID: cont,
		GridX: sql.NullInt64{Int64: 0, Valid: true}, GridY: sql.NullInt64{Int64: 0, Valid: true},
	})
	okPart2, _ := store.CreatePart(ctx, db.CreatePartParams{Name: "OK Part 2", MinStockThreshold: sql.NullInt64{Int64: 1, Valid: true}})
	_ = store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: okPart2, BinID: sql.NullInt64{Int64: b00, Valid: true}, Quantity: 10})

	grid, err := s.GetGrid(ctx)
	if err != nil {
		t.Fatalf("GetGrid: %v", err)
	}
	bins := grid[0].Containers[0].Bins
	if len(bins) != 2 {
		t.Fatalf("expected 2 bins, got %d", len(bins))
	}
	// Sorted by GridY then GridX: B00 (0,0) before B10 (1,0).
	if bins[0].Name != "B00" || bins[1].Name != "B10" {
		t.Fatalf("unexpected bin order: %q, %q", bins[0].Name, bins[1].Name)
	}
	// B10 aggregates both statuses (sorted alphabetically by GetStatusKeys).
	keys := bins[1].GetStatusKeys()
	if len(keys) != 2 || keys[0] != "critical" || keys[1] != "ok" {
		t.Errorf("expected [critical ok] statuses for B10, got %v", keys)
	}
}

// TestService_GetGrid_ContainerSortBySegment verifies containers are ordered by
// segment then name, independent of insertion order.
func TestService_GetGrid_ContainerSortBySegment(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "Ctrl", IpAddress: "10.0.0.5"})
	// Insert segment 1 before segment 0 to prove the service sorts, not the DB.
	contSeg1, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Seg1", ControllerID: c.ID, SegmentID: 1})
	contSeg0, _ := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Seg0", ControllerID: c.ID, SegmentID: 0})
	for _, cont := range []int64{contSeg1, contSeg0} {
		if _, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "Bin", ContainerID: cont,
			GridX: sql.NullInt64{Int64: 0, Valid: true}, GridY: sql.NullInt64{Int64: 0, Valid: true},
		}); err != nil {
			t.Fatalf("create bin: %v", err)
		}
	}

	grid, err := s.GetGrid(ctx)
	if err != nil {
		t.Fatalf("GetGrid: %v", err)
	}
	if len(grid) != 1 || len(grid[0].Containers) != 2 {
		t.Fatalf("expected 1 controller with 2 containers")
	}
	if grid[0].Containers[0].SegmentID != 0 || grid[0].Containers[1].SegmentID != 1 {
		t.Errorf("expected containers ordered by segment 0 then 1, got %d then %d",
			grid[0].Containers[0].SegmentID, grid[0].Containers[1].SegmentID)
	}
}

// TestService_GetGridByController_EmptyController verifies a controller that
// exists but has no mapped bins is returned (not treated as missing).
func TestService_GetGridByController_EmptyController(t *testing.T) {
	s, store, dbConn := setupTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "Empty", IpAddress: "10.0.0.6"})

	got, err := s.GetGridByController(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetGridByController: %v", err)
	}
	if got == nil {
		t.Fatal("expected a controller, got nil")
	}
	if got.Name != "Empty" {
		t.Errorf("expected name Empty, got %q", got.Name)
	}
	if len(got.Containers) != 0 {
		t.Errorf("expected 0 containers, got %d", len(got.Containers))
	}
}

// TestService_GetGridByController_Nonexistent verifies a missing controller still
// reports sql.ErrNoRows.
func TestService_GetGridByController_Nonexistent(t *testing.T) {
	s, _, dbConn := setupTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	_, err := s.GetGridByController(ctx, 999999)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}
