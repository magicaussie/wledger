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

// SaveDrawerLEDAllocations preserves the previous single-controller API and
// routes writes to the cabinet's legacy primary controller.
func (s *LayoutService) SaveDrawerLEDAllocations(ctx context.Context, cabinetID, drawerID, revision int64, inputs []LEDAllocationInput) error {
	cabinet, err := s.store.GetCabinet(ctx, cabinetID)
	if err != nil {
		return err
	}
	controllerID := int64(0)
	if cabinet.ControllerID.Valid {
		controllerID = cabinet.ControllerID.Int64
	}
	return s.SaveDrawerLEDAllocationsForController(ctx, cabinetID, drawerID, revision, controllerID, inputs)
}

// SaveDrawerLEDAllocationsForController atomically replaces one drawer's range
// list. Every range for a drawer belongs to exactly one controller; ranges use
// that controller's 1-based controller-wide pixel space and may cross physical
// output boundaries. Physical outputs are not WLED segments.
func (s *LayoutService) SaveDrawerLEDAllocationsForController(ctx context.Context, cabinetID, drawerID, revision, controllerID int64, inputs []LEDAllocationInput) error {
	if controllerID < 0 {
		return ErrLEDControllerConfiguration
	}
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
		members, err := q.ListCabinetControllers(ctx, cabinetID)
		if err != nil {
			return err
		}
		memberIDs := make(map[int64]bool, len(members))
		for _, member := range members {
			memberIDs[member.ControllerID] = true
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
		drawerAllocations, err := q.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
		if err != nil {
			return err
		}
		existingControllerID := int64(0)
		for _, allocation := range drawerAllocations {
			if existingControllerID == 0 {
				existingControllerID = allocation.ControllerID
			} else if existingControllerID != allocation.ControllerID {
				return fmt.Errorf("%w: drawer #%d already contains ranges from multiple controllers", ErrLEDControllerConfiguration, drawerID)
			}
		}
		if len(inputs) > 0 {
			if controllerID == 0 || !memberIDs[controllerID] {
				return fmt.Errorf("%w: select a controller assigned to this cabinet before saving drawer LED ranges", ErrLEDControllerConfiguration)
			}
			if existingControllerID != 0 && existingControllerID != controllerID {
				return fmt.Errorf("%w: clear this drawer's saved LED ranges before changing its controller", ErrUnsafeLayout)
			}
		}
		var controller db.Controller
		if controllerID > 0 {
			controller, err = q.GetController(ctx, controllerID)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: cabinet references missing controller #%d", ErrLEDControllerConfiguration, controllerID)
			}
			if err != nil {
				return err
			}
		}
		capacity := controller.LedCount
		if len(inputs) > 0 && capacity < 1 {
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
				if other.ControllerID != controller.ID {
					continue
				}
				otherEnd, rangeErr := ledAllocationEnd(other.StartPixel, other.PixelCount)
				if rangeErr != nil {
					return fmt.Errorf("%w: existing controller #%d allocation is invalid", ErrLEDControllerConfiguration, controller.ID)
				}
				if input.StartPixel <= otherEnd && other.StartPixel <= end {
					return ledAllocationError("range %d (pixels %d–%d) overlaps drawer #%d on controller #%d (pixels %d–%d)", i+1, input.StartPixel, end, other.DrawerID, controller.ID, other.StartPixel, otherEnd)
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
