package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

type WiringPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type WiringSectionInput struct {
	ID           int64         `json:"id"`
	DisplayOrder int64         `json:"display_order"`
	PixelCount   int64         `json:"pixel_count"`
	Points       []WiringPoint `json:"points"`
}

type WiringStringInput struct {
	ID             int64                `json:"id"`
	ControllerID   int64                `json:"controller_id"`
	Name           string               `json:"name"`
	DisplayOrder   int64                `json:"display_order"`
	PixelCount     int64                `json:"pixel_count"`
	LayoutComplete bool                 `json:"layout_complete"`
	Sections       []WiringSectionInput `json:"sections"`
}

type CabinetWiringInput struct {
	Revision                int64               `json:"revision"`
	ConfiguredControllerIDs []int64             `json:"configured_controller_ids"`
	Strings                 []WiringStringInput `json:"strings"`
}

type wiringSectionGeometry struct {
	PixelCount int64         `json:"pixel_count"`
	Points     []WiringPoint `json:"points"`
}

// SaveCabinetWiring saves ordered physical strings and path sections without
// modifying drawers or inventory. Capacity is derived from string lengths for
// controllers explicitly marked configured. Existing legacy capacities remain
// unchanged until the user configures strings for that controller.
func (s *LayoutService) SaveCabinetWiring(ctx context.Context, cabinetID int64, input CabinetWiringInput, confirmMappingImpact bool) error {
	if cabinetID <= 0 {
		return ErrLayoutInvalid
	}
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		cabinet, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cabinet.ElectricalRevision != input.Revision {
			return ErrLEDAllocationStale
		}
		currentTopology, err := cabinetpixels.Load(ctx, q, cabinetID)
		if err != nil {
			return fmt.Errorf("%w: current cabinet wiring is invalid: %v", ErrUnsafeLayout, err)
		}
		members, err := q.ListCabinetControllers(ctx, cabinetID)
		if err != nil {
			return err
		}
		memberByID := make(map[int64]db.CabinetController, len(members))
		for _, member := range members {
			memberByID[member.ControllerID] = member
		}
		configured := make(map[int64]bool, len(input.ConfiguredControllerIDs))
		for _, id := range input.ConfiguredControllerIDs {
			if _, ok := memberByID[id]; !ok || configured[id] {
				return fmt.Errorf("%w: wiring configuration names an unassigned or duplicate controller #%d", ErrLEDControllerConfiguration, id)
			}
			configured[id] = true
		}
		allStrings, err := q.ListCabinetLEDStrings(ctx, cabinetID)
		if err != nil {
			return err
		}
		oldStrings := make(map[int64]db.CabinetLedString, len(allStrings))
		oldSections := make(map[int64][]db.CabinetLedPathSection)
		for _, record := range allStrings {
			oldStrings[record.ID] = record
			sections, err := q.ListCabinetLEDPathSectionsByString(ctx, record.ID)
			if err != nil {
				return err
			}
			oldSections[record.ID] = sections
		}
		seenStrings := make(map[int64]bool, len(input.Strings))
		seenStringOrders := make(map[[2]int64]bool, len(input.Strings))
		newPixelsByController := make(map[int64]int64, len(members))
		for i, item := range input.Strings {
			if item.ID < 0 || item.ControllerID <= 0 || item.PixelCount < 1 || item.DisplayOrder < 0 || strings.TrimSpace(item.Name) == "" || len(strings.TrimSpace(item.Name)) > 200 {
				return fmt.Errorf("%w: string %d has invalid identity, name, order or pixel count", ErrLayoutInvalid, i+1)
			}
			member, ok := memberByID[item.ControllerID]
			if !ok {
				return fmt.Errorf("%w: string %q belongs to a controller not assigned to this cabinet", ErrLEDControllerConfiguration, item.Name)
			}
			if member.StringsConfigured != 1 && !configured[item.ControllerID] {
				return fmt.Errorf("%w: explicitly configure controller #%d before adding physical strings", ErrLEDControllerConfiguration, item.ControllerID)
			}
			if item.ID > 0 {
				old, exists := oldStrings[item.ID]
				if !exists || old.ControllerID != item.ControllerID || seenStrings[item.ID] {
					return fmt.Errorf("%w: string %d is unknown, duplicated, or moved between controllers", ErrLayoutInvalid, item.ID)
				}
				seenStrings[item.ID] = true
			}
			orderKey := [2]int64{item.ControllerID, item.DisplayOrder}
			if seenStringOrders[orderKey] {
				return fmt.Errorf("%w: controller #%d has duplicate string order %d", ErrLayoutInvalid, item.ControllerID, item.DisplayOrder)
			}
			seenStringOrders[orderKey] = true
			if item.PixelCount > math.MaxInt64-newPixelsByController[item.ControllerID] {
				return fmt.Errorf("%w: controller string total overflows", ErrLayoutInvalid)
			}
			newPixelsByController[item.ControllerID] += item.PixelCount
			if err := validateWiringSections(item, cabinet.LayoutWidth, cabinet.LayoutHeight); err != nil {
				return fmt.Errorf("%w: string %q: %v", ErrLayoutInvalid, item.Name, err)
			}
		}

		newCapacities := make([]cabinetpixels.Capacity, 0, len(members))
		for _, member := range members {
			controller, err := q.GetController(ctx, member.ControllerID)
			if err != nil {
				return err
			}
			capacity := controller.LedCount
			if member.StringsConfigured == 1 || configured[member.ControllerID] {
				capacity = newPixelsByController[member.ControllerID]
			}
			newCapacities = append(newCapacities, cabinetpixels.Capacity{ControllerID: member.ControllerID, Order: member.Slot, Pixels: capacity})
		}
		newSpans, newTotal, err := cabinetpixels.BuildSpans(newCapacities)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrLayoutInvalid, err)
		}
		effectiveConfigured := make(map[int64]bool, len(configured)+len(members))
		for id := range configured {
			effectiveConfigured[id] = true
		}
		for _, member := range members {
			if member.StringsConfigured == 1 {
				effectiveConfigured[member.ControllerID] = true
			}
		}
		afterTopology := topologyWithProposedStrings(currentTopology, input, effectiveConfigured, newSpans, newTotal)
		structureChanged := configuredWiringChanged(currentTopology, input, configured, members)
		allocations, err := q.ListDrawerLEDAllocationsByCabinet(ctx, cabinetID)
		if err != nil {
			return err
		}
		mappingImpact := structureChanged && len(allocations) > 0
		var changedDrawerIDs []int64
		for _, allocation := range allocations {
			if structureChanged {
				changedDrawerIDs = append(changedDrawerIDs, allocation.DrawerID)
			}
			if _, err := cabinetpixels.SplitRange(allocation.StartPixel, allocation.PixelCount, newSpans); err != nil {
				return fmt.Errorf("%w: new wiring capacity %d would put drawer #%d range %d–%d out of bounds", ErrUnsafeLayout, newTotal, allocation.DrawerID, allocation.StartPixel, allocation.StartPixel+allocation.PixelCount-1)
			}
			before, err := cabinetpixels.SplitTopologyRange(currentTopology, allocation.StartPixel, allocation.PixelCount)
			if err != nil {
				return fmt.Errorf("%w: current drawer #%d range is outside cabinet capacity", ErrUnsafeLayout, allocation.DrawerID)
			}
			after, err := cabinetpixels.SplitTopologyRange(afterTopology, allocation.StartPixel, allocation.PixelCount)
			if err != nil {
				return fmt.Errorf("%w: proposed wiring cannot resolve drawer #%d's pixel range", ErrUnsafeLayout, allocation.DrawerID)
			}
			if !reflect.DeepEqual(before, after) {
				mappingImpact = true
				changedDrawerIDs = append(changedDrawerIDs, allocation.DrawerID)
			}
		}
		if mappingImpact && !confirmMappingImpact {
			return fmt.Errorf("%w: the proposed string order or lengths change physical destinations for drawer mappings %v; review the before/after preview and confirm explicitly", ErrUnsafeLayout, uniqueIDs(changedDrawerIDs))
		}

		// Replace path sections inside this transaction, preserving IDs and
		// creation times for rows that the editor submitted back unchanged.
		for _, old := range allStrings {
			if err := q.DeleteCabinetLEDPathSectionsByString(ctx, old.ID); err != nil {
				return err
			}
			if _, err := q.DeleteCabinetLEDString(ctx, db.DeleteCabinetLEDStringParams{ID: old.ID, CabinetID: cabinetID, ControllerID: old.ControllerID}); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		for _, item := range input.Strings {
			var stringID int64
			if item.ID == 0 {
				stringID, err = q.CreateCabinetLEDString(ctx, db.CreateCabinetLEDStringParams{
					CabinetID: cabinetID, ControllerID: item.ControllerID, Name: strings.TrimSpace(item.Name),
					DisplayOrder: item.DisplayOrder, PixelCount: item.PixelCount, LayoutComplete: boolInt64(item.LayoutComplete),
				})
				if err != nil {
					return err
				}
			} else {
				old := oldStrings[item.ID]
				stringID = old.ID
				if err := q.RestoreCabinetLEDString(ctx, db.RestoreCabinetLEDStringParams{
					ID: old.ID, CabinetID: cabinetID, ControllerID: item.ControllerID, Name: strings.TrimSpace(item.Name),
					DisplayOrder: item.DisplayOrder, PixelCount: item.PixelCount, LayoutComplete: boolInt64(item.LayoutComplete),
					CreatedAt: old.CreatedAt, UpdatedAt: now,
				}); err != nil {
					return err
				}
			}
			oldSectionByID := make(map[int64]db.CabinetLedPathSection)
			for _, old := range oldSections[item.ID] {
				oldSectionByID[old.ID] = old
			}
			seenSections := make(map[int64]bool, len(item.Sections))
			for _, section := range item.Sections {
				pathPoints := section.Points
				if pathPoints == nil {
					pathPoints = []WiringPoint{}
				}
				points, err := json.Marshal(pathPoints)
				if err != nil {
					return err
				}
				if section.ID > 0 {
					old, ok := oldSectionByID[section.ID]
					if !ok || seenSections[section.ID] {
						return fmt.Errorf("%w: section #%d is unknown or duplicated", ErrLayoutInvalid, section.ID)
					}
					seenSections[section.ID] = true
					if err := q.RestoreCabinetLEDPathSection(ctx, db.RestoreCabinetLEDPathSectionParams{
						ID: old.ID, StringID: stringID, DisplayOrder: section.DisplayOrder, PixelCount: section.PixelCount,
						PointsJson: string(points), CreatedAt: old.CreatedAt, UpdatedAt: now,
					}); err != nil {
						return err
					}
				} else if _, err := q.CreateCabinetLEDPathSection(ctx, db.CreateCabinetLEDPathSectionParams{
					StringID: stringID, DisplayOrder: section.DisplayOrder, PixelCount: section.PixelCount, PointsJson: string(points),
				}); err != nil {
					return err
				}
			}
		}
		for id := range configured {
			rows, err := q.UpdateCabinetControllerStringsConfigured(ctx, db.UpdateCabinetControllerStringsConfiguredParams{StringsConfigured: 1, CabinetID: cabinetID, ControllerID: id})
			if err != nil {
				return err
			}
			if rows != 1 {
				return ErrLEDControllerConfiguration
			}
		}
		for _, member := range members {
			if member.StringsConfigured != 1 && !configured[member.ControllerID] {
				continue
			}
			if _, err := q.SetControllerLEDCapacityFromStrings(ctx, db.SetControllerLEDCapacityFromStringsParams{LedCount: newPixelsByController[member.ControllerID], ID: member.ControllerID}); err != nil {
				return err
			}
		}
		rows, err := q.AdvanceCabinetElectricalRevision(ctx, db.AdvanceCabinetElectricalRevisionParams{ID: cabinetID, ElectricalRevision: input.Revision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLEDAllocationStale
		}
		return nil
	})
}

