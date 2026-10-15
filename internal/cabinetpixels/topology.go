package cabinetpixels

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
)

type String struct {
	Record          db.CabinetLedString
	ControllerStart int64 // string's first controller-local pixel, 1-based
	CabinetStart    int64 // string's first cabinet-wide pixel, 1-based
	Sections        []db.CabinetLedPathSection
	Direction       string
	Pattern         string
	Rows            int64
	Columns         int64
	CustomOrder     []int64
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
			logicalStart := left - stringLocalStart + 1
			logicalEnd := right - stringLocalStart + 1
			if err := validateOutputMapping(string); err != nil {
				return nil, err
			}
			if string.Pattern == "custom" || string.Pattern == "serpentine_rows" || string.Pattern == "serpentine_columns" {
				if string.Record.PixelCount > controllerconfig.MaxPixelsPerOutput {
					return nil, ErrInvalidTopology
				}
				for logical := logicalStart; logical <= logicalEnd; logical++ {
					physical, err := outputPixelIndex(string, logical)
					if err != nil {
						return nil, err
					}
					pixel := PhysicalPiece{
						ControllerID: local.ControllerID, StringID: string.Record.ID,
						CabinetStart: controller.Start + left - 1 + logical - logicalStart,
						LocalStart:   stringLocalStart + physical, StringStart: physical + 1, Count: 1,
					}
					if len(pieces) > 0 {
						previous := &pieces[len(pieces)-1]
						if previous.ControllerID == pixel.ControllerID && previous.StringID == pixel.StringID && previous.LocalStart+previous.Count == pixel.LocalStart && previous.CabinetStart+previous.Count == pixel.CabinetStart {
							previous.Count++
							continue
						}
					}
					pieces = append(pieces, pixel)
				}
				continue
			}
			localStart := left
			stringStart := logicalStart
			if string.Direction == "reverse" {
				localStart = stringLocalStart + string.Record.PixelCount - logicalEnd
				stringStart = string.Record.PixelCount - logicalEnd + 1
			}
			pieces = append(pieces, PhysicalPiece{
				ControllerID: local.ControllerID,
				StringID:     string.Record.ID,
				CabinetStart: controller.Start + left - 1,
				LocalStart:   localStart,
				StringStart:  stringStart,
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

func validateOutputMapping(output String) error {
	pixelCount := output.Record.PixelCount
	if pixelCount < 1 || pixelCount > controllerconfig.MaxPixelsPerOutput {
		return ErrInvalidTopology
	}
	if output.Direction != "" && output.Direction != "forward" && output.Direction != "reverse" {
		return ErrInvalidTopology
	}
	switch output.Pattern {
	case "", "linear":
		if len(output.CustomOrder) != 0 {
			return ErrInvalidTopology
		}
	case "serpentine_rows", "serpentine_columns":
		if output.Rows < 1 || output.Columns < 1 || output.Rows > math.MaxInt64/output.Columns || output.Rows*output.Columns != pixelCount || len(output.CustomOrder) != 0 {
			return ErrInvalidTopology
		}
	case "custom":
		if int64(len(output.CustomOrder)) != pixelCount {
			return ErrInvalidTopology
		}
		seen := make([]bool, len(output.CustomOrder))
		for _, pixel := range output.CustomOrder {
			if pixel < 0 || pixel >= int64(len(seen)) || seen[int(pixel)] {
				return ErrInvalidTopology
			}
			seen[int(pixel)] = true
		}
	default:
		return ErrInvalidTopology
	}
	return nil
}

func outputPixelIndex(output String, logicalPixel int64) (int64, error) {
	pixelCount := output.Record.PixelCount
	if pixelCount < 1 || pixelCount > controllerconfig.MaxPixelsPerOutput || logicalPixel < 1 || logicalPixel > pixelCount {
		return 0, ErrInvalidTopology
	}
	logicalIndex := logicalPixel - 1
	physicalIndex := logicalIndex
	switch output.Pattern {
	case "", "linear":
	case "serpentine_rows":
		if output.Rows < 1 || output.Columns < 1 || output.Rows > math.MaxInt64/output.Columns || output.Rows*output.Columns != pixelCount {
			return 0, ErrInvalidTopology
		}
		row, column := logicalIndex/output.Columns, logicalIndex%output.Columns
		if row%2 == 1 {
			column = output.Columns - 1 - column
		}
		physicalIndex = row*output.Columns + column
	case "serpentine_columns":
		if output.Rows < 1 || output.Columns < 1 || output.Rows > math.MaxInt64/output.Columns || output.Rows*output.Columns != pixelCount {
			return 0, ErrInvalidTopology
		}
		row, column := logicalIndex/output.Columns, logicalIndex%output.Columns
		if column%2 == 1 {
			row = output.Rows - 1 - row
		}
		physicalIndex = column*output.Rows + row
	case "custom":
		if int64(len(output.CustomOrder)) != pixelCount {
			return 0, ErrInvalidTopology
		}
		physicalIndex = output.CustomOrder[logicalIndex]
	default:
		return 0, ErrInvalidTopology
	}
	if physicalIndex < 0 || physicalIndex >= pixelCount {
		return 0, ErrInvalidTopology
	}
	if output.Direction == "reverse" {
		physicalIndex = pixelCount - 1 - physicalIndex
	} else if output.Direction != "" && output.Direction != "forward" {
		return 0, ErrInvalidTopology
	}
	return physicalIndex, nil
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
	outputsByController := make(map[int64]map[int64]db.ControllerOutput, len(members))
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
		outputs, err := q.GetControllerOutputs(ctx, member.ControllerID)
		if err != nil {
			return Topology{}, fmt.Errorf("read outputs for controller #%d: %w", member.ControllerID, err)
		}
		outputByID := make(map[int64]db.ControllerOutput, len(outputs))
		for _, output := range outputs {
			outputByID[output.ID] = output
		}
		outputsByController[member.ControllerID] = outputByID
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
			mapped := String{
				Record: stringRecord, ControllerStart: localOffset + 1,
				CabinetStart: span.Start + localOffset, Sections: sectionsByString[stringRecord.ID],
				Direction: "forward", Pattern: "linear", Rows: 1, Columns: 1,
			}
			if stringRecord.ControllerOutputID.Valid {
				output, ok := outputsByController[member.ControllerID][stringRecord.ControllerOutputID.Int64]
				if !ok || output.ControllerID != member.ControllerID || output.DisplayOrder != stringRecord.DisplayOrder || output.PixelCount != stringRecord.PixelCount {
					return Topology{}, fmt.Errorf("%w: cabinet string #%d does not match its controller output", ErrInvalidTopology, stringRecord.ID)
				}
				mapped.Direction, mapped.Pattern, mapped.Rows, mapped.Columns = output.Direction, output.Pattern, output.Rows, output.Columns
				if err := json.Unmarshal([]byte(output.CustomOrderJson), &mapped.CustomOrder); err != nil {
					return Topology{}, fmt.Errorf("%w: controller output #%d custom pattern is invalid: %v", ErrInvalidTopology, output.ID, err)
				}
			}
			controller.Strings = append(controller.Strings, mapped)
			localOffset += stringRecord.PixelCount
		}
		out.Controllers = append(out.Controllers, controller)
	}
	return out, nil
}
