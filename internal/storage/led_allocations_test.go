package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func ledAllocationFixture(t *testing.T) (*LayoutService, db.Store, int64, int64) {
	t.Helper()
	store := newLayoutStore(t)
	ctx := context.Background()
	cabinetID, err := NewLayoutService(store).CreateCabinet(ctx, "LED cabinet", 0, 40, 40)
	if err != nil {
		t.Fatal(err)
	}
	var controllerID, drawerID int64
	err = store.ExecImmediateTx(ctx, func(q db.Querier) error {
		controller, err := q.CreateController(ctx, db.CreateControllerParams{Name: "Two-string test controller", IpAddress: "192.0.2.200", Port: sql.NullInt64{Int64: 80, Valid: true}})
		if err != nil {
			return err
		}
		controllerID = controller.ID
		if err := q.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{IsOnline: sql.NullBool{Bool: true, Valid: true}, LedCount: 200, ID: controllerID}); err != nil {
			return err
		}
		if _, err := q.UpdateCabinet(ctx, db.UpdateCabinetParams{Name: "LED cabinet", ControllerID: sql.NullInt64{Int64: controllerID, Valid: true}, CoordinateSystem: "grid", ID: cabinetID}); err != nil {
			return err
		}
		if err := q.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinetID, ControllerID: controllerID, Slot: 0}); err != nil {
			return err
		}
		drawerID, err = q.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer One", Width: 10, Height: 10})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewLayoutService(store), store, cabinetID, drawerID
}

func TestCabinetWideAllocationsCrossControllerBoundariesAndOverlapGlobally(t *testing.T) {
	service, store, cabinetID, drawerOne := ledAllocationFixture(t)
	ctx := context.Background()
	cabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	primaryID := cabinet.ControllerID.Int64
	var secondaryID, drawerTwo int64
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		secondary, err := q.CreateController(ctx, db.CreateControllerParams{Name: "Secondary controller", IpAddress: "192.0.2.201"})
		if err != nil {
			return err
		}
		secondaryID = secondary.ID
		if err := q.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{IsOnline: sql.NullBool{Bool: false, Valid: true}, LedCount: 100, ID: secondaryID}); err != nil {
			return err
		}
		drawerTwo, err = q.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer Two", DisplayOrder: 1, X: 12, Width: 10, Height: 10})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveCabinetControllers(ctx, cabinetID, cabinet.ElectricalRevision, []int64{primaryID, secondaryID}); err != nil {
		t.Fatalf("assign second controller: %v", err)
	}
	cabinet, err = store.GetCabinet(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerOne, cabinet.ElectricalRevision, []LEDAllocationInput{{StartPixel: 195, PixelCount: 16}}); err != nil {
		t.Fatalf("save a drawer range spanning controllers: %v", err)
	}
	cabinet, err = store.GetCabinet(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerTwo, cabinet.ElectricalRevision, []LEDAllocationInput{{StartPixel: 201, PixelCount: 10}}); !errors.Is(err, ErrLEDAllocationInvalid) {
		t.Fatalf("overlap on controller 2's cabinet-wide pixels = %v, want invalid allocation", err)
	}
	if err := service.SaveControllerLEDCapacity(ctx, secondaryID, 100, 9, cabinet.ElectricalRevision); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("secondary controller capacity shrink invalidating global ranges = %v, want unsafe-layout rejection", err)
	}
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		_, err := q.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: cabinetID, DrawerID: drawerTwo, StartPixel: 200, PixelCount: 2})
		return err
	}); err == nil {
		t.Fatal("database accepted overlapping ranges in cabinet-wide pixel space")
	}
}

