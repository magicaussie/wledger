package pages

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
)

// TestDashboardWallIntegrationSyntheticDemo drives the real query + service +
// template path against a disposable SQLite database seeded with a synthetic
// Wall: one demo controller, two demo containers (one populated, one empty) and
// representative bins. It is an integration test (DB -> dashboard.Service ->
// pages.Dashboard), not a browser test.
//
// The controller uses an RFC5737 TEST-NET address (192.0.2.10) so the fixture
// can never address real hardware. No network or LED calls are made.
func TestDashboardWallIntegrationSyntheticDemo(t *testing.T) {
	ctx := context.Background()

	conn, err := db.Open("file:" + t.TempDir() + "/wall-demo.db")
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate temp db: %v", err)
	}
	store := db.NewStore(conn)
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	// Synthetic controller on an RFC5737 TEST-NET address (never a real device).
	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Demo Controller",
		IpAddress: "192.0.2.10",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	// Container A: populated with representative bins (2x2 grid).
	contA, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:          "Demo Drawer A — A Very Long Container Name For Overflow Testing",
		ControllerID:  ctrl.ID,
		SegmentID:     0,
		ConfigJson:    sql.NullString{String: `{"type":"grid","rows":2,"cols":2}`, Valid: true},
		PositionIndex: 0,
		LedCount:      4,
	})
	if err != nil {
		t.Fatalf("create container A: %v", err)
	}

	// Container B: intentionally empty (no bins) to exercise empty-container handling.
	contB, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:          "Demo Drawer B (empty)",
		ControllerID:  ctrl.ID,
		SegmentID:     1,
		ConfigJson:    sql.NullString{String: `{"type":"grid","rows":2,"cols":2}`, Valid: true},
		PositionIndex: 1,
		LedCount:      4,
	})
	if err != nil {
		t.Fatalf("create container B: %v", err)
	}

	binNames := []string{"R1C1", "R1C2", "R2C1", "A very long bin name that should truncate"}
	for i, name := range binNames {
		if _, err := store.CreateBin(ctx, db.CreateBinParams{
			Name:        name,
			ContainerID: contA,
			Width:       sql.NullInt64{Int64: 1, Valid: true},
			GridX:       sql.NullInt64{Int64: int64(i % 2), Valid: true},
			GridY:       sql.NullInt64{Int64: int64(i / 2), Valid: true},
		}); err != nil {
			t.Fatalf("create bin %q: %v", name, err)
		}
	}

	// One part assigned to a bin so the stock-status path is exercised.
	partID, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:              "Demo Resistor 10k",
		MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true},
		ReorderLevel:      sql.NullInt64{Int64: 10, Valid: true},
	})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	bins, err := store.GetBinsByContainer(ctx, contA)
	if err != nil {
		t.Fatalf("list bins: %v", err)
	}
	if len(bins) == 0 {
		t.Fatal("expected seeded bins")
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   partID,
		BinID:    sql.NullInt64{Int64: bins[0].ID, Valid: true},
		Quantity: 3,
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	// The Wall: two cards, A then B.
	wallID, err := store.CreateWall(ctx, db.CreateWallParams{
		Name:        "Demo Wall",
		Description: sql.NullString{String: "Synthetic demo wall", Valid: true},
	})
	if err != nil {
		t.Fatalf("create wall: %v", err)
	}
	for i, cID := range []int64{contA, contB} {
		if err := store.AddContainerToWall(ctx, db.AddContainerToWallParams{
			WallID:        wallID,
			ContainerID:   cID,
			PositionIndex: int64(i),
		}); err != nil {
			t.Fatalf("add container %d to wall: %v", cID, err)
		}
	}

	// --- Service layer: the empty container must survive the query/grouping. ---
	svc := dashboard.NewService(store)
	walls, err := svc.GetAllWallsWithContainers(ctx)
	if err != nil {
		t.Fatalf("GetAllWallsWithContainers: %v", err)
	}
	if len(walls) != 1 {
		t.Fatalf("expected 1 wall, got %d", len(walls))
	}
	if walls[0].Name != "Demo Wall" {
		t.Errorf("expected wall name %q, got %q", "Demo Wall", walls[0].Name)
	}
	if len(walls[0].Containers) != 2 {
		t.Fatalf("expected 2 containers on the wall, got %d", len(walls[0].Containers))
	}
	if walls[0].Containers[0].ID != contA || walls[0].Containers[1].ID != contB {
		t.Errorf("containers must be ordered by position_index (A then B)")
	}
	if got := len(walls[0].Containers[0].Bins); got != len(binNames) {
		t.Errorf("container A: expected %d bins, got %d", len(binNames), got)
	}
	if got := len(walls[0].Containers[1].Bins); got != 0 {
		t.Errorf("container B must be empty, got %d bins", got)
	}

	// --- Template layer: the full dashboard page renders the wall. ---
	stats, err := svc.GetStats(ctx)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	user := auth.User{ID: 1, Email: "demo@example.test", Role: "admin"}

	var buf bytes.Buffer
	if err := Dashboard(user, stats, walls, nil).Render(ctx, &buf); err != nil {
		t.Fatalf("render dashboard: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Demo Wall",
		"Synthetic demo wall",
		"Demo Drawer A — A Very Long Container Name For Overflow Testing",
		"Demo Drawer B (empty)",
		"R1C1",
		"A very long bin name that should truncate",
		`aria-haspopup="dialog"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard output missing %q", want)
		}
	}
	if got := strings.Count(out, `x-ref="modal"`); got != 2 {
		t.Errorf("expected 2 scoped modals, got %d", got)
	}
	if strings.Contains(out, "container_modal_") {
		t.Errorf("modals must not use a global container_modal_ id")
	}
	// No localizer is attached to the render context, so i18n renders the key.
	if !strings.Contains(out, "NoBinsMapped") {
		t.Errorf("expected the empty-container state for container B")
	}
	// The synthetic TEST-NET address must never leak into the rendered page.
	if strings.Contains(out, "192.0.2.10") {
		t.Errorf("controller IP address must not be rendered on the dashboard")
	}
}
