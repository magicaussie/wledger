package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestPhysicalBinLifecycleIsIndependentAndPreservesInventory(t *testing.T) {
	ctx := context.Background()
	store := newLayoutStore(t)
	service := NewLayoutService(store)

	cabinetID, err := service.CreateCabinet(ctx, "Physical first", 0, 40, 20)
	if err != nil {
		t.Fatalf("create controller-free cabinet: %v", err)
	}
	controllers, err := store.GetControllers(ctx)
	if err != nil || len(controllers) != 0 {
		t.Fatalf("physical cabinet setup should not require any WLED controllers: %#v err=%v", controllers, err)
	}
	if err := service.Save(ctx, LayoutInput{
		ID: cabinetID, Revision: 0, Name: "Physical first", CoordinateSystem: "grid", Width: 40, Height: 20,
		Drawers: []DrawerInput{
			{Label: "Drawer One", DisplayOrder: 0, X: 0, Y: 0, Width: 12, Height: 10, Bins: []BinInput{}},
			{Label: "Drawer Two", DisplayOrder: 1, X: 20, Y: 0, Width: 12, Height: 10, Bins: []BinInput{}},
		},
	}); err != nil {
		t.Fatalf("create drawers without controllers or wiring: %v", err)
	}
	layout, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.AssignedControllers) != 0 || len(layout.Wiring.Controllers) != 0 || len(layout.Drawers) != 2 {
		t.Fatalf("unexpected electrical prerequisite/state: %#v", layout)
	}
	firstDrawer, secondDrawer := layout.Drawers[0].Drawer, layout.Drawers[1].Drawer

	binID, err := service.CreatePhysicalBin(ctx, cabinetID, firstDrawer.ID, layout.Cabinet.LayoutRevision, "Physical Bin A", 1, 1, 4, 4)
	if err != nil {
		t.Fatalf("create a physical bin without electrical mapping: %v", err)
	}
	bin, err := store.GetBin(ctx, binID)
	if err != nil {
		t.Fatal(err)
	}
	if bin.ID != binID || bin.Name != "Physical Bin A" || bin.ContainerID.Valid || bin.LedIndex.Valid || bin.Width.Valid || bin.GridX.Valid || bin.GridY.Valid {
		t.Fatalf("new physical bin should have no legacy electrical association: %+v", bin)
	}

	partID, err := store.CreatePart(ctx, db.CreatePartParams{Name: "Unwired inventory part"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}, Quantity: 7}); err != nil {
		t.Fatalf("assign stock to an LED-free bin: %v", err)
	}
	secondID, err := service.CreatePhysicalBin(ctx, cabinetID, secondDrawer.ID, layout.Cabinet.LayoutRevision+1, "Physical Bin B", 1, 1, 4, 4)
	if err != nil {
		t.Fatalf("create second bin: %v", err)
	}

	layout, err = service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	input := layoutToInput(layout)
	input.Drawers[0].Bins[0].Name = "Renamed Bin A"
	input.Drawers[0].Bins[0].X = 2
	input.Drawers[0].Bins[0].Y = 2
	if err := service.Save(ctx, input); err != nil {
		t.Fatalf("rename and reposition bin in the existing editor: %v", err)
	}
	layout, err = service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Drawers[0].Bins[0].BinID != binID || layout.Drawers[0].Bins[0].Name != "Renamed Bin A" || layout.Drawers[0].Bins[0].X != 2 {
		t.Fatalf("rename/reposition replaced or lost bin identity: %+v", layout.Drawers[0].Bins)
	}

	revision := layout.Cabinet.LayoutRevision
	if err := service.MovePhysicalBin(ctx, cabinetID, binID, secondDrawer.ID, revision, 1, 1); !errors.Is(err, ErrLayoutInvalid) {
		t.Fatalf("overlapping move error = %v, want ErrLayoutInvalid", err)
	}
	unchanged, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Cabinet.LayoutRevision != revision || unchanged.Drawers[0].Bins[0].BinID != binID {
		t.Fatalf("rejected move partially changed physical layout: %#v", unchanged)
	}
	if err := service.MovePhysicalBin(ctx, cabinetID, binID, secondDrawer.ID, revision, 6, 1); err != nil {
		t.Fatalf("move existing bin without replacing it: %v", err)
	}
	moved, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Drawers[0].Bins) != 0 || len(moved.Drawers[1].Bins) != 2 || moved.Drawers[1].Bins[1].BinID != binID {
		t.Fatalf("move did not preserve the bin identity: %#v", moved.Drawers)
	}
	assignments, err := store.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 1 || assignments[0].BinID.Int64 != binID || assignments[0].Quantity != 7 {
		t.Fatalf("move changed stock identity or quantity: %+v err=%v", assignments, err)
	}
	if secondID == binID {
		t.Fatal("separate physical bins unexpectedly share an identity")
	}

	if err := service.DetachBinPlacement(ctx, cabinetID, binID, moved.Cabinet.LayoutRevision); err != nil {
		t.Fatalf("detach placement without deleting inventory: %v", err)
	}
	detached, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	foundUnplaced := false
	for _, loose := range detached.UnattachedBins {
		foundUnplaced = foundUnplaced || loose.ID == binID
	}
	if !foundUnplaced {
		t.Fatalf("detached bin %d is not available to place again: %+v", binID, detached.UnattachedBins)
	}
	assignments, err = store.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 1 || assignments[0].Quantity != 7 {
		t.Fatalf("detach deleted assigned stock: %+v err=%v", assignments, err)
	}
}