func validateWiringSections(item WiringStringInput, maxWidth, maxHeight float64) error {
	if item.LayoutComplete && len(item.Sections) == 0 {
		return fmt.Errorf("a complete path requires at least one ordered section")
	}
	var total int64
	seenOrders := make(map[int64]bool, len(item.Sections))
	seenIDs := make(map[int64]bool, len(item.Sections))
	for index, section := range item.Sections {
		if section.ID < 0 || section.DisplayOrder < 0 || section.PixelCount < 1 || seenOrders[section.DisplayOrder] {
			return fmt.Errorf("section %d has invalid identity, order, or pixel count", index+1)
		}
		if section.ID > 0 && seenIDs[section.ID] {
			return fmt.Errorf("section identity %d is duplicated", section.ID)
		}
		seenIDs[section.DisplayOrder] = true
		seenOrders[section.DisplayOrder] = true
		if section.PixelCount > math.MaxInt64-total {
			return fmt.Errorf("section pixel total overflows")
		}
		total += section.PixelCount
		if len(section.Points) != 0 && len(section.Points) < 2 {
			return fmt.Errorf("section %d path needs at least two ordered points", index+1)
		}
		if item.LayoutComplete && len(section.Points) < 2 {
			return fmt.Errorf("complete section %d needs at least two ordered points", index+1)
		}
		for _, point := range section.Points {
			if math.IsNaN(point.X) || math.IsNaN(point.Y) || math.IsInf(point.X, 0) || math.IsInf(point.Y, 0) || point.X < 0 || point.Y < 0 || point.X > maxWidth || point.Y > maxHeight {
				return fmt.Errorf("section %d has a point outside the cabinet canvas", index+1)
			}
		}
	}
	if total > item.PixelCount {
		return fmt.Errorf("path sections account for %d pixels, exceeding string length %d", total, item.PixelCount)
	}
	if item.LayoutComplete && total != item.PixelCount {
		return fmt.Errorf("complete path accounts for %d pixels, but string length is %d", total, item.PixelCount)
	}
	return nil
}

