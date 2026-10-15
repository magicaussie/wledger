package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
)

var ErrCabinetControllerInvalid = errors.New("invalid cabinet controller assignment")

// SaveCabinetController is the backwards-compatible single-controller form.
func (s *LayoutService) SaveCabinetController(ctx context.Context, cabinetID, electricalRevision, controllerID int64) error {
	if controllerID < 0 {
		return ErrCabinetControllerInvalid
	}
	var ids []int64
	if controllerID > 0 {
		ids = []int64{controllerID}
	}
	return s.SaveCabinetControllersConfirmed(ctx, cabinetID, electricalRevision, ids, false)
}

// SaveCabinetControllers preserves the supplied controller order. If that
// changes the physical destination of any saved cabinet-wide range, the caller
// must obtain an explicit mapping-impact confirmation and call the confirmed
// form.
func (s *LayoutService) SaveCabinetControllers(ctx context.Context, cabinetID, electricalRevision int64, controllerIDs []int64) error {
	return s.SaveCabinetControllersConfirmed(ctx, cabinetID, electricalRevision, controllerIDs, false)
}

func (s *LayoutService) SaveCabinetControllersConfirmed(ctx context.Context, cabinetID, electricalRevision int64, controllerIDs []int64, confirmMappingImpact bool) error {
	if cabinetID <= 0 || len(controllerIDs) > 5 {
		return ErrCabinetControllerInvalid
	}
	desired := make(map[int64]bool, len(controllerIDs))
	for _, id := range controllerIDs {
		if id <= 0 || desired[id] {
			return fmt.Errorf("%w: select between zero and five distinct controllers", ErrCabinetControllerInvalid)
		}
		desired[id] = true
	}
	return db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return err
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
		cabinet, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cabinet.ElectricalRevision != electricalRevision {
			return ErrLEDAllocationStale
		}

		current, err := q.ListCabinetControllers(ctx, cabinetID)
		if err != nil {
			return err
		}
		currentByID := make(map[int64]db.CabinetController, len(current))
		for _, member := range current {
			currentByID[member.ControllerID] = member
		}
		unchanged := len(currentByID) == len(controllerIDs)
		if unchanged {
			for i, id := range controllerIDs {
				if i >= len(current) || current[i].ControllerID != id {
					unchanged = false
					break
				}
			}
		}
		if unchanged {
			return nil
		}

		controllers := make(map[int64]db.Controller, len(controllerIDs))
		for _, id := range controllerIDs {
			controller, err := q.GetController(ctx, id)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: controller #%d does not exist", ErrCabinetControllerInvalid, id)
			}
			if err != nil {
				return err
			}
			controllers[id] = controller
			if _, existed := currentByID[id]; existed {
				continue
			}
			otherCabinet, err := q.GetCabinetByAssignedController(ctx, id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && otherCabinet.ID != cabinetID {
				return fmt.Errorf("%w: controller #%d is already assigned to cabinet #%d", ErrCabinetControllerInvalid, id, otherCabinet.ID)
			}
		}

		oldTopology, err := cabinetpixels.Load(ctx, q, cabinetID)
		if err != nil {
			return fmt.Errorf("%w: current cabinet pixel topology is invalid: %v", ErrUnsafeLayout, err)
		}
		allocations, err := q.ListDrawerLEDAllocationsByCabinet(ctx, cabinetID)
		if err != nil {
			return err
		}
		newCapacities := make([]cabinetpixels.Capacity, 0, len(controllerIDs))
		for slot, id := range controllerIDs {
			controller := controllers[id]
			strings, err := q.ListCabinetLEDStringsByController(ctx, db.ListCabinetLEDStringsByControllerParams{CabinetID: cabinetID, ControllerID: id})
			if _, existed := currentByID[id]; !existed {
				strings = nil
			}
			if err != nil {
				return err
			}
			capacity := controller.LedCount
			member, configured := currentByID[id]
			if configured && member.StringsConfigured == 1 {
				capacity = 0
				for _, stringRecord := range strings {
					if stringRecord.PixelCount < 1 || stringRecord.PixelCount > math.MaxInt64-capacity {
						return ErrCabinetControllerInvalid
					}
					capacity += stringRecord.PixelCount
				}
			}
			newCapacities = append(newCapacities, cabinetpixels.Capacity{ControllerID: id, Order: int64(slot), Pixels: capacity})
		}
		newSpans, newTotal, err := cabinetpixels.BuildSpans(newCapacities)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCabinetControllerInvalid, err)
		}
		oldSpans := make([]cabinetpixels.Span, 0, len(oldTopology.Controllers))
		for _, controller := range oldTopology.Controllers {
			oldSpans = append(oldSpans, controller.Span)
		}
		mappingImpact := false
		var changedDrawerIDs []int64
		for _, allocation := range allocations {
			before, splitErr := cabinetpixels.SplitRange(allocation.StartPixel, allocation.PixelCount, oldSpans)
			if splitErr != nil {
				return fmt.Errorf("%w: allocation #%d is outside the current cabinet capacity", ErrUnsafeLayout, allocation.ID)
			}
			after, splitErr := cabinetpixels.SplitRange(allocation.StartPixel, allocation.PixelCount, newSpans)
			if splitErr != nil {
				return fmt.Errorf("%w: the proposed controller order leaves drawer #%d range %d–%d outside the new cabinet capacity %d; adjust the range before changing controllers", ErrUnsafeLayout, allocation.DrawerID, allocation.StartPixel, allocation.StartPixel+allocation.PixelCount-1, newTotal)
			}
			if !reflect.DeepEqual(before, after) {
				mappingImpact = true
				changedDrawerIDs = append(changedDrawerIDs, allocation.DrawerID)
			}
		}
		if mappingImpact && !confirmMappingImpact {
			return fmt.Errorf("%w: changing controller order or membership changes physical destinations for saved drawer mappings %v; review the before/after preview and confirm explicitly", ErrUnsafeLayout, uniqueIDs(changedDrawerIDs))
		}

		for id := range currentByID {
			if desired[id] {
				continue
			}
			strings, err := q.ListCabinetLEDStringsByController(ctx, db.ListCabinetLEDStringsByControllerParams{CabinetID: cabinetID, ControllerID: id})
			if err != nil {
				return err
			}
			if len(strings) != 0 {
				return fmt.Errorf("%w: delete controller #%d's physical strings before removing it from this cabinet", ErrUnsafeLayout, id)
			}
			if _, err := q.DeleteCabinetController(ctx, db.DeleteCabinetControllerParams{CabinetID: cabinetID, ControllerID: id}); err != nil {
				return err
			}
		}

		maxSlot := int64(-1)
		for _, member := range current {
			if member.Slot > maxSlot {
				maxSlot = member.Slot
			}
		}
		if maxSlot > math.MaxInt64-int64(len(current)+len(controllerIDs))-2 {
			return ErrCabinetControllerInvalid
		}
		temporaryBase := maxSlot + int64(len(current)+len(controllerIDs)) + 2
		if err := q.CreateCabinetControllerReorderGuard(ctx, cabinetID); err != nil {
			return err
		}
		for index, member := range current {
			if !desired[member.ControllerID] {
				continue
			}
			if _, err := q.UpdateCabinetControllerSlot(ctx, db.UpdateCabinetControllerSlotParams{Slot: temporaryBase + int64(index), CabinetID: cabinetID, ControllerID: member.ControllerID}); err != nil {
				return err
			}
		}
		for index, id := range controllerIDs {
			if _, exists := currentByID[id]; exists {
				continue
			}
			if err := q.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinetID, ControllerID: id, Slot: temporaryBase + int64(len(current)+index)}); err != nil {
				return fmt.Errorf("assign controller #%d: %w", id, err)
			}
		}
		for slot, id := range controllerIDs {
			rows, err := q.UpdateCabinetControllerSlot(ctx, db.UpdateCabinetControllerSlotParams{Slot: int64(slot), CabinetID: cabinetID, ControllerID: id})
			if err != nil {
				return err
			}
			if rows != 1 {
				return ErrCabinetControllerInvalid
			}
		}
		if err := q.DeleteCabinetControllerReorderGuard(ctx, cabinetID); err != nil {
			return err
		}

		legacyPrimary := sql.NullInt64{}
		if len(controllerIDs) > 0 {
			legacyPrimary = sql.NullInt64{Int64: controllerIDs[0], Valid: true}
		}
		if _, err := q.UpdateCabinetController(ctx, db.UpdateCabinetControllerParams{ControllerID: legacyPrimary, ID: cabinetID}); err != nil {
			return err
		}
		for _, controllerID := range controllerIDs {
			if _, existed := currentByID[controllerID]; existed {
				continue
			}
			controller := controllers[controllerID]
			if err := initializeCabinetControllerOutputs(ctx, q, cabinetID, controller); err != nil {
				return err
			}
		}
		rows, err := q.AdvanceCabinetElectricalRevision(ctx, db.AdvanceCabinetElectricalRevisionParams{ID: cabinetID, ElectricalRevision: electricalRevision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLEDAllocationStale
		}
		return nil
	})
}