func TestCreatePhysicalBinRejectsInvalidAndDuplicatePositionsTransactionally(t *testing.T) {
	ctx := context.Background()
	store := newLayoutStore(t)
	service := NewLayoutService(store)
	cabinetID, err := service.CreateCabinet(ctx, "Placement validation", 0, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Save(ctx, LayoutInput{ID: cabinetID, Name: "Placement validation", CoordinateSystem: "grid", Width: 20, Height: 20, Drawers: []DrawerInput{{Label: "Drawer", Width: 10, Height: 10, Bins: []BinInput{}}}}); err != nil {
		t.Fatal(err)
	}
	layout, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	drawerID := layout.Drawers[0].Drawer.ID
	if _, err := service.CreatePhysicalBin(ctx, cabinetID, drawerID, layout.Cabinet.LayoutRevision, "First", 1, 1, 4, 4); err != nil {
		t.Fatal(err)
	}
	layout, _ = service.Get(ctx, cabinetID)
	revision := layout.Cabinet.LayoutRevision
	for _, tc := range []struct {
		name       string
		x, y, w, h float64
	}{
		{name: "overlap", x: 2, y: 2, w: 3, h: 3},
		{name: "out of bounds", x: 9, y: 9, w: 2, h: 2},
		{name: "invalid size", x: 1, y: 1, w: 0, h: 2},
	} {
		if _, err := service.CreatePhysicalBin(ctx, cabinetID, drawerID, revision, tc.name, tc.x, tc.y, tc.w, tc.h); !errors.Is(err, ErrLayoutInvalid) {
			t.Errorf("%s: error = %v, want ErrLayoutInvalid", tc.name, err)
		}
	}
	unchanged, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Cabinet.LayoutRevision != revision || len(unchanged.Drawers[0].Bins) != 1 {
		t.Fatalf("invalid creates partially committed: %#v", unchanged)
	}
}

func TestLayoutSaveRollsBackWhenBinIsAttachedElsewhere(t *testing.T) {
	ctx := context.Background()
	store := newLayoutStore(t)
	service := NewLayoutService(store)
	firstID, err := service.CreateCabinet(ctx, "First", 0, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := service.CreateCabinet(ctx, "Second", 1, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, cabinetID := range []int64{firstID, secondID} {
		cabinet, err := service.Get(ctx, cabinetID)
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Save(ctx, LayoutInput{ID: cabinetID, Revision: cabinet.Cabinet.LayoutRevision, Name: cabinet.Cabinet.Name, CoordinateSystem: "grid", Width: 20, Height: 20, Drawers: []DrawerInput{{Label: "Drawer", Width: 10, Height: 10, Bins: []BinInput{}}}}); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := service.Get(ctx, firstID)
	second, _ := service.Get(ctx, secondID)
	foreignDrawerID := second.Drawers[0].Drawer.ID
	foreignBinID, err := service.CreatePhysicalBin(ctx, secondID, foreignDrawerID, second.Cabinet.LayoutRevision, "Foreign bin", 1, 1, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	first = mustLayout(t, service, firstID)
	input := layoutToInput(first)
	input.Name = "Must roll back"
	input.Drawers[0].Label = "Must roll back drawer"
	input.Drawers[0].Bins = []BinInput{{ID: foreignBinID, Name: "Must not rename", X: 1, Y: 1, Width: 3, Height: 3}}
	if err := service.Save(ctx, input); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("foreign attached bin error = %v, want ErrUnsafeLayout", err)
	}
	first = mustLayout(t, service, firstID)
	foreign, err := store.GetBin(ctx, foreignBinID)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cabinet.Name != "First" || first.Drawers[0].Drawer.Label != "Drawer" || first.Cabinet.LayoutRevision != 1 || foreign.Name != "Foreign bin" {
		t.Fatalf("invalid attachment partially committed: cabinet=%+v drawer=%+v bin=%+v", first.Cabinet, first.Drawers[0].Drawer, foreign)
	}
	attachment, err := store.GetBinDrawerAttachment(ctx, foreignBinID)
	if err != nil || attachment.DrawerID != foreignDrawerID {
		t.Fatalf("foreign bin was relocated: %+v err=%v", attachment, err)
	}
}

func TestPhysicalBinMutationsRejectActiveAndUnresolvedLocateOwnership(t *testing.T) {
	for _, tc := range []struct {
		name            string
		state           string
		mayHaveReceived int64
	}{
		{name: "active session", state: "active"},
		{name: "unresolved cleanup target", state: "stopped", mayHaveReceived: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := newLayoutStore(t)
			service := NewLayoutService(store)
			cabinetID, err := service.CreateCabinet(ctx, "Located cabinet", 0, 30, 20)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Save(ctx, LayoutInput{ID: cabinetID, Name: "Located cabinet", CoordinateSystem: "grid", Width: 30, Height: 20, Drawers: []DrawerInput{
				{Label: "Source", DisplayOrder: 0, X: 0, Y: 0, Width: 10, Height: 10, Bins: []BinInput{}},
				{Label: "Target", DisplayOrder: 1, X: 15, Y: 0, Width: 10, Height: 10, Bins: []BinInput{}},
			}}); err != nil {
				t.Fatal(err)
			}
			layout := mustLayout(t, service, cabinetID)
			binID, err := service.CreatePhysicalBin(ctx, cabinetID, layout.Drawers[0].Drawer.ID, layout.Cabinet.LayoutRevision, "Located bin", 1, 1, 3, 3)
			if err != nil {
				t.Fatal(err)
			}
			controller, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Unrelated controller", IpAddress: "192.0.2.200"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.SavePhysicalLocateSession(ctx, db.SavePhysicalLocateSessionParams{
				ControllerID: controller.ID, ControllerAddress: controller.IpAddress, ControllerPort: 80,
				SessionID: "layout-safety", DrawerID: 1, BinID: 1, OwnerID: 1, State: tc.state,
				RangesJson: `[]`, CommandsJson: `[]`, Fingerprint: "fingerprint", Color: "#ff0000", Brightness: 128,
				LastOperation: "activation", LastOperationAt: time.Now(), LastError: "pending",
			}); err != nil {
				t.Fatal(err)
			}
			if tc.mayHaveReceived != 0 {
				if err := store.SavePhysicalLocateSessionTarget(ctx, db.SavePhysicalLocateSessionTargetParams{
					SessionID: "layout-safety", ControllerID: controller.ID, ControllerAddress: controller.IpAddress,
					ControllerPort: 80, RangesJson: `[]`, CommandsJson: `[]`, Fingerprint: "fingerprint",
					MayHaveReceived: tc.mayHaveReceived, LastError: "cleanup pending",
				}); err != nil {
					t.Fatal(err)
				}
			}
			layout = mustLayout(t, service, cabinetID)
			revision := layout.Cabinet.LayoutRevision
			if _, err := service.CreatePhysicalBin(ctx, cabinetID, layout.Drawers[1].Drawer.ID, revision, "Blocked", 1, 1, 2, 2); !errors.Is(err, db.ErrPhysicalLocateActive) {
				t.Errorf("create during Locate = %v, want ErrPhysicalLocateActive", err)
			}
			if err := service.MovePhysicalBin(ctx, cabinetID, binID, layout.Drawers[1].Drawer.ID, revision, 5, 1); !errors.Is(err, db.ErrPhysicalLocateActive) {
				t.Errorf("move during Locate = %v, want ErrPhysicalLocateActive", err)
			}
			if err := service.DetachBinPlacement(ctx, cabinetID, binID, revision); !errors.Is(err, db.ErrPhysicalLocateActive) {
				t.Errorf("detach during Locate = %v, want ErrPhysicalLocateActive", err)
			}
			input := layoutToInput(layout)
			input.Drawers[0].Bins[0].X = 2
			if err := service.Save(ctx, input); !errors.Is(err, db.ErrPhysicalLocateActive) {
				t.Errorf("layout save during Locate = %v, want ErrPhysicalLocateActive", err)
			}
			final := mustLayout(t, service, cabinetID)
			if final.Cabinet.LayoutRevision != revision || len(final.Drawers[0].Bins) != 1 || len(final.Drawers[1].Bins) != 0 {
				t.Fatalf("Locate-blocked physical writes changed layout: %#v", final)
			}
		})
	}
}

func layoutToInput(layout CabinetLayout) LayoutInput {
	input := LayoutInput{
		ID: layout.Cabinet.ID, Revision: layout.Cabinet.LayoutRevision, Name: layout.Cabinet.Name,
		CoordinateSystem: layout.Cabinet.CoordinateSystem, DisplayOrder: layout.Cabinet.DisplayOrder,
		Width: layout.Cabinet.LayoutWidth, Height: layout.Cabinet.LayoutHeight,
	}
	for _, drawer := range layout.Drawers {
		drawerInput := DrawerInput{
			ID: drawer.Drawer.ID, Label: drawer.Drawer.Label, DisplayOrder: drawer.Drawer.DisplayOrder,
			X: drawer.Drawer.X, Y: drawer.Drawer.Y, Width: drawer.Drawer.Width, Height: drawer.Drawer.Height,
			Bins: make([]BinInput, 0, len(drawer.Bins)),
		}
		for _, bin := range drawer.Bins {
			drawerInput.Bins = append(drawerInput.Bins, BinInput{
				ID: bin.BinID, Name: bin.Name, DisplayOrder: bin.DisplayOrder,
				X: bin.X, Y: bin.Y, Width: bin.Width, Height: bin.Height,
			})
		}
		input.Drawers = append(input.Drawers, drawerInput)
	}
	return input
}

func mustLayout(t *testing.T, service *LayoutService, cabinetID int64) CabinetLayout {
	t.Helper()
	layout, err := service.Get(context.Background(), cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	return layout
}
