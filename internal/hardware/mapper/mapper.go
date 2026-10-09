package mapper

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// ErrUnresolvedCoordinateSpace is returned when a bin's stored LED index cannot
// be mapped because the coordinate system of stored indices is unresolved.
var ErrUnresolvedCoordinateSpace = errors.New("LED coordinate space is unresolved")

type ContainerConfig struct {
	Type     string    `json:"type"`     // "linear", "grid", "compound"
	Rows     int       `json:"rows"`     // for grid
	Cols     int       `json:"cols"`     // for grid
	Total    int       `json:"total"`    // for linear
	Sections []Section `json:"sections"` // for compound
}

type Section struct {
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

// Length returns the number of LEDs the layout describes (its capacity).
// It is a layout estimate, not a guarantee of the mapped LED span.
func (c ContainerConfig) Length() int64 {
	switch c.Type {
	case "linear":
		return int64(c.Total)
	case "grid":
		return int64(c.Rows * c.Cols)
	case "compound":
		var total int64
		for _, s := range c.Sections {
			total += int64(s.Rows * s.Cols)
		}
		return total
	default:
		// Fallback for legacy or untyped grid configs
		if c.Rows > 0 && c.Cols > 0 {
			return int64(c.Rows * c.Cols)
		}
		return 0
	}
}

// CalculateGlobalIndex determines the WLED segment and absolute LED index for a
// bin, interpreting the bin's stored led_index in the given coordinate space.
// It assumes the `containers` slice is sorted in physical wiring order.
//
//   - segment: the stored index is already segment-absolute and is returned as-is.
//   - drawer: the stored index is relative to its drawer, so the drawer's
//     segment-relative allocation start is added.
//   - unresolved: rejected, because the stored index cannot be interpreted.
//
// It validates that the resulting index is a valid, non-negative WLED index and
// that a drawer-relative index lies within its drawer's allocation.
func CalculateGlobalIndex(space string, containers []db.Container, targetBin db.Bin) (int64, int64, error) {
	// Find Target Container
	var targetContainer db.Container
	found := false
	for _, c := range containers {
		if c.ID == targetBin.ContainerID {
			targetContainer = c
			found = true
			break
		}
	}
	if !found {
		return 0, 0, fmt.Errorf("target container %d not found in provided list", targetBin.ContainerID)
	}

	if !targetBin.LedIndex.Valid {
		return 0, 0, fmt.Errorf("target bin has no LED index")
	}
	idx := targetBin.LedIndex.Int64
	if idx < 0 {
		return 0, 0, fmt.Errorf("target bin has negative LED index %d", idx)
	}

	switch space {
	case ledspace.Segment:
		return targetContainer.SegmentID, idx, nil
	case ledspace.Drawer:
		if targetContainer.LedCount <= 0 {
			return 0, 0, fmt.Errorf("drawer %d has no LED allocation", targetContainer.ID)
		}
		if idx >= targetContainer.LedCount {
			return 0, 0, fmt.Errorf("bin LED index %d is outside drawer allocation [0,%d)", idx, targetContainer.LedCount)
		}
		if targetContainer.LedStart > math.MaxInt64-idx {
			return 0, 0, fmt.Errorf("drawer LED index overflows: start %d index %d", targetContainer.LedStart, idx)
		}
		return targetContainer.SegmentID, targetContainer.LedStart + idx, nil
	case ledspace.Unresolved:
		return 0, 0, ErrUnresolvedCoordinateSpace
	default:
		return 0, 0, fmt.Errorf("unknown LED coordinate space %q", space)
	}
}

func GetContainerLength(c db.Container) (int64, error) {
	if !c.ConfigJson.Valid {
		return 0, nil
	}

	var config ContainerConfig
	if err := json.Unmarshal([]byte(c.ConfigJson.String), &config); err != nil {
		return 0, err
	}

	return config.Length(), nil
}