func TestDrawerLEDAllocationGlobalIndexCrossesPhysicalStringBoundary(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()

	// A 200-pixel controller with a string boundary at pixel 100 (for example,
	// two 100-pixel strings) uses one global address space. The 97–104 range
	// remains valid and is not split at that boundary.
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 97, PixelCount: 8}}); err != nil {
		t.Fatalf("save range crossing pixel 100: %v", err)
	}
	layout, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if got := layout.Drawers[0].LedAllocations; len(got) != 1 || got[0].StartPixel != 97 || got[0].PixelCount != 8 {
		t.Fatalf("saved allocation did not reload exactly: %#v", got)
	}
	if end := layout.Drawers[0].LedAllocations[0].StartPixel + layout.Drawers[0].LedAllocations[0].PixelCount - 1; end != 104 {
		t.Fatalf("controller-wide inclusive end = %d, want 104", end)
	}
	cabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil || cabinet.ElectricalRevision != 1 {
		t.Fatalf("electrical revision = %d, err=%v; want 1", cabinet.ElectricalRevision, err)
	}

	if err := service.Save(ctx, LayoutInput{ID: cabinetID, Revision: cabinet.LayoutRevision, Name: "LED cabinet", CoordinateSystem: "grid", Width: 40, Height: 40,
		Drawers: []DrawerInput{{ID: drawerID, Label: "Moved drawer", DisplayOrder: 0, X: 4, Y: 3, Width: 10, Height: 10, Bins: []BinInput{}}}}); err != nil {
		t.Fatalf("move the drawer without changing its electrical allocation: %v", err)
	}
	moved, err := service.Get(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved.Drawers) != 1 || moved.Drawers[0].Drawer.X != 4 || len(moved.Drawers[0].LedAllocations) != 1 || moved.Drawers[0].LedAllocations[0].StartPixel != 97 || moved.Drawers[0].LedAllocations[0].PixelCount != 8 {
		t.Fatalf("physical movement changed the persisted electrical allocation: %#v", moved.Drawers)
	}
}

func TestDrawerLEDAllocationMultipleOrderedRangesAndValidation(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 97, PixelCount: 8}, {StartPixel: 150, PixelCount: 4}}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].DisplayOrder != 0 || rows[1].DisplayOrder != 1 || rows[0].ID == rows[1].ID {
		t.Fatalf("ranges lack stable deterministic order: %#v", rows)
	}
	stableIDs := []int64{rows[0].ID, rows[1].ID}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 1, []LEDAllocationInput{{ID: rows[0].ID, StartPixel: 97, PixelCount: 8}, {ID: rows[1].ID, StartPixel: 150, PixelCount: 4}}); err != nil {
		t.Fatalf("unchanged allocations should remain writable: %v", err)
	}
	rows, err = store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil || rows[0].ID != stableIDs[0] || rows[1].ID != stableIDs[1] {
		t.Fatalf("editing changed stable allocation identities: %#v, err=%v", rows, err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 2, []LEDAllocationInput{{ID: rows[0].ID, StartPixel: 97, PixelCount: 8}, {ID: rows[1].ID, StartPixel: 104, PixelCount: 3}}); !errors.Is(err, ErrLEDAllocationInvalid) {
		t.Fatalf("overlapping same-drawer ranges: %v", err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 2, []LEDAllocationInput{{StartPixel: 200, PixelCount: 2}}); !errors.Is(err, ErrLEDAllocationInvalid) {
		t.Fatalf("capacity overflow: %v", err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 2, []LEDAllocationInput{{StartPixel: 0, PixelCount: 1}}); !errors.Is(err, ErrLEDAllocationInvalid) {
		t.Fatalf("zero-based start: %v", err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 2, []LEDAllocationInput{{StartPixel: 1, PixelCount: 0}}); !errors.Is(err, ErrLEDAllocationInvalid) {
		t.Fatalf("zero count: %v", err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 2, []LEDAllocationInput{{StartPixel: 2, PixelCount: math.MaxInt64}}); !errors.Is(err, ErrLEDAllocationInvalid) {
		t.Fatalf("overflowing end: %v", err)
	}
	rows, err = store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil || len(rows) != 2 || rows[0].ID != stableIDs[0] || rows[1].ID != stableIDs[1] {
		t.Fatalf("invalid writes did not roll back/preserve allocations: %#v, err=%v", rows, err)
	}
}

type failingElectricalRevisionStore struct{ db.Store }

type failingElectricalRevisionQuerier struct{ db.Querier }

func (s failingElectricalRevisionStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	return s.Store.ExecImmediateTx(ctx, func(q db.Querier) error {
		return fn(failingElectricalRevisionQuerier{Querier: q})
	})
}

func (failingElectricalRevisionQuerier) AdvanceCabinetElectricalRevision(context.Context, db.AdvanceCabinetElectricalRevisionParams) (int64, error) {
	return 0, errors.New("injected electrical revision failure")
}

func TestDrawerLEDAllocationRevisionFailureRollsBackRangeChanges(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 10, PixelCount: 5}, {StartPixel: 30, PixelCount: 5}}); err != nil {
		t.Fatal(err)
	}
	before, err := store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil {
		t.Fatal(err)
	}
	failingService := NewLayoutService(failingElectricalRevisionStore{Store: store})
	err = failingService.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 1, []LEDAllocationInput{{ID: before[0].ID, StartPixel: 50, PixelCount: 5}, {StartPixel: 70, PixelCount: 5}})
	if err == nil {
		t.Fatal("injected revision failure should abort the allocation transaction")
	}
	after, err := store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) || after[0].ID != before[0].ID || after[0].StartPixel != before[0].StartPixel || after[1].ID != before[1].ID || after[1].StartPixel != before[1].StartPixel {
		t.Fatalf("allocation rows escaped a rolled-back transaction: before=%#v after=%#v", before, after)
	}
	cabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil || cabinet.ElectricalRevision != 1 {
		t.Fatalf("revision escaped a rolled-back transaction: revision=%d err=%v", cabinet.ElectricalRevision, err)
	}
}