func topologyWithProposedStrings(current cabinetpixels.Topology, input CabinetWiringInput, configured map[int64]bool, spans []cabinetpixels.Span, total int64) cabinetpixels.Topology {
	out := cabinetpixels.Topology{CabinetID: current.CabinetID, Total: total, Controllers: make([]cabinetpixels.Controller, 0, len(current.Controllers))}
	spanByID := make(map[int64]cabinetpixels.Span, len(spans))
	for _, span := range spans {
		spanByID[span.ControllerID] = span
	}
	byID := make(map[int64][]WiringStringInput)
	for _, item := range input.Strings {
		byID[item.ControllerID] = append(byID[item.ControllerID], item)
	}
	for _, oldController := range current.Controllers {
		controller := cabinetpixels.Controller{Record: oldController.Record, Span: spanByID[oldController.Record.ID]}
		if !configured[oldController.Record.ID] {
			controller.Strings = oldController.Strings
			out.Controllers = append(out.Controllers, controller)
			continue
		}
		localOffset := int64(0)
		items := byID[oldController.Record.ID]
		sort.Slice(items, func(i, j int) bool { return items[i].DisplayOrder < items[j].DisplayOrder })
		for itemIndex, item := range items {
			stringID := item.ID
			if stringID == 0 {
				stringID = -int64(itemIndex + 1)
			}
			sections := make([]db.CabinetLedPathSection, 0, len(item.Sections))
			for index, section := range item.Sections {
				points, _ := json.Marshal(section.Points)
				sections = append(sections, db.CabinetLedPathSection{ID: section.ID, StringID: stringID, DisplayOrder: int64(index), PixelCount: section.PixelCount, PointsJson: string(points)})
			}
			controller.Strings = append(controller.Strings, cabinetpixels.String{
				Record:          db.CabinetLedString{ID: stringID, CabinetID: current.CabinetID, ControllerID: oldController.Record.ID, Name: item.Name, DisplayOrder: item.DisplayOrder, PixelCount: item.PixelCount, LayoutComplete: boolInt64(item.LayoutComplete)},
				ControllerStart: localOffset + 1, CabinetStart: controller.Start + localOffset, Sections: sections,
			})
			localOffset += item.PixelCount
		}
		out.Controllers = append(out.Controllers, controller)
	}
	return out
}

