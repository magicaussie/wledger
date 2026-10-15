package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

var (
	ErrLEDControllerCapacityStale   = errors.New("controller LED capacity has changed; reload before saving")
	ErrLEDControllerCapacityInvalid = errors.New("controller LED capacity must be a positive whole number")
)

// SaveControllerLEDCapacity is retained for legacy installations that have not
// configured physical strings. Cabinet users should configure string lengths;
// those lengths set capacity automatically.
func (s *LayoutService) SaveControllerLEDCapacity(ctx context.Context, controllerID, expectedCapacity, capacity, electricalRevision int64) error {
	return s.SaveControllerLEDCapacityConfirmed(ctx, controllerID, expectedCapacity, capacity, electricalRevision, false)
}

func (s *LayoutService) SaveControllerLEDCapacityConfirmed(ctx context.Context, controllerID, expectedCapacity, capacity, electricalRevision int64, confirmMappingImpact bool) error {
	if controllerID <= 0 || expectedCapacity < 0 || capacity < 1 {
		return ErrLEDControllerCapacityInvalid
	}
	return db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return err
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
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
		cabinet, cabinetErr := q.GetCabinetByAssignedController(ctx, controllerID)
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
		if assigned {
			members, err := q.ListCabinetControllers(ctx, cabinet.ID)
			if err != nil {
				return err
			}
			for _, member := range members {
				if member.ControllerID != controllerID || member.StringsConfigured != 1 {
					continue
				}
				allocations, err := q.ListDrawerLEDAllocationsByCabinet(ctx, cabinet.ID)
				if err != nil {
					return err
				}
				if len(allocations) > 0 {
					return fmt.Errorf("%w: controller #%d output capacity is hardware-owned; review the output configuration in Hardware", ErrUnsafeLayout, controllerID)
				}
				return fmt.Errorf("%w: controller #%d capacity is calculated from its configured outputs", ErrLEDControllerCapacityInvalid, controllerID)
			}
			topology, err := cabinetpixels.Load(ctx, q, cabinet.ID)
			if err != nil {
				return fmt.Errorf("%w: current topology is invalid: %v", ErrUnsafeLayout, err)
			}
			capacities := make([]cabinetpixels.Capacity, 0, len(topology.Controllers))
			for _, item := range topology.Controllers {
				pixels := item.Pixels
				if item.Record.ID == controllerID {
					pixels = capacity
				}
				capacities = append(capacities, cabinetpixels.Capacity{ControllerID: item.Record.ID, Order: item.Order, Pixels: pixels})
			}
			newSpans, newTotal, err := cabinetpixels.BuildSpans(capacities)
			if err != nil {
				return err
			}
			allocations, err := q.ListDrawerLEDAllocationsByCabinet(ctx, cabinet.ID)
			if err != nil {
				return err
			}
			var changed []int64
			for _, allocation := range allocations {
				before, err := cabinetpixels.SplitTopologyRange(topology, allocation.StartPixel, allocation.PixelCount)
				if err != nil {
					return ErrUnsafeLayout
				}
				if _, err := cabinetpixels.SplitRange(allocation.StartPixel, allocation.PixelCount, newSpans); err != nil {
					return fmt.Errorf("%w: capacity %d would put drawer #%d range %d–%d outside cabinet capacity %d", ErrUnsafeLayout, capacity, allocation.DrawerID, allocation.StartPixel, allocation.StartPixel+allocation.PixelCount-1, newTotal)
				}
				afterTopology := topologyWithControllerSpans(topology, newSpans, newTotal)
				after, err := cabinetpixels.SplitTopologyRange(afterTopology, allocation.StartPixel, allocation.PixelCount)
				if err != nil {
					return ErrUnsafeLayout
				}
				if !samePixelDestination(before, after) {
					changed = append(changed, allocation.DrawerID)
				}
			}
			if len(changed) > 0 && !confirmMappingImpact {
				return fmt.Errorf("%w: capacity change shifts physical destinations for drawer mappings %v; review the before/after preview and confirm explicitly", ErrUnsafeLayout, uniqueIDs(changed))
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

func topologyWithControllerSpans(current cabinetpixels.Topology, spans []cabinetpixels.Span, total int64) cabinetpixels.Topology {
	out := cabinetpixels.Topology{CabinetID: current.CabinetID, Total: total, Controllers: make([]cabinetpixels.Controller, 0, len(current.Controllers))}
	byID := make(map[int64]cabinetpixels.Span, len(spans))
	for _, span := range spans {
		byID[span.ControllerID] = span
	}
	for _, controller := range current.Controllers {
		controller.Span = byID[controller.Record.ID]
		out.Controllers = append(out.Controllers, controller)
	}
	return out
}

func samePixelDestination(a, b []cabinetpixels.PhysicalPiece) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ControllerID != b[i].ControllerID || a[i].StringID != b[i].StringID || a[i].LocalStart != b[i].LocalStart || a[i].StringStart != b[i].StringStart || a[i].Count != b[i].Count {
			return false
		}
	}
	return true
}