func TestCabinetControllerAssignmentIsRevisionAndAllocationSafe(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()
	var secondControllerID int64
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		controller, err := q.CreateController(ctx, db.CreateControllerParams{Name: "Second controller", IpAddress: "192.0.2.201"})
		if err != nil {
			return err
		}
		secondControllerID = controller.ID
		return q.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{IsOnline: sql.NullBool{Bool: false, Valid: true}, LedCount: 100, ID: secondControllerID})
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveCabinetController(ctx, cabinetID, 0, secondControllerID); err != nil {
		t.Fatalf("assign controller: %v", err)
	}
	if err := service.SaveCabinetController(ctx, cabinetID, 0, 1); !errors.Is(err, ErrLEDAllocationStale) {
		t.Fatalf("stale controller reassignment: %v", err)
	}
	cabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil || !cabinet.ControllerID.Valid || cabinet.ControllerID.Int64 != secondControllerID || cabinet.ElectricalRevision != 1 {
		t.Fatalf("controller assignment/revision not saved: cabinet=%#v err=%v", cabinet, err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 1, []LEDAllocationInput{{StartPixel: 97, PixelCount: 4}}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveCabinetController(ctx, cabinetID, 2, 1); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("controller reassignment with saved allocation: %v", err)
	}
}

func TestControllerOrderChangeRequiresExplicitMappingImpactConfirmation(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()
	var secondControllerID int64
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		controller, err := q.CreateController(ctx, db.CreateControllerParams{Name: "Second ordered controller", IpAddress: "192.0.2.233"})
		if err != nil {
			return err
		}
		secondControllerID = controller.ID
		return q.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{IsOnline: sql.NullBool{Bool: false, Valid: true}, LedCount: 100, ID: secondControllerID})
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveCabinetControllers(ctx, cabinetID, 0, []int64{1, secondControllerID}); err != nil {
		t.Fatal(err)
	}
	cabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, cabinet.ElectricalRevision, []LEDAllocationInput{{StartPixel: 205, PixelCount: 10}}); err != nil {
		t.Fatal(err)
	}
	cabinet, _ = store.GetCabinet(ctx, cabinetID)
	if err := service.SaveCabinetControllersConfirmed(ctx, cabinetID, cabinet.ElectricalRevision, []int64{secondControllerID, 1}, false); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("controller reorder without impact confirmation = %v, want ErrUnsafeLayout", err)
	}
	if err := service.SaveCabinetControllersConfirmed(ctx, cabinetID, cabinet.ElectricalRevision, []int64{secondControllerID, 1}, true); err != nil {
		t.Fatalf("confirmed controller reorder: %v", err)
	}
	members, err := store.ListCabinetControllers(ctx, cabinetID)
	if err != nil || len(members) != 2 || members[0].ControllerID != secondControllerID || members[0].Slot != 0 || members[1].ControllerID != 1 || members[1].Slot != 1 {
		t.Fatalf("saved controller order=%+v err=%v", members, err)
	}
	allocations, err := store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil || len(allocations) != 1 || allocations[0].StartPixel != 205 {
		t.Fatalf("controller order change rewrote cabinet-wide allocation: %+v err=%v", allocations, err)
	}
}

