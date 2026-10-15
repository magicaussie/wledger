package controllerconfig

import (
	"errors"
	"math"
	"sort"
	"strings"
)

const DefaultConnectionTimeoutMS int64 = 2000
const MaxPixelsPerOutput int64 = 100_000

var ErrInvalidConfiguration = errors.New("invalid controller hardware configuration")

type Output struct {
	ID           int64   `json:"id"`
	DisplayOrder int64   `json:"display_order"`
	Name         string  `json:"name"`
	PixelCount   int64   `json:"pixel_count"`
	Direction    string  `json:"direction"`
	Pattern      string  `json:"pattern"`
	Rows         int64   `json:"rows"`
	Columns      int64   `json:"columns"`
	CustomOrder  []int64 `json:"custom_order"`
}

type Settings struct {
	ControllerType      string   `json:"controller_type"`
	ConnectionTimeoutMS int64    `json:"connection_timeout_ms"`
	Outputs             []Output `json:"outputs"`
}

func (s *Settings) NormalizeAndValidate() (int64, error) {
	s.ControllerType = strings.TrimSpace(strings.ToLower(s.ControllerType))
	if s.ControllerType == "" {
		s.ControllerType = "wled"
	}
	switch s.ControllerType {
	case "wled", "wled-esp8266", "wled-esp32", "wled-compatible":
	default:
		return 0, ErrInvalidConfiguration
	}
	if s.ConnectionTimeoutMS == 0 {
		s.ConnectionTimeoutMS = DefaultConnectionTimeoutMS
	}
	if s.ConnectionTimeoutMS < 100 || s.ConnectionTimeoutMS > 30000 {
		return 0, ErrInvalidConfiguration
	}
	if s.Outputs == nil {
		s.Outputs = []Output{}
	}
	maxOutputs := int64(64)
	switch s.ControllerType {
	case "wled-esp8266":
		maxOutputs = 1
	case "wled-esp32":
		maxOutputs = 10
	case "wled":
		maxOutputs = 32
	}
	if int64(len(s.Outputs)) > maxOutputs {
		return 0, ErrInvalidConfiguration
	}
	sort.Slice(s.Outputs, func(i, j int) bool { return s.Outputs[i].DisplayOrder < s.Outputs[j].DisplayOrder })
	var total int64
	for i := range s.Outputs {
		output := &s.Outputs[i]
		output.Name = strings.TrimSpace(output.Name)
		if output.ID < 0 || output.DisplayOrder < 0 || (i > 0 && s.Outputs[i-1].DisplayOrder == output.DisplayOrder) || output.Name == "" || len(output.Name) > 120 || output.PixelCount < 1 || output.PixelCount > MaxPixelsPerOutput || output.PixelCount > math.MaxInt64-total {
			return 0, ErrInvalidConfiguration
		}
		output.DisplayOrder = int64(i)
		if output.Direction == "" {
			output.Direction = "forward"
		}
		if output.Direction != "forward" && output.Direction != "reverse" {
			return 0, ErrInvalidConfiguration
		}
		if output.Pattern == "" {
			output.Pattern = "linear"
		}
		if output.Rows == 0 {
			output.Rows = 1
		}
		if output.Columns == 0 {
			output.Columns = 1
		}
		if output.Rows < 1 || output.Columns < 1 {
			return 0, ErrInvalidConfiguration
		}
		switch output.Pattern {
		case "linear":
			if len(output.CustomOrder) != 0 {
				return 0, ErrInvalidConfiguration
			}
			output.Rows, output.Columns = 1, 1
		case "serpentine_rows", "serpentine_columns":
			if len(output.CustomOrder) != 0 || output.Rows > math.MaxInt64/output.Columns || output.Rows*output.Columns != output.PixelCount {
				return 0, ErrInvalidConfiguration
			}
		case "custom":
			if int64(len(output.CustomOrder)) != output.PixelCount {
				return 0, ErrInvalidConfiguration
			}
			seen := make([]bool, len(output.CustomOrder))
			for _, pixel := range output.CustomOrder {
				if pixel < 0 || pixel >= int64(len(seen)) || seen[int(pixel)] {
					return 0, ErrInvalidConfiguration
				}
				seen[int(pixel)] = true
			}
		default:
			return 0, ErrInvalidConfiguration
		}
		total += output.PixelCount
	}
	return total, nil
}
