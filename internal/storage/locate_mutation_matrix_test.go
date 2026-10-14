package storage

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestPhysicalLayoutMutationMatrixRejectsAllOwnedLocateStates(t *testing.T) {
	states := []string{"activating", "active", "stopping", "partial", "stopped with unresolved marker"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			store := newLayoutStore(t)
			service := NewLayoutService(store)

			controllers := make([]db.Controller, 3)
			for i := range controllers {
				created, err := store.CreateController(ctx, db.CreateControllerParams{
					Name: string(rune('A' + i)), IpAddress: "127.0.0.1", Port: sql.NullInt64{Int64: int64(18080 + i), Valid: true},
				})
				if err != nil {
					t.Fatal(err)
				}
				controllers[i], err = store.GetController(ctx, created.ID)
				if err != nil {
					t.Fatal(err)
				}
			}

			cabinetID, err := service.CreateCabinet(ctx, "Mutation matrix cabinet", 0, 50, 30)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Save(ctx, LayoutInput{ID: cabinetID, Name: "Mutation matrix cabinet", CoordinateSystem: "grid", Width: 50, Height: 30, Drawers: []DrawerInput{
				{Label: "Drawer 1", DisplayOrder: 0, X: 0, Y: 0, Width: 20, Height: 20, Bins: []BinInput{}},
				{Label: "Drawer 2", DisplayOrder: 1, X: 25, Y: 0, Width: 20, Height: 20, Bins: []BinInput{}},
			}}); err != nil {
				t.Fatal(err)
			}
			secondCabinetID, err := service.CreateCabinet(ctx, "Second cabinet", 1, 30, 20)
			if err != nil {
				t.Fatal(err)
			}
			if err := service.Save(ctx, LayoutInput{ID: secondCabinetID, Name: "Second cabinet", CoordinateSystem: "grid", Width: 30, Height: 20, Drawers: []DrawerInput{{Label: "Other drawer", Width: 20, Height: 15, Bins: []BinInput{}}}}); err != nil {
				t.Fatal(err)
			}
			for slot, controller := range controllers {
				if err := store.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinetID, ControllerID: controller.ID, Slot: int64(slot)}); err != nil {
					t.Fatal(err)
				}
			}
			layout := mustLayout(t, service, cabinetID)
			wiringSeed := CabinetWiringInput{Revision: layout.Cabinet.ElectricalRevision, ConfiguredControllerIDs: []int64{controllers[0].ID, controllers[1].ID, controllers[2].ID}}
			for slot, controller := range controllers {
				wiringSeed.Strings = append(wiringSeed.Strings, WiringStringInput{
					ControllerID: controller.ID, Name: "String " + string(rune('A'+slot)), DisplayOrder: 0, PixelCount: 100,
					Sections: []WiringSectionInput{{DisplayOrder: 0, PixelCount: 100, Points: []WiringPoint{{X: 1, Y: 1}, {X: 49, Y: 1}}}},
				})
			}
			if err := service.SaveCabinetWiring(ctx, cabinetID, wiringSeed, true); err != nil {
				t.Fatalf("seed configured wiring: %v", err)
			}
			strings, err := store.ListCabinetLEDStrings(ctx, cabinetID)
			if err != nil || len(strings) != 3 {
				t.Fatalf("seed wiring strings=%+v err=%v", strings, err)
			}

			layout = mustLayout(t, service, cabinetID)
			binID, err := service.CreatePhysicalBin(ctx, cabinetID, layout.Drawers[0].Drawer.ID, layout.Cabinet.LayoutRevision, "Stocked bin", 1, 1, 4, 4)
			if err != nil {
				t.Fatal(err)
			}
			partID, err := store.CreatePart(ctx, db.CreatePartParams{Name: "Stocked part"})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}, Quantity: 9}); err != nil {
				t.Fatal(err)
			}
			layout = mustLayout(t, service, cabinetID)
			if _, err := store.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: cabinetID, DrawerID: layout.Drawers[0].Drawer.ID, StartPixel: 25, PixelCount: 8, DisplayOrder: 0}); err != nil {
				t.Fatal(err)
			}
			layout = mustLayout(t, service, cabinetID)

			sessionState := state
			if state == "stopped with unresolved marker" {
				sessionState = "stopped"
			}
			if _, err := store.SavePhysicalLocateSession(ctx, db.SavePhysicalLocateSessionParams{
				ControllerID: controllers[0].ID, ControllerAddress: controllers[0].IpAddress, ControllerPort: 18080,
				SessionID: "layout-matrix-" + state, DrawerID: layout.Drawers[0].Drawer.ID, BinID: binID, OwnerID: 11, State: sessionState,
				RangesJson: `[{"start":25,"count":8}]`, CommandsJson: `[{"segment_id":10,"start":24,"stop":32}]`,
				Fingerprint: "saved-fingerprint", Color: "#ff0000", Brightness: 128,
				LastOperation: "activation", LastOperationAt: time.Now(), LastError: "pending",
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.SavePhysicalLocateSessionTarget(ctx, db.SavePhysicalLocateSessionTargetParams{
				SessionID: "layout-matrix-" + state, ControllerID: controllers[0].ID, ControllerAddress: controllers[0].IpAddress,
				ControllerPort: 18080, RangesJson: `[{"start":25,"count":8}]`, CommandsJson: `[{"segment_id":10,"start":24,"stop":32}]`,
				Fingerprint: "saved-fingerprint", MayHaveReceived: 1, LastError: "pending",
			}); err != nil {
				t.Fatal(err)
			}

			beforeLayout := mustLayout(t, service, cabinetID)
			beforeCabinet := mustLayout(t, service, secondCabinetID)
			beforeMemberships, err := store.ListCabinetControllers(ctx, cabinetID)
			if err != nil {
				t.Fatal(err)
			}
			beforeStrings, err := store.ListCabinetLEDStrings(ctx, cabinetID)
			if err != nil {
				t.Fatal(err)
			}
			beforeSections, err := store.ListCabinetLEDPathSectionsByString(ctx, beforeStrings[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			beforeAllocations, err := store.ListDrawerLEDAllocationsByCabinet(ctx, cabinetID)
			if err != nil {
				t.Fatal(err)
			}
			beforeTarget, err := store.ListPhysicalLocateSessionTargets(ctx, "layout-matrix-"+state)
			if err != nil || len(beforeTarget) != 1 {
				t.Fatalf("saved target snapshot=%+v err=%v", beforeTarget, err)
			}
			assignmentID, err := store.GetAssignmentID(ctx, db.GetAssignmentIDParams{PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}})
			if err != nil {
				t.Fatal(err)
			}
			beforeAssignment, err := store.GetAssignment(ctx, assignmentID)
			if err != nil {
				t.Fatal(err)
			}

			validLayoutEdit := layoutToInput(beforeLayout)
			validLayoutEdit.Name = "Must not rename"
			validLayoutEdit.Drawers[0].Bins[0].X += 1
			validStringEdit := CabinetWiringInput{Revision: beforeLayout.Cabinet.ElectricalRevision, ConfiguredControllerIDs: []int64{controllers[0].ID}, Strings: []WiringStringInput{{ID: beforeStrings[0].ID, ControllerID: controllers[0].ID, Name: "Changed string", DisplayOrder: 0, PixelCount: 100, Sections: []WiringSectionInput{{ID: beforeSections[0].ID, DisplayOrder: 0, PixelCount: 100, Points: []WiringPoint{{X: 1, Y: 1}, {X: 49, Y: 1}}}}}}}
			validPathEdit := CabinetWiringInput{Revision: beforeLayout.Cabinet.ElectricalRevision, ConfiguredControllerIDs: []int64{controllers[0].ID}, Strings: []WiringStringInput{{ID: beforeStrings[0].ID, ControllerID: controllers[0].ID, Name: beforeStrings[0].Name, DisplayOrder: 0, PixelCount: 100, Sections: []WiringSectionInput{{ID: beforeSections[0].ID, DisplayOrder: 0, PixelCount: 100, Points: []WiringPoint{{X: 2, Y: 2}, {X: 48, Y: 2}}}}}}}
			validAllocationEdit := []LEDAllocationInput{{ID: beforeAllocations[0].ID, StartPixel: 40, PixelCount: 8}}
			for _, mutation := range []struct {
				name string
				call func() error
			}{
				{name: "controller reassignment and membership", call: func() error {
					return service.SaveCabinetControllersConfirmed(ctx, cabinetID, beforeLayout.Cabinet.ElectricalRevision, []int64{controllers[0].ID, controllers[1].ID}, true)
				}},
				{name: "controller order", call: func() error {
					return service.SaveCabinetControllersConfirmed(ctx, cabinetID, beforeLayout.Cabinet.ElectricalRevision, []int64{controllers[1].ID, controllers[0].ID, controllers[2].ID}, true)
				}},
				{name: "LED string edit", call: func() error { return service.SaveCabinetWiring(ctx, cabinetID, validStringEdit, true) }},
				{name: "wiring path edit", call: func() error { return service.SaveCabinetWiring(ctx, cabinetID, validPathEdit, true) }},
				{name: "drawer LED allocation", call: func() error {
					return service.SaveDrawerLEDAllocations(ctx, cabinetID, beforeLayout.Drawers[0].Drawer.ID, beforeLayout.Cabinet.ElectricalRevision, validAllocationEdit)
				}},
				{name: "physical bin reposition and layout save", call: func() error { return service.Save(ctx, validLayoutEdit) }},
				{name: "physical bin move", call: func() error {
					return service.MovePhysicalBin(ctx, cabinetID, binID, beforeLayout.Drawers[1].Drawer.ID, beforeLayout.Cabinet.LayoutRevision, 5, 5)
				}},
				{name: "physical bin detach", call: func() error {
					return service.DetachBinPlacement(ctx, cabinetID, binID, beforeLayout.Cabinet.LayoutRevision)
				}},
				{name: "cabinet archive", call: func() error { return service.ArchiveCabinet(ctx, cabinetID, beforeLayout.Cabinet.LayoutRevision) }},
				{name: "cabinet delete", call: func() error { return service.DeleteCabinet(ctx, cabinetID, beforeLayout.Cabinet.LayoutRevision) }},
				{name: "drawer archive", call: func() error {
					return service.ArchiveDrawer(ctx, cabinetID, beforeLayout.Drawers[0].Drawer.ID, beforeLayout.Cabinet.LayoutRevision)
				}},
				{name: "drawer delete", call: func() error {
					return service.DeleteDrawer(ctx, cabinetID, beforeLayout.Drawers[0].Drawer.ID, beforeLayout.Cabinet.LayoutRevision)
				}},
				{name: "create physical bin", call: func() error {
					_, err := service.CreatePhysicalBin(ctx, cabinetID, beforeLayout.Drawers[1].Drawer.ID, beforeLayout.Cabinet.LayoutRevision, "Must not create", 1, 1, 2, 2)
					return err
				}},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					if err := mutation.call(); !errors.Is(err, db.ErrPhysicalLocateActive) {
						t.Fatalf("mutation error=%v, want ErrPhysicalLocateActive", err)
					}
				})
			}

			afterLayout := mustLayout(t, service, cabinetID)
			afterSecondCabinet := mustLayout(t, service, secondCabinetID)
			afterMemberships, err := store.ListCabinetControllers(ctx, cabinetID)
			if err != nil {
				t.Fatal(err)
			}
			afterStrings, err := store.ListCabinetLEDStrings(ctx, cabinetID)
			if err != nil {
				t.Fatal(err)
			}
			afterSections, err := store.ListCabinetLEDPathSectionsByString(ctx, beforeStrings[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			afterAllocations, err := store.ListDrawerLEDAllocationsByCabinet(ctx, cabinetID)
			if err != nil {
				t.Fatal(err)
			}
			afterTarget, err := store.ListPhysicalLocateSessionTargets(ctx, "layout-matrix-"+state)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(beforeLayout, afterLayout) || !reflect.DeepEqual(beforeCabinet, afterSecondCabinet) || !reflect.DeepEqual(beforeMemberships, afterMemberships) || !reflect.DeepEqual(beforeStrings, afterStrings) || !reflect.DeepEqual(beforeSections, afterSections) || !reflect.DeepEqual(beforeAllocations, afterAllocations) || !reflect.DeepEqual(beforeTarget, afterTarget) {
				t.Fatalf("rejected %s mutations changed persisted layout or Locate target", state)
			}
			afterAssignment, err := store.GetAssignment(ctx, assignmentID)
			if err != nil || !reflect.DeepEqual(beforeAssignment, afterAssignment) {
				t.Fatalf("rejected mutations changed inventory quantity/identity: before=%+v after=%+v err=%v", beforeAssignment, afterAssignment, err)
			}
		})
	}
}
