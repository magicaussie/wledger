package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestSaveCabinetWiringStoresOrderedSerpentineSectionsAndDerivesCapacity(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 1, PixelCount: 10}}); err != nil {
		t.Fatal(err)
	}
	input := CabinetWiringInput{
		Revision:                1,
		ConfiguredControllerIDs: []int64{1},
		Strings: []WiringStringInput{
			{
				ControllerID: 1, Name: "Four-row serpentine", DisplayOrder: 0, PixelCount: 100, LayoutComplete: true,
				Sections: []WiringSectionInput{
					{DisplayOrder: 0, PixelCount: 25, Points: []WiringPoint{{X: 0, Y: 2}, {X: 40, Y: 2}}},
					{DisplayOrder: 1, PixelCount: 25, Points: []WiringPoint{{X: 40, Y: 4}, {X: 0, Y: 4}}},
					{DisplayOrder: 2, PixelCount: 25, Points: []WiringPoint{{X: 0, Y: 6}, {X: 40, Y: 6}}},
					{DisplayOrder: 3, PixelCount: 25, Points: []WiringPoint{{X: 40, Y: 8}, {X: 0, Y: 8}}},
				},
			},
			{ControllerID: 1, Name: "Unrouted spare", DisplayOrder: 1, PixelCount: 100, LayoutComplete: false, Sections: []WiringSectionInput{}},
		},
	}
	if err := service.SaveCabinetWiring(ctx, cabinetID, input, false); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("wiring change with saved mapping = %v, want explicit-impact confirmation", err)
	}
	if err := service.SaveCabinetWiring(ctx, cabinetID, input, true); err != nil {
		t.Fatalf("confirmed wiring save: %v", err)
	}
	controller, err := store.GetController(ctx, 1)
	if err != nil || controller.LedCount != 200 {
		t.Fatalf("derived controller capacity=%d err=%v, want 200", controller.LedCount, err)
	}
	layout, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.Wiring.Controllers) != 1 || len(layout.Wiring.Controllers[0].Strings) != 2 {
		t.Fatalf("saved wiring strings = %+v", layout.Wiring.Controllers)
	}
	first := layout.Wiring.Controllers[0].Strings[0]
	if first.Record.PixelCount != 100 || first.Record.LayoutComplete != 1 || len(first.Sections) != 4 {
		t.Fatalf("serpentine string/path sections = %+v", first)
	}
	if first.Sections[1].PointsJson != `[{"x":40,"y":4},{"x":0,"y":4}]` {
		t.Fatalf("reverse-direction section points=%s", first.Sections[1].PointsJson)
	}
	rows, err := store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil || len(rows) != 1 || rows[0].StartPixel != 1 || rows[0].PixelCount != 10 {
		t.Fatalf("wiring update changed drawer mapping: %+v err=%v", rows, err)
	}
}

func TestCabinetWiringCapacityChangeNeedsPreviewConfirmation(t *testing.T) {
	service, store, cabinetID, drawerOne := ledAllocationFixture(t)
	ctx := context.Background()
	cabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	var secondController int64
	var drawerTwo int64
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		controller, err := q.CreateController(ctx, db.CreateControllerParams{Name: "Second", IpAddress: "192.0.2.222"})
		if err != nil {
			return err
		}
		secondController = controller.ID
		if err := q.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{IsOnline: sql.NullBool{Bool: false, Valid: true}, LedCount: 100, ID: secondController}); err != nil {
			return err
		}
		drawerTwo, err = q.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer Two", DisplayOrder: 1, X: 12, Width: 10, Height: 10})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveCabinetControllers(ctx, cabinetID, cabinet.ElectricalRevision, []int64{1, secondController}); err != nil {
		t.Fatal(err)
	}
	cabinet, err = store.GetCabinet(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerOne, cabinet.ElectricalRevision, []LEDAllocationInput{{StartPixel: 10, PixelCount: 5}}); err != nil {
		t.Fatal(err)
	}
	cabinet, _ = store.GetCabinet(ctx, cabinetID)
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerTwo, cabinet.ElectricalRevision, []LEDAllocationInput{{StartPixel: 250, PixelCount: 10}}); err != nil {
		t.Fatal(err)
	}
	cabinet, _ = store.GetCabinet(ctx, cabinetID)
	input := CabinetWiringInput{
		Revision:                cabinet.ElectricalRevision,
		ConfiguredControllerIDs: []int64{1, secondController},
		Strings: []WiringStringInput{
			{ControllerID: 1, Name: "Primary string", DisplayOrder: 0, PixelCount: 150},
			{ControllerID: secondController, Name: "Secondary string", DisplayOrder: 0, PixelCount: 150},
		},
	}
	if err := service.SaveCabinetWiring(ctx, cabinetID, input, false); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("capacity change that shifts a saved range = %v, want confirmation requirement", err)
	}
	if err := service.SaveCabinetWiring(ctx, cabinetID, input, true); err != nil {
		t.Fatalf("confirmed capacity change: %v", err)
	}
	cabinet, _ = store.GetCabinet(ctx, cabinetID)
	if cabinet.ElectricalRevision != input.Revision+1 {
		t.Fatalf("electrical revision=%d, want %d", cabinet.ElectricalRevision, input.Revision+1)
	}
	rows, err := store.ListDrawerLEDAllocationsByDrawer(ctx, drawerTwo)
	if err != nil || len(rows) != 1 || rows[0].StartPixel != 250 {
		t.Fatalf("wiring edit changed cabinet-wide allocation: %+v err=%v", rows, err)
	}
}

func TestCabinetWiringRejectsIncompletePixelAccountingWhenMarkedComplete(t *testing.T) {
	service, _, cabinetID, _ := ledAllocationFixture(t)
	input := CabinetWiringInput{
		Revision:                0,
		ConfiguredControllerIDs: []int64{1},
		Strings: []WiringStringInput{{
			ControllerID: 1, Name: "Bad complete string", DisplayOrder: 0, PixelCount: 100, LayoutComplete: true,
			Sections: []WiringSectionInput{{DisplayOrder: 0, PixelCount: 99, Points: []WiringPoint{{X: 0, Y: 0}, {X: 1, Y: 1}}}},
		}},
	}
	if err := service.SaveCabinetWiring(context.Background(), cabinetID, input, false); !errors.Is(err, ErrLayoutInvalid) {
		t.Fatalf("completed path with 99 of 100 pixels = %v, want invalid-layout rejection", err)
	}
}
