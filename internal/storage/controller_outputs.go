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

// SaveControllerOutputSettingsTx persists controller-owned output definitions
// and their cabinet path projections within the caller's existing operation
// claim. The cabinet string rows remain the cabinet-specific visual-path layer;
// output capacity, order and electrical presentation come from controller_outputs.
func SaveControllerOutputSettingsTx(ctx context.Context, q db.Querier, controllerID int64, expectedCapacity int64, settings controllerconfig.Settings, confirmMappingImpact bool) error {
	if controllerID <= 0 {
		return ErrLEDControllerConfiguration
	}
	total, err := settings.NormalizeAndValidate()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrLayoutInvalid, err)
	}
	controller, err := q.GetController(ctx, controllerID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLayoutNotFound
	}
	if err != nil {
		return err
	}
	if controller.LedCount != expectedCapacity {
		return ErrLEDControllerCapacityStale
	}
	oldOutputs, err := q.GetControllerOutputs(ctx, controllerID)
	if err != nil {
		return err
	}
	oldByID := make(map[int64]db.ControllerOutput, len(oldOutputs))
	var maxOutputOrder int64
	for i, output := range oldOutputs {
		if output.ControllerID != controllerID {
			return ErrLEDControllerConfiguration
		}
		oldByID[output.ID] = output
		if i == 0 || output.DisplayOrder > maxOutputOrder {
			maxOutputOrder = output.DisplayOrder
		}
	}
	outputChanged := !sameControllerOutputs(oldOutputs, settings.Outputs)
	seenIDs := make(map[int64]bool, len(settings.Outputs))
	for _, output := range settings.Outputs {
		if output.ID > 0 {
			if _, exists := oldByID[output.ID]; !exists || seenIDs[output.ID] {
				return fmt.Errorf("%w: output identity is unknown or repeated", ErrLEDControllerConfiguration)
			}
			seenIDs[output.ID] = true
		}
	}

	cabinet, cabinetErr := q.GetCabinetByAssignedController(ctx, controllerID)
	assigned := cabinetErr == nil
	if cabinetErr != nil && !errors.Is(cabinetErr, sql.ErrNoRows) {
		return cabinetErr
	}
	var currentTopology cabinetpixels.Topology
	var members []db.CabinetController
	var oldStrings []db.CabinetLedString
	var allocations []db.DrawerLedAllocation
	stringByOutputID := make(map[int64]db.CabinetLedString)
	sectionByStringID := make(map[int64][]db.CabinetLedPathSection)
	if assigned {
		currentTopology, err = cabinetpixels.Load(ctx, q, cabinet.ID)
		if err != nil {
			return fmt.Errorf("%w: current cabinet topology is invalid: %v", ErrUnsafeLayout, err)
		}
		members, err = q.ListCabinetControllers(ctx, cabinet.ID)
		if err != nil {
			return err
		}
		oldStrings, err = q.ListCabinetLEDStringsByController(ctx, db.ListCabinetLEDStringsByControllerParams{CabinetID: cabinet.ID, ControllerID: controllerID})
		if err != nil {
			return err
		}
		for _, stringRecord := range oldStrings {
			outputID := stringRecord.ControllerOutputID.Int64
			if !stringRecord.ControllerOutputID.Valid {
				for _, output := range oldOutputs {
					if output.DisplayOrder == stringRecord.DisplayOrder {
						outputID = output.ID
						break
					}
				}
			}
			if outputID > 0 {
				stringByOutputID[outputID] = stringRecord
			}
			sections, err := q.ListCabinetLEDPathSectionsByString(ctx, stringRecord.ID)
			if err != nil {
				return err
			}
			sectionByStringID[stringRecord.ID] = sections
		}
		allocations, err = q.ListDrawerLEDAllocationsByCabinet(ctx, cabinet.ID)
		if err != nil {
			return err
		}
	}

	newSpans := make([]cabinetpixels.Capacity, 0, len(members))
	var newTotal int64
	if assigned {
		for _, member := range members {
			pixels := total
			if member.ControllerID != controllerID {
				other, err := q.GetController(ctx, member.ControllerID)
				if err != nil {
					return err
				}
				pixels = other.LedCount
			}
			newSpans = append(newSpans, cabinetpixels.Capacity{ControllerID: member.ControllerID, Order: member.Slot, Pixels: pixels})
		}
		var spans []cabinetpixels.Span
		spans, newTotal, err = cabinetpixels.BuildSpans(newSpans)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrLayoutInvalid, err)
		}
		mappingImpact := outputChanged && len(allocations) > 0
		for _, allocation := range allocations {
			if _, err := cabinetpixels.SplitRange(allocation.StartPixel, allocation.PixelCount, spans); err != nil {
				return fmt.Errorf("%w: proposed output capacity puts drawer #%d mapping outside the cabinet", ErrUnsafeLayout, allocation.DrawerID)
			}
			before, err := cabinetpixels.SplitTopologyRange(currentTopology, allocation.StartPixel, allocation.PixelCount)
			if err != nil {
				return fmt.Errorf("%w: current drawer #%d mapping is invalid", ErrUnsafeLayout, allocation.DrawerID)
			}
			afterTopology, err := topologyWithControllerOutputs(currentTopology, controllerID, settings.Outputs, stringByOutputID, sectionByStringID, spans, newTotal)
			if err != nil {
				return err
			}
			after, err := cabinetpixels.SplitTopologyRange(afterTopology, allocation.StartPixel, allocation.PixelCount)
			if err != nil {
				return fmt.Errorf("%w: proposed output mapping cannot resolve drawer #%d", ErrUnsafeLayout, allocation.DrawerID)
			}
			if !samePixelDestination(before, after) {
				mappingImpact = true
			}
		}
		if mappingImpact && !confirmMappingImpact {
			return fmt.Errorf("%w: controller outputs change saved drawer pixel destinations; review the Hardware output preview and confirm the mapping impact", ErrUnsafeLayout)
		}
		for _, stringRecord := range oldStrings {
			outputID := stringRecord.ControllerOutputID.Int64
			if !stringRecord.ControllerOutputID.Valid {
				for _, output := range oldOutputs {
					if output.DisplayOrder == stringRecord.DisplayOrder {
						outputID = output.ID
						break
					}
				}
			}
			if outputID > 0 && !seenIDs[outputID] {
				sections := sectionByStringID[stringRecord.ID]
				if len(sections) > 0 && !confirmMappingImpact {
					return fmt.Errorf("%w: removing output %q would discard its cabinet path; confirm the output change after reviewing the preview", ErrUnsafeLayout, stringRecord.Name)
				}
			}
		}
	}

	if outputChanged {
		tempBase := maxOutputOrder + int64(len(oldOutputs)+len(settings.Outputs)) + 100
		if tempBase < maxOutputOrder || tempBase > math.MaxInt64-int64(len(oldOutputs)+len(settings.Outputs)+1) {
			return fmt.Errorf("%w: output order overflows", ErrLayoutInvalid)
		}
		for index, output := range oldOutputs {
			if _, err := q.UpdateControllerOutput(ctx, db.UpdateControllerOutputParams{
				DisplayOrder: tempBase + int64(index), Name: output.Name, PixelCount: output.PixelCount,
				Direction: output.Direction, Pattern: output.Pattern, Rows: output.Rows, Columns: output.Columns,
				CustomOrderJson: output.CustomOrderJson, ID: output.ID, ControllerID: controllerID,
			}); err != nil {
				return err
			}
		}
		newOutputByOrder := make(map[int64]int64, len(settings.Outputs))
		for index := range settings.Outputs {
			output := &settings.Outputs[index]
			customOrder, err := json.Marshal(output.CustomOrder)
			if err != nil {
				return err
			}
			if output.ID == 0 {
				outputID, err := q.CreateControllerOutput(ctx, db.CreateControllerOutputParams{
					ControllerID: controllerID, DisplayOrder: tempBase + int64(len(oldOutputs)+index), Name: output.Name,
					PixelCount: output.PixelCount, Direction: output.Direction, Pattern: output.Pattern, Rows: output.Rows,
					Columns: output.Columns, CustomOrderJson: string(customOrder),
				})
				if err != nil {
					return err
				}
				output.ID = outputID
			} else {
				old := oldByID[output.ID]
				if _, err := q.UpdateControllerOutput(ctx, db.UpdateControllerOutputParams{
					DisplayOrder: tempBase + int64(len(oldOutputs)+index), Name: output.Name, PixelCount: output.PixelCount,
					Direction: output.Direction, Pattern: output.Pattern, Rows: output.Rows, Columns: output.Columns,
					CustomOrderJson: string(customOrder), ID: output.ID, ControllerID: controllerID,
				}); err != nil {
					return err
				}
				_ = old
			}
			newOutputByOrder[output.DisplayOrder] = output.ID
		}
		for index := range settings.Outputs {
			output := settings.Outputs[index]
			customOrder, err := json.Marshal(output.CustomOrder)
			if err != nil {
				return err
			}
			if _, err := q.UpdateControllerOutput(ctx, db.UpdateControllerOutputParams{
				DisplayOrder: output.DisplayOrder, Name: output.Name, PixelCount: output.PixelCount,
				Direction: output.Direction, Pattern: output.Pattern, Rows: output.Rows, Columns: output.Columns,
				CustomOrderJson: string(customOrder), ID: output.ID, ControllerID: controllerID,
			}); err != nil {
				return err
			}
		}
		if assigned {
			if err := syncCabinetOutputProjection(ctx, q, cabinet.ID, controllerID, settings.Outputs, newOutputByOrder, stringByOutputID, sectionByStringID, tempBase, confirmMappingImpact); err != nil {
				return err
			}
		}
		for _, output := range oldOutputs {
			if seenIDs[output.ID] {
				continue
			}
			if _, err := q.DeleteControllerOutput(ctx, db.DeleteControllerOutputParams{ID: output.ID, ControllerID: controllerID}); err != nil {
				return err
			}
		}
	}

	if controller.LedCount != total {
		rows, err := q.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{LedCount: total, ID: controllerID, LedCount_2: controller.LedCount})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLEDControllerCapacityStale
		}
	}
	if assigned && (outputChanged || controller.LedCount != total) {
		if _, err := q.AdvanceCabinetElectricalRevision(ctx, db.AdvanceCabinetElectricalRevisionParams{ID: cabinet.ID, ElectricalRevision: cabinet.ElectricalRevision}); err != nil {
			return err
		}
		for _, member := range members {
			if member.ControllerID == controllerID {
				if _, err := q.UpdateCabinetControllerStringsConfigured(ctx, db.UpdateCabinetControllerStringsConfiguredParams{
					StringsConfigured: 1, CabinetID: cabinet.ID, ControllerID: controllerID,
				}); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

func sameControllerOutputs(existing []db.ControllerOutput, desired []controllerconfig.Output) bool {
	if len(existing) != len(desired) {
		return false
	}
	byID := make(map[int64]db.ControllerOutput, len(existing))
	for _, output := range existing {
		byID[output.ID] = output
	}
	for _, output := range desired {
		old, ok := byID[output.ID]
		if !ok || old.DisplayOrder != output.DisplayOrder || old.Name != output.Name || old.PixelCount != output.PixelCount || old.Direction != output.Direction || old.Pattern != output.Pattern || old.Rows != output.Rows || old.Columns != output.Columns {
			return false
		}
		var oldCustom []int64
		if err := json.Unmarshal([]byte(old.CustomOrderJson), &oldCustom); err != nil {
			return false
		}
		if !reflect.DeepEqual(oldCustom, output.CustomOrder) {
			return false
		}
	}
	return true
}

func topologyWithControllerOutputs(current cabinetpixels.Topology, controllerID int64, outputs []controllerconfig.Output, stringsByOutput map[int64]db.CabinetLedString, sectionsByString map[int64][]db.CabinetLedPathSection, spans []cabinetpixels.Span, total int64) (cabinetpixels.Topology, error) {
	spanByController := make(map[int64]cabinetpixels.Span, len(spans))
	for _, span := range spans {
		spanByController[span.ControllerID] = span
	}
	out := current
	out.Total = total
	for controllerIndex := range out.Controllers {
		controller := &out.Controllers[controllerIndex]
		controller.Span = spanByController[controller.Record.ID]
		if controller.Record.ID != controllerID {
			continue
		}
		controller.Strings = make([]cabinetpixels.String, 0, len(outputs))
		localOffset := int64(0)
		for outputIndex, output := range outputs {
			record, exists := stringsByOutput[output.ID]
			if !exists {
				record.ID = -int64(outputIndex + 1)
				record.CabinetID = current.CabinetID
				record.ControllerID = controllerID
			}
			record.Name = output.Name
			record.DisplayOrder = output.DisplayOrder
			record.PixelCount = output.PixelCount
			controller.Strings = append(controller.Strings, cabinetpixels.String{
				Record: record, ControllerStart: localOffset + 1,
				CabinetStart: controller.Start + localOffset,
				Sections:     sectionsByString[record.ID],
			})
			localOffset += output.PixelCount
		}
	}
	return out, nil
}

func syncCabinetOutputProjection(ctx context.Context, q db.Querier, cabinetID, controllerID int64, outputs []controllerconfig.Output, outputIDByOrder map[int64]int64, stringsByOutput map[int64]db.CabinetLedString, sectionsByString map[int64][]db.CabinetLedPathSection, temporaryOrderBase int64, confirmMappingImpact bool) error {
	oldStrings, err := q.ListCabinetLEDStringsByController(ctx, db.ListCabinetLEDStringsByControllerParams{CabinetID: cabinetID, ControllerID: controllerID})
	if err != nil {
		return err
	}
	outputIDs := make(map[int64]bool, len(outputs))
	for _, output := range outputs {
		outputIDs[output.ID] = true
	}
	for index, record := range oldStrings {
		outputID := record.ControllerOutputID.Int64
		if !record.ControllerOutputID.Valid {
			for candidateID, candidate := range stringsByOutput {
				if candidate.ID == record.ID {
					outputID = candidateID
					break
				}
			}
		}
		if !outputIDs[outputID] {
			sections := sectionsByString[record.ID]
			if len(sections) > 0 && !confirmMappingImpact {
				return fmt.Errorf("%w: removing output %q would discard its cabinet path", ErrUnsafeLayout, record.Name)
			}
			if err := q.DeleteCabinetLEDPathSectionsByString(ctx, record.ID); err != nil {
				return err
			}
			if _, err := q.DeleteCabinetLEDString(ctx, db.DeleteCabinetLEDStringParams{ID: record.ID, CabinetID: cabinetID, ControllerID: controllerID}); err != nil {
				return err
			}
			continue
		}
		_, err := q.UpdateCabinetLEDString(ctx, db.UpdateCabinetLEDStringParams{
			Name: record.Name, DisplayOrder: temporaryOrderBase + int64(index), PixelCount: record.PixelCount,
			LayoutComplete: record.LayoutComplete, ControllerOutputID: sql.NullInt64{Int64: outputID, Valid: outputID > 0},
			ID: record.ID, CabinetID: cabinetID, ControllerID: controllerID,
		})
		if err != nil {
			return err
		}
	}
	for _, output := range outputs {
		outputID := outputIDByOrder[output.DisplayOrder]
		record, exists := stringsByOutput[outputID]
		layoutComplete := int64(0)
		if exists {
			layoutComplete = record.LayoutComplete
			var pathPixels int64
			for _, section := range sectionsByString[record.ID] {
				if section.PixelCount > math.MaxInt64-pathPixels {
					return ErrLayoutInvalid
				}
				pathPixels += section.PixelCount
			}
			if pathPixels > output.PixelCount {
				return fmt.Errorf("%w: output %q is shorter than its saved cabinet path", ErrUnsafeLayout, output.Name)
			}
			if layoutComplete == 1 && pathPixels != output.PixelCount {
				layoutComplete = 0
			}
		}
		if !exists {
			if _, err := q.CreateCabinetLEDString(ctx, db.CreateCabinetLEDStringParams{
				CabinetID: cabinetID, ControllerID: controllerID, Name: output.Name,
				DisplayOrder: temporaryOrderBase + int64(len(oldStrings)) + output.DisplayOrder,
				PixelCount:   output.PixelCount, LayoutComplete: 0,
				ControllerOutputID: sql.NullInt64{Int64: outputID, Valid: true},
			}); err != nil {
				return err
			}
			continue
		}
		if _, err := q.UpdateCabinetLEDString(ctx, db.UpdateCabinetLEDStringParams{
			Name: output.Name, DisplayOrder: temporaryOrderBase + int64(len(oldStrings)) + output.DisplayOrder,
			PixelCount: output.PixelCount, LayoutComplete: layoutComplete,
			ControllerOutputID: sql.NullInt64{Int64: outputID, Valid: true},
			ID:                 record.ID, CabinetID: cabinetID, ControllerID: controllerID,
		}); err != nil {
			return err
		}
	}
	for _, output := range outputs {
		outputID := outputIDByOrder[output.DisplayOrder]
		record, exists := stringsByOutput[outputID]
		if !exists {
			rows, err := q.ListCabinetLEDStringsByController(ctx, db.ListCabinetLEDStringsByControllerParams{CabinetID: cabinetID, ControllerID: controllerID})
			if err != nil {
				return err
			}
			for _, candidate := range rows {
				if candidate.ControllerOutputID.Valid && candidate.ControllerOutputID.Int64 == outputID {
					record = candidate
					break
				}
			}
		}
		if record.ID == 0 {
			return fmt.Errorf("%w: new output projection could not be located", ErrUnsafeLayout)
		}
		layoutComplete := record.LayoutComplete
		var pathPixels int64
		for _, section := range sectionsByString[record.ID] {
			pathPixels += section.PixelCount
		}
		if layoutComplete == 1 && pathPixels != output.PixelCount {
			layoutComplete = 0
		}
		if _, err := q.UpdateCabinetLEDString(ctx, db.UpdateCabinetLEDStringParams{
			Name: output.Name, DisplayOrder: output.DisplayOrder, PixelCount: output.PixelCount,
			LayoutComplete: layoutComplete, ControllerOutputID: sql.NullInt64{Int64: outputID, Valid: true},
			ID: record.ID, CabinetID: cabinetID, ControllerID: controllerID,
		}); err != nil {
			return err
		}
	}
	return nil
}