func TestControllerCapacityChangeIsRevisionSafeAndPreservesAllocations(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 97, PixelCount: 8}}); err != nil {
		t.Fatal(err)
	}
	cabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil {
		t.Fatal(err)
	}
	controllerID := cabinet.ControllerID.Int64
	if err := service.SaveControllerLEDCapacity(ctx, controllerID, 200, 103, cabinet.ElectricalRevision); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("capacity reduction that would invalidate a saved range: %v", err)
	}
	if err := service.SaveControllerLEDCapacity(ctx, controllerID, 200, 250, cabinet.ElectricalRevision); err != nil {
		t.Fatalf("increase controller-wide capacity: %v", err)
	}
	if err := service.SaveControllerLEDCapacity(ctx, controllerID, 200, 300, cabinet.ElectricalRevision); !errors.Is(err, ErrLEDControllerCapacityStale) {
		t.Fatalf("stale controller capacity write: %v", err)
	}
	updatedController, err := store.GetController(ctx, controllerID)
	if err != nil || updatedController.LedCount != 250 {
		t.Fatalf("controller capacity changed unexpectedly: capacity=%d err=%v", updatedController.LedCount, err)
	}
	updatedCabinet, err := store.GetCabinet(ctx, cabinetID)
	if err != nil || updatedCabinet.ElectricalRevision != cabinet.ElectricalRevision+1 {
		t.Fatalf("capacity update did not advance electrical revision: revision=%d err=%v", updatedCabinet.ElectricalRevision, err)
	}
	allocations, err := store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil || len(allocations) != 1 || allocations[0].StartPixel != 97 {
		t.Fatalf("capacity update changed a saved range: %#v err=%v", allocations, err)
	}
}

func TestDrawerLEDAllocationRejectsInvalidControllerCapacity(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()
	var controllerID int64
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		cabinet, err := q.GetCabinet(ctx, cabinetID)
		if err != nil {
			return err
		}
		controllerID = cabinet.ControllerID.Int64
		return q.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{IsOnline: sql.NullBool{Bool: false, Valid: true}, LedCount: 0, ID: controllerID})
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 1, PixelCount: 1}}); !errors.Is(err, ErrLEDControllerConfiguration) {
		t.Fatalf("nonpositive capacity: %v", err)
	}
}

func TestDrawerLEDAllocationRejectsOtherDrawerOverlapAndStaleRevision(t *testing.T) {
	service, store, cabinetID, drawerID := ledAllocationFixture(t)
	ctx := context.Background()
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 97, PixelCount: 8}}); err != nil {
		t.Fatal(err)
	}
	var secondDrawerID int64
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		var err error
		secondDrawerID, err = q.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer Two", X: 12, Width: 10, Height: 10})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, secondDrawerID, 1, []LEDAllocationInput{{StartPixel: 102, PixelCount: 2}}); !errors.Is(err, ErrLEDAllocationInvalid) {
		t.Fatalf("overlap with another drawer: %v", err)
	}
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, secondDrawerID, 0, []LEDAllocationInput{{StartPixel: 120, PixelCount: 2}}); !errors.Is(err, ErrLEDAllocationStale) {
		t.Fatalf("stale electrical revision: %v", err)
	}
	rows, err := store.ListDrawerLEDAllocationsByCabinet(ctx, cabinetID)
	if err != nil || len(rows) != 1 || rows[0].DrawerID != drawerID {
		t.Fatalf("failed writes mutated allocations: %#v, err=%v", rows, err)
	}
}

func TestDrawerLEDAllocationRequiresControllerAndProtectsDrawerLifecycle(t *testing.T) {
	store := newLayoutStore(t)
	ctx := context.Background()
	cabinetID, err := NewLayoutService(store).CreateCabinet(ctx, "Unassigned", 0, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	var drawerID int64
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		var e error
		drawerID, e = q.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer", Width: 10, Height: 10})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	service := NewLayoutService(store)
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 1, PixelCount: 1}}); !errors.Is(err, ErrLEDControllerConfiguration) {
		t.Fatalf("unassigned controller: %v", err)
	}

	service, store, cabinetID, drawerID = ledAllocationFixture(t)
	if err := service.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, 0, []LEDAllocationInput{{StartPixel: 1, PixelCount: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteDrawer(ctx, cabinetID, drawerID, 0); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("delete mapped drawer: %v", err)
	}
	if err := service.ArchiveDrawer(ctx, cabinetID, drawerID, 0); err != nil {
		t.Fatalf("archive should preserve the configured allocation: %v", err)
	}
	rows, err := store.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("archiving removed mapping: %#v, err=%v", rows, err)
	}
	if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		_, err := q.UpdateCabinet(ctx, db.UpdateCabinetParams{Name: "Reassigned", ControllerID: sql.NullInt64{Int64: 999, Valid: true}, CoordinateSystem: "grid", ID: cabinetID})
		return err
	}); err == nil {
		t.Fatal("controller reassignment should be blocked while allocations exist")
	}
}
