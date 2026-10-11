package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/tuxedocurly/wledger/internal/db"
)

var (
	ErrLEDAllocationStale         = errors.New("drawer LED allocation has changed; reload before saving")
	ErrLEDAllocationInvalid       = errors.New("invalid drawer LED allocation")
	ErrLEDControllerConfiguration = errors.New("drawer LED allocation requires a valid cabinet controller configuration")
)

// LEDAllocationInput uses user-facing controller-wide, 1-based pixel numbers.
// ID preserves the stable row identity when a range is edited.
type LEDAllocationInput struct {
	ID         int64 `json:"id"`
	StartPixel int64 `json:"start_pixel"`
	PixelCount int64 `json:"pixel_count"`
}

func ledAllocationError(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrLEDAllocationInvalid}, args...)...)
}

func ledAllocationEnd(start, count int64) (int64, error) {
	if start < 1 {
		return 0, ledAllocationError("LED Start must be at least 1")
	}
	if count < 1 {
		return 0, ledAllocationError("LED Count must be at least 1")
	}
	if count > math.MaxInt64-start+1 {
		return 0, ledAllocationError("LED range end overflows the supported integer range")
	}
	return start + count - 1, nil
}

// SaveDrawerLEDAllocations atomically replaces one drawer's range list. Cabinet
// controller ownership is inherited from cabinets.controller_id and also
// enforced by composite foreign keys. Ranges use controller-wide 1-based pixel
// numbers; they are not WLED segment-relative and may cross physical string
// boundaries.
func (s *LayoutService) SaveDrawerLEDAllocations(ctx context.Context, cabinetID, drawerID, revision int64, inputs []LEDAllocationInput) error {
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		cab, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cab.ElectricalRevision != revision {
			return ErrLEDAllocationStale
		}
		if !cab.ControllerID.Valid || cab.ControllerID.Int64 <= 0 {
			return fmt.Errorf("%w: assign a controller to this cabinet before saving drawer LED ranges", ErrLEDControllerConfiguration)
		}
		drawer, err := q.GetDrawer(ctx, drawerID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if drawer.CabinetID != cabinetID {
			return fmt.Errorf("%w: drawer does not belong to this cabinet", ErrLEDAllocationInvalid)
		}
		controller, err := q.GetController(ctx, cab.ControllerID.Int64)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: cabinet references missing controller #%d", ErrLEDControllerConfiguration, cab.ControllerID.Int64)
		}
		if err != nil {
			return err
		}
		capacity := controller.LedCount
		if capacity < 1 {
			return fmt.Errorf("%w: controller #%d has no valid positive LED capacity", ErrLEDControllerConfiguration, controller.ID)
		}

		existing, err := q.ListDrawerLEDAllocationsByCabinet(ctx, cabinetID)
		if err != nil {
			return err
		}
		byID := make(map[int64]db.DrawerLedAllocation, len(existing))
		others := make([]db.DrawerLedAllocation, 0, len(existing))
		for _, allocation := range existing {
			if allocation.DrawerID == drawerID {
				byID[allocation.ID] = allocation
			} else {
				others = append(others, allocation)
			}
		}

		type checked struct {
			input LEDAllocationInput
			end   int64
			order int64
		}
		checkedInputs := make([]checked, 0, len(inputs))
		seenIDs := make(map[int64]bool, len(inputs))
		for i, input := range inputs {
			if input.ID < 0 || (input.ID > 0 && (seenIDs[input.ID] || byID[input.ID].ID == 0)) {
				return ledAllocationError("range %d has an unknown or duplicated allocation identity", i+1)
			}
			if input.ID > 0 {
				seenIDs[input.ID] = true
			}
			end, err := ledAllocationEnd(input.StartPixel, input.PixelCount)
			if err != nil {
				return err
			}
			if end > capacity {
				return ledAllocationError("range %d ends at pixel %d, beyond controller #%d capacity (%d)", i+1, end, controller.ID, capacity)
			}
			for _, other := range others {
				otherEnd, rangeErr := ledAllocationEnd(other.StartPixel, other.PixelCount)
				if rangeErr != nil || other.ControllerID != controller.ID {
					return fmt.Errorf("%w: existing drawer LED configuration is invalid; review controller assignments and ranges", ErrLEDControllerConfiguration)
				}
				if input.StartPixel <= otherEnd && other.StartPixel <= end {
					return ledAllocationError("range %d (pixels %d–%d) overlaps drawer #%d (pixels %d–%d)", i+1, input.StartPixel, end, other.DrawerID, other.StartPixel, otherEnd)
				}
			}
			for j := 0; j < i; j++ {
				prior := checkedInputs[j]
				if input.StartPixel <= prior.end && prior.input.StartPixel <= end {
					return ledAllocationError("ranges %d and %d on this drawer overlap (pixels %d–%d and %d–%d)", j+1, i+1, prior.input.StartPixel, prior.end, input.StartPixel, end)
				}
			}
			checkedInputs = append(checkedInputs, checked{input: input, end: end, order: int64(i)})
		}

		for id := range byID {
			if !seenIDs[id] {
				if _, err := q.DeleteDrawerLEDAllocation(ctx, db.DeleteDrawerLEDAllocationParams{ID: id, CabinetID: cabinetID, DrawerID: drawerID}); err != nil {
					return err
				}
			}
		}
		for _, item := range checkedInputs {
			if item.input.ID == 0 {
				if _, err := q.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: cabinetID, DrawerID: drawerID, ControllerID: controller.ID, StartPixel: item.input.StartPixel, PixelCount: item.input.PixelCount, DisplayOrder: item.order}); err != nil {
					return err
				}
				continue
			}
			rows, err := q.UpdateDrawerLEDAllocation(ctx, db.UpdateDrawerLEDAllocationParams{StartPixel: item.input.StartPixel, PixelCount: item.input.PixelCount, DisplayOrder: item.order, ID: item.input.ID, CabinetID: cabinetID, DrawerID: drawerID, ControllerID: controller.ID})
			if err != nil {
				return err
			}
			if rows != 1 {
				return ledAllocationError("allocation #%d no longer belongs to this drawer and controller", item.input.ID)
			}
		}
		rows, err := q.AdvanceCabinetElectricalRevision(ctx, db.AdvanceCabinetElectricalRevisionParams{ID: cabinetID, ElectricalRevision: revision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLEDAllocationStale
		}
		return nil
	})
}
