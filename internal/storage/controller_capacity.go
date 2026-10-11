package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
)

var (
	ErrLEDControllerCapacityStale   = errors.New("controller LED capacity has changed; reload before saving")
	ErrLEDControllerCapacityInvalid = errors.New("controller LED capacity must be a positive whole number")
)

// SaveControllerLEDCapacity stores the controller-wide pixel capacity. The
// expected old value protects the controller setting from stale form posts;
// when a cabinet uses this controller, its electrical revision is checked and
// advanced in the same transaction so open allocation editors become stale.
func (s *LayoutService) SaveControllerLEDCapacity(ctx context.Context, controllerID, expectedCapacity, capacity, electricalRevision int64) error {
	if controllerID <= 0 || expectedCapacity < 0 || capacity < 1 {
		return ErrLEDControllerCapacityInvalid
	}
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		controller, err := q.GetController(ctx, controllerID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: controller #%d does not exist", ErrLEDControllerConfiguration, controllerID)
		}
		if err != nil {
			return err
		}
		if controller.LedCount != expectedCapacity {
			return ErrLEDControllerCapacityStale
		}

		cabinet, cabinetErr := q.GetCabinetByController(ctx, sql.NullInt64{Int64: controllerID, Valid: true})
		assigned := cabinetErr == nil
		if cabinetErr != nil && !errors.Is(cabinetErr, sql.ErrNoRows) {
			return cabinetErr
		}
		if assigned && cabinet.ElectricalRevision != electricalRevision {
			return ErrLEDAllocationStale
		}
		if !assigned && electricalRevision != 0 {
			return ErrLEDAllocationStale
		}

		allocations, err := q.ListDrawerLEDAllocationsByController(ctx, controllerID)
		if err != nil {
			return err
		}
		for _, allocation := range allocations {
			end, rangeErr := ledAllocationEnd(allocation.StartPixel, allocation.PixelCount)
			if rangeErr != nil || end > capacity {
				return fmt.Errorf("%w: capacity %d would invalidate saved drawer #%d LED range", ErrLEDControllerCapacityInvalid, capacity, allocation.DrawerID)
			}
		}
		if controller.LedCount == capacity {
			return nil
		}
		rows, err := q.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{LedCount: capacity, ID: controllerID, LedCount_2: expectedCapacity})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLEDControllerCapacityStale
		}
		if assigned {
			rows, err = q.AdvanceCabinetElectricalRevision(ctx, db.AdvanceCabinetElectricalRevisionParams{ID: cabinet.ID, ElectricalRevision: electricalRevision})
			if err != nil {
				return err
			}
			if rows != 1 {
				return ErrLEDAllocationStale
			}
		}
		return nil
	})
}