func configuredWiringChanged(current cabinetpixels.Topology, input CabinetWiringInput, configured map[int64]bool, members []db.CabinetController) bool {
	newByController := make(map[int64][]WiringStringInput)
	for _, item := range input.Strings {
		newByController[item.ControllerID] = append(newByController[item.ControllerID], item)
	}
	for _, member := range members {
		if member.StringsConfigured != 1 && !configured[member.ControllerID] {
			continue
		}
		var oldStrings []cabinetpixels.String
		for _, controller := range current.Controllers {
			if controller.Record.ID == member.ControllerID {
				oldStrings = controller.Strings
				break
			}
		}
		newStrings := newByController[member.ControllerID]
		sort.Slice(newStrings, func(i, j int) bool { return newStrings[i].DisplayOrder < newStrings[j].DisplayOrder })
		if member.StringsConfigured != 1 || len(oldStrings) != len(newStrings) {
			return true
		}
		for i, next := range newStrings {
			old := oldStrings[i]
			if old.Record.ID != next.ID || old.Record.DisplayOrder != next.DisplayOrder || old.Record.PixelCount != next.PixelCount || (old.Record.LayoutComplete == 1) != next.LayoutComplete || len(old.Sections) != len(next.Sections) {
				return true
			}
			for j, section := range next.Sections {
				oldSection := old.Sections[j]
				var oldPoints []WiringPoint
				if err := json.Unmarshal([]byte(oldSection.PointsJson), &oldPoints); err != nil {
					return true
				}
				if oldPoints == nil {
					oldPoints = []WiringPoint{}
				}
				newPoints := section.Points
				if newPoints == nil {
					newPoints = []WiringPoint{}
				}
				if oldSection.DisplayOrder != section.DisplayOrder || oldSection.PixelCount != section.PixelCount || !reflect.DeepEqual(oldPoints, newPoints) {
					return true
				}
			}
		}
	}
	return false
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
