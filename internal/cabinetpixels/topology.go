package cabinetpixels

import (
	"context"
	"fmt"
	"math"

	"github.com/tuxedocurly/wledger/internal/db"
)

type String struct {
	Record          db.CabinetLedString
	ControllerStart int64 // string's first controller-local pixel, 1-based
	CabinetStart    int64 // string's first cabinet-wide pixel, 1-based
	Sections        []db.CabinetLedPathSection
}

type Controller struct {
	Record db.Controller
	Span
	Strings []String
}

type Topology struct {
	CabinetID   int64
	Controllers []Controller
	Total       int64
}

type PhysicalPiece struct {
	ControllerID int64
	StringID     int64 // 0 denotes an unstructured legacy controller range
	CabinetStart int64
	LocalStart   int64 // controller-local, 1-based
	StringStart  int64 // string-local, 1-based; equals LocalStart for legacy ranges
	Count        int64
}

// SplitTopologyRange maps a cabinet-wide range to ordered physical-string
// intervals while retaining the controller-local address required by WLED.
func SplitTopologyRange(topology Topology, start, count int64) ([]PhysicalPiece, error) {
	controllerPieces, err := SplitRange(start, count, spansOf(topology.Controllers))
	if err != nil {
		return nil, err
	}
	pieces := make([]PhysicalPiece, 0, len(controllerPieces))
	for _, local := range controllerPieces {
		var controller *Controller
		for i := range topology.Controllers {
			if topology.Controllers[i].Record.ID == local.ControllerID {
				controller = &topology.Controllers[i]
				break
			}
		}
		if controller == nil {
			return nil, ErrInvalidTopology
		}
		localEnd := local.Start + local.Count - 1
		if len(controller.Strings) == 0 {
			pieces = append(pieces, PhysicalPiece{
				ControllerID: local.ControllerID,
				LocalStart:   local.Start, StringStart: local.Start, Count: local.Count,
				CabinetStart: controller.Start + local.Start - 1,
			})
			continue
		}
		for _, string := range controller.Strings {
			stringLocalStart := string.ControllerStart
			stringLocalEnd := stringLocalStart + string.Record.PixelCount - 1
			left := max(local.Start, stringLocalStart)
			right := min(localEnd, stringLocalEnd)
			if left > right {
				continue
			}
			pieces = append(pieces, PhysicalPiece{
				ControllerID: local.ControllerID,
				StringID:     string.Record.ID,
				CabinetStart: controller.Start + left - 1,
				LocalStart:   left,
				StringStart:  left - stringLocalStart + 1,
				Count:        right - left + 1,
			})
		}
	}
	var mapped int64
	for _, piece := range pieces {
		mapped += piece.Count
	}
	if mapped != count {
		return nil, ErrInvalidTopology
	}
	return pieces, nil
}

func spansOf(controllers []Controller) []Span {
	spans := make([]Span, 0, len(controllers))
	for _, controller := range controllers {
		spans = append(spans, controller.Span)
	}
	return spans
}

// Load builds the cabinet-wide address map using controller slots and string
// order. Existing installations whose membership is not marked
// strings_configured retain their stored controller capacity as a legacy
// fallback. Once explicitly configured, the sum of string lengths (including
// an empty set's zero capacity) is authoritative and must match led_count.
func Load(ctx context.Context, q db.Querier, cabinetID int64) (Topology, error) {
	members, err := q.ListCabinetControllers(ctx, cabinetID)
	if err != nil {
		return Topology{}, fmt.Errorf("read cabinet controller order: %w", err)
	}
	capacities := make([]Capacity, 0, len(members))
	controllers := make(map[int64]db.Controller, len(members))
	stringsByController := make(map[int64][]db.CabinetLedString, len(members))
	sectionsByString := make(map[int64][]db.CabinetLedPathSection)
	allSections, err := q.ListCabinetLEDPathSections(ctx, cabinetID)
	if err != nil {
		return Topology{}, fmt.Errorf("read cabinet wiring sections: %w", err)
	}
	for _, section := range allSections {
		sectionsByString[section.StringID] = append(sectionsByString[section.StringID], section)
	}
	for _, member := range members {
		controller, err := q.GetController(ctx, member.ControllerID)
		if err != nil {
			return Topology{}, fmt.Errorf("read cabinet controller #%d: %w", member.ControllerID, err)
		}
		strings, err := q.ListCabinetLEDStringsByController(ctx, db.ListCabinetLEDStringsByControllerParams{CabinetID: cabinetID, ControllerID: member.ControllerID})
		if err != nil {
			return Topology{}, fmt.Errorf("read strings for controller #%d: %w", member.ControllerID, err)
		}
		capacity := controller.LedCount
		if member.StringsConfigured == 1 {
			capacity = 0
			for _, stringRecord := range strings {
				if stringRecord.PixelCount < 1 || stringRecord.PixelCount > math.MaxInt64-capacity {
					return Topology{}, fmt.Errorf("%w: controller #%d has invalid string capacity", ErrInvalidTopology, controller.ID)
				}
				capacity += stringRecord.PixelCount
			}
			if controller.LedCount != capacity {
				return Topology{}, fmt.Errorf("%w: controller #%d stored capacity %d does not match its string total %d", ErrInvalidTopology, controller.ID, controller.LedCount, capacity)
			}
		} else if len(strings) != 0 {
			return Topology{}, fmt.Errorf("%w: controller #%d has strings but is not marked configured", ErrInvalidTopology, controller.ID)
		}
		if capacity < 0 {
			return Topology{}, fmt.Errorf("%w: controller #%d has negative capacity", ErrInvalidTopology, controller.ID)
		}
		capacities = append(capacities, Capacity{ControllerID: controller.ID, Order: member.Slot, Pixels: capacity})
		controllers[controller.ID] = controller
		stringsByController[controller.ID] = strings
	}
	spans, total, err := BuildSpans(capacities)
	if err != nil {
		return Topology{}, err
	}
	spanByController := make(map[int64]Span, len(spans))
	for _, span := range spans {
		spanByController[span.ControllerID] = span
	}
	out := Topology{CabinetID: cabinetID, Total: total, Controllers: make([]Controller, 0, len(members))}
	for _, member := range members {
		span := spanByController[member.ControllerID]
		controller := Controller{Record: controllers[member.ControllerID], Span: span, Strings: make([]String, 0, len(stringsByController[member.ControllerID]))}
		localOffset := int64(0)
		for _, stringRecord := range stringsByController[member.ControllerID] {
			controller.Strings = append(controller.Strings, String{
				Record:          stringRecord,
				ControllerStart: localOffset + 1,
				CabinetStart:    span.Start + localOffset,
				Sections:        sectionsByString[stringRecord.ID],
			})
			localOffset += stringRecord.PixelCount
		}
		out.Controllers = append(out.Controllers, controller)
	}
	return out, nil
}
