// Package cabinetpixels translates the cabinet-wide logical pixel namespace to
// controller-local 1-based coordinates. It deliberately knows nothing about
// physical drawer order, wiring paths, WLED outputs or WLED segments.
package cabinetpixels

import (
	"errors"
	"math"
	"sort"
)

var ErrInvalidTopology = errors.New("invalid cabinet pixel topology")
var ErrRangeOutOfBounds = errors.New("cabinet pixel range is outside configured capacity")

type Capacity struct {
	ControllerID int64
	Order        int64
	Pixels       int64
}

type Span struct {
	ControllerID int64
	Order        int64
	Start        int64 // cabinet-wide, 1-based
	Pixels       int64
}

type LocalRange struct {
	ControllerID int64
	Start        int64 // controller-local, 1-based
	Count        int64
}

// BuildSpans assigns each controller a deterministic, contiguous cabinet-wide
// range in explicit order. Zero-capacity controllers are allowed during setup,
// but cannot contain an allocation.
func BuildSpans(capacities []Capacity) ([]Span, int64, error) {
	ordered := append([]Capacity(nil), capacities...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Order == ordered[j].Order {
			return ordered[i].ControllerID < ordered[j].ControllerID
		}
		return ordered[i].Order < ordered[j].Order
	})
	seenIDs := make(map[int64]struct{}, len(ordered))
	seenOrders := make(map[int64]struct{}, len(ordered))
	total := int64(0)
	spans := make([]Span, 0, len(ordered))
	for _, capacity := range ordered {
		if capacity.ControllerID <= 0 || capacity.Order < 0 || capacity.Pixels < 0 {
			return nil, 0, ErrInvalidTopology
		}
		if _, exists := seenIDs[capacity.ControllerID]; exists {
			return nil, 0, ErrInvalidTopology
		}
		if _, exists := seenOrders[capacity.Order]; exists {
			return nil, 0, ErrInvalidTopology
		}
		seenIDs[capacity.ControllerID] = struct{}{}
		seenOrders[capacity.Order] = struct{}{}
		if total == math.MaxInt64 || capacity.Pixels > math.MaxInt64-total {
			return nil, 0, ErrInvalidTopology
		}
		spans = append(spans, Span{
			ControllerID: capacity.ControllerID,
			Order:        capacity.Order,
			Start:        total + 1,
			Pixels:       capacity.Pixels,
		})
		total += capacity.Pixels
	}
	return spans, total, nil
}

// SplitRange converts one cabinet-wide range to its controller-local pieces.
// String boundaries are intentionally absent here: strings describe physical
// wiring and do not change controller-local numbering.
func SplitRange(start, count int64, spans []Span) ([]LocalRange, error) {
	if start < 1 || count < 1 || count > math.MaxInt64-start+1 {
		return nil, ErrRangeOutOfBounds
	}
	end := start + count - 1
	var total int64
	for _, span := range spans {
		if span.Pixels > math.MaxInt64-total {
			return nil, ErrInvalidTopology
		}
		total += span.Pixels
	}
	if end > total {
		return nil, ErrRangeOutOfBounds
	}
	pieces := make([]LocalRange, 0, len(spans))
	for _, span := range spans {
		if span.Pixels == 0 {
			continue
		}
		spanEnd := span.Start + span.Pixels - 1
		left := max(start, span.Start)
		right := min(end, spanEnd)
		if left > right {
			continue
		}
		pieces = append(pieces, LocalRange{
			ControllerID: span.ControllerID,
			Start:        left - span.Start + 1,
			Count:        right - left + 1,
		})
	}
	if len(pieces) == 0 {
		return nil, ErrRangeOutOfBounds
	}
	return pieces, nil
}
