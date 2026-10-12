package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

var (
	ErrLEDAllocationStale         = errors.New("drawer LED allocation has changed; reload before saving")
	ErrLEDAllocationInvalid       = errors.New("invalid drawer LED allocation")
	ErrLEDControllerConfiguration = errors.New("drawer LED allocation requires a valid cabinet controller configuration")
)

// LEDAllocationInput uses user-facing cabinet-wide, 1-based pixel numbers. ID
// preserves the stable allocation identity when a range is edited.
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

// SaveDrawerLEDAllocations atomically replaces a drawer's cabinet-wide ranges.
// A range can cross string and controller boundaries; those spans are derived
// later from the saved wiring topology.
func (s *LayoutService) SaveDrawerLEDAllocations(ctx context.Context, cabinetID, drawerID, revision int64, inputs []LEDAllocationInput) error {
	if cabinetID <= 0 || drawerID <= 0 {
		return ErrLEDAllocationInvalid
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
		topology, err := cabinetpixels.Load(ctx, q, cabinetID)
		if err != nil {
			return fmt.Errorf("%w: cannot resolve cabinet pixel capacity: %v", ErrLEDControllerConfiguration, err)
		}
		if len(inputs) > 0 && topology.Total < 1 {
			return fmt.Errorf("%w: configure at least one positive-capacity controller string before assigning pixels", ErrLEDControllerConfiguration)
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
			if end > topology.Total {
				return ledAllocationError("range %d ends at pixel %d, beyond cabinet capacity (%d)", i+1, end, topology.Total)
			}
			for _, other := range others {
				otherEnd, rangeErr := ledAllocationEnd(other.StartPixel, other.PixelCount)
				if rangeErr != nil {
					return fmt.Errorf("%w: existing allocation #%d is invalid", ErrLEDControllerConfiguration, other.ID)
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
			checkedInputs = append(checkedInputs, checked{input: input, end: end})
		}

		// Replace this drawer's rows together so a valid edit can move or reorder
		// ranges without transient overlap against another row in the same drawer.
		for _, old := range byID {
			if _, err := q.DeleteDrawerLEDAllocation(ctx, db.DeleteDrawerLEDAllocationParams{ID: old.ID, CabinetID: cabinetID, DrawerID: drawerID}); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		for order, item := range checkedInputs {
			if item.input.ID == 0 {
				if _, err := q.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: cabinetID, DrawerID: drawerID, StartPixel: item.input.StartPixel, PixelCount: item.input.PixelCount, DisplayOrder: int64(order)}); err != nil {
					return err
				}
				continue
			}
			old := byID[item.input.ID]
			if err := q.RestoreDrawerLEDAllocation(ctx, db.RestoreDrawerLEDAllocationParams{
				ID: old.ID, CabinetID: cabinetID, DrawerID: drawerID,
				StartPixel: item.input.StartPixel, PixelCount: item.input.PixelCount,
				DisplayOrder: int64(order), CreatedAt: old.CreatedAt, UpdatedAt: now,
			}); err != nil {
				return err
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

// SaveDrawerLEDAllocationsForController is a compatibility adapter for old
// clients whose payload used controller-local pixels. New UI must call
// SaveDrawerLEDAllocations with cabinet-wide coordinates.
func (s *LayoutService) SaveDrawerLEDAllocationsForController(ctx context.Context, cabinetID, drawerID, revision, controllerID int64, inputs []LEDAllocationInput) error {
	if len(inputs) == 0 {
		return s.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, revision, inputs)
	}
	if controllerID <= 0 {
		return ErrLEDControllerConfiguration
	}
	var converted []LEDAllocationInput
	if err := s.store.ExecTx(ctx, func(q db.Querier) error {
		topology, err := cabinetpixels.Load(ctx, q, cabinetID)
		if err != nil {
			return err
		}
		var selected *cabinetpixels.Controller
		for i := range topology.Controllers {
			if topology.Controllers[i].Record.ID == controllerID {
				selected = &topology.Controllers[i]
				break
			}
		}
		if selected == nil {
			return ErrLEDControllerConfiguration
		}
		converted = append([]LEDAllocationInput(nil), inputs...)
		for i := range converted {
			end, err := ledAllocationEnd(converted[i].StartPixel, converted[i].PixelCount)
			if err != nil || end > selected.Pixels {
				return ErrLEDAllocationInvalid
			}
			converted[i].StartPixel += selected.Start - 1
		}
		return nil
	}); err != nil {
		return err
	}
	return s.SaveDrawerLEDAllocations(ctx, cabinetID, drawerID, revision, converted)
}
