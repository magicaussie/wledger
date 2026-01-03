package mapper

import (
	"encoding/json"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
)

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

// CalculateGlobalIndex determines the WLED segment and absolute LED index for a bin.
// It assumes the `containers` slice is sorted in physical wiring order.
func CalculateGlobalIndex(containers []db.Container, targetBin db.Bin) (int64, int64, error) {
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

	return targetContainer.SegmentID, targetBin.LedIndex.Int64, nil
}

func GetContainerLength(c db.Container) (int64, error) {
	if !c.ConfigJson.Valid {
		return 0, nil
	}

	var config ContainerConfig
	if err := json.Unmarshal([]byte(c.ConfigJson.String), &config); err != nil {
		return 0, err
	}

	switch config.Type {
	case "linear":
		return int64(config.Total), nil
	case "grid":
		return int64(config.Rows * config.Cols), nil
	case "compound":
		var total int64
		for _, s := range config.Sections {
			total += int64(s.Rows * s.Cols)
		}
		return total, nil
	default:
		// Fallback for legacy or untyped grid configs
		if config.Rows > 0 && config.Cols > 0 {
			return int64(config.Rows * config.Cols), nil
		}
		return 0, nil
	}
}