func initializeCabinetControllerOutputs(ctx context.Context, q db.Querier, cabinetID int64, controller db.Controller) error {
	outputs, err := q.GetControllerOutputs(ctx, controller.ID)
	if err != nil {
		return err
	}
	if len(outputs) == 0 && controller.LedCount > 0 {
		if _, err := q.CreateControllerOutput(ctx, db.CreateControllerOutputParams{
			ControllerID: controller.ID, DisplayOrder: 0, Name: "Output 1", PixelCount: controller.LedCount,
			Direction: "forward", Pattern: "linear", Rows: 1, Columns: 1, CustomOrderJson: "[]",
		}); err != nil {
			return fmt.Errorf("initialize legacy output for controller #%d: %w", controller.ID, err)
		}
		outputs, err = q.GetControllerOutputs(ctx, controller.ID)
		if err != nil {
			return err
		}
	}
	if len(outputs) == 0 {
		return nil
	}
	settings := controllerconfig.Settings{ControllerType: controller.ControllerType, ConnectionTimeoutMS: controller.ConnectionTimeoutMs}
	for _, output := range outputs {
		var customOrder []int64
		if err := json.Unmarshal([]byte(output.CustomOrderJson), &customOrder); err != nil {
			return fmt.Errorf("controller #%d output #%d has invalid custom mapping: %w", controller.ID, output.ID, err)
		}
		settings.Outputs = append(settings.Outputs, controllerconfig.Output{
			ID: output.ID, DisplayOrder: output.DisplayOrder, Name: output.Name, PixelCount: output.PixelCount,
			Direction: output.Direction, Pattern: output.Pattern, Rows: output.Rows, Columns: output.Columns,
			CustomOrder: customOrder,
		})
	}
	total, err := settings.NormalizeAndValidate()
	if err != nil || total != controller.LedCount {
		return fmt.Errorf("%w: controller #%d outputs do not match its configured total pixel capacity", ErrLEDControllerConfiguration, controller.ID)
	}
	for _, output := range outputs {
		if _, err := q.CreateCabinetLEDString(ctx, db.CreateCabinetLEDStringParams{
			CabinetID: cabinetID, ControllerID: controller.ID, Name: output.Name,
			DisplayOrder: output.DisplayOrder, PixelCount: output.PixelCount, LayoutComplete: 0,
			ControllerOutputID: sql.NullInt64{Int64: output.ID, Valid: true},
		}); err != nil {
			return fmt.Errorf("create cabinet path projection for controller #%d output #%d: %w", controller.ID, output.ID, err)
		}
	}
	rows, err := q.UpdateCabinetControllerStringsConfigured(ctx, db.UpdateCabinetControllerStringsConfiguredParams{
		StringsConfigured: 1, CabinetID: cabinetID, ControllerID: controller.ID,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrCabinetControllerInvalid
	}
	return nil
}

func uniqueIDs(values []int64) []int64 {
	seen := make(map[int64]bool, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
