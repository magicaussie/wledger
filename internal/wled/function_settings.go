package wled

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/tuxedocurly/wledger/internal/db"
)

const FunctionSettingsKey = "led_function_settings"

type FunctionStyle struct {
	Color      string `json:"color"`
	Brightness int    `json:"brightness"`
}

type FunctionSettings map[string]FunctionStyle

// FunctionNames returns the stable display order and supported function keys.
func FunctionNames() []string {
	return []string{"locate_part", "locate_bin", "locate_drawer", "locate_cabinet", "identify_pixel", "test_pixel", "warning", "error", "success", "selected"}
}

// DefaultFunctionSettings returns a fresh map; brightness defaults to 128.
func DefaultFunctionSettings() FunctionSettings {
	colors := []string{"#00FF00", "#0000FF", "#FFFF00", "#800080", "#FFFFFF", "#00FFFF", "#FFA500", "#FF0000", "#00FF00", "#FF00FF"}
	result := make(FunctionSettings)
	for i, name := range FunctionNames() {
		result[name] = FunctionStyle{Color: colors[i], Brightness: 128}
	}
	return result
}

var functionHEX = regexp.MustCompile(`^#?[0-9a-fA-F]{6}$`)
var functionRGB = regexp.MustCompile(`(?i)^rgb\(\s*([0-9]{1,3})\s*,\s*([0-9]{1,3})\s*,\s*([0-9]{1,3})\s*\)$`)

// NormalizeFunctionColor accepts six-digit HEX (with optional #) or rgb(r,g,b).
// It returns canonical #RRGGBB, rejecting out-of-range channels and CSS expressions.
func NormalizeFunctionColor(color string) (string, error) {
	color = strings.TrimSpace(color)
	if functionHEX.MatchString(color) {
		return "#" + strings.ToUpper(strings.TrimPrefix(color, "#")), nil
	}
	if m := functionRGB.FindStringSubmatch(color); m != nil {
		var channels [3]int
		for i := range channels {
			var err error
			channels[i], err = strconv.Atoi(m[i+1])
			if err != nil {
				return "", fmt.Errorf("invalid RGB channel: %w", err)
			}
			if channels[i] > 255 {
				return "", fmt.Errorf("RGB channel must be 0..255")
			}
		}
		return fmt.Sprintf("#%02X%02X%02X", channels[0], channels[1], channels[2]), nil
	}
	return "", fmt.Errorf("color must be six-digit HEX or rgb(r,g,b)")
}

// NormalizeFunctionSettings validates supplied entries and fills missing functions.
// Unknown function names are rejected to catch typos rather than silently losing intent.
func NormalizeFunctionSettings(settings FunctionSettings) (FunctionSettings, error) {
	result := DefaultFunctionSettings()
	for name, style := range settings {
		if _, ok := result[name]; !ok {
			return nil, fmt.Errorf("unknown LED function %q", name)
		}
		color, err := NormalizeFunctionColor(style.Color)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if style.Brightness < 0 || style.Brightness > 255 {
			return nil, fmt.Errorf("%s: brightness must be 0..255", name)
		}
		result[name] = FunctionStyle{Color: color, Brightness: style.Brightness}
	}
	return result, nil
}

// RGB validates the style and returns brightness-scaled channels without changing
// controller-wide brightness. Zero brightness produces black, not a fallback.
func (style FunctionStyle) RGB() ([3]int, error) {
	var rgb [3]int
	normalized, err := NormalizeFunctionSettings(FunctionSettings{"selected": style})
	if err != nil {
		return rgb, err
	}
	style = normalized["selected"]
	for i := range rgb {
		channel, err := strconv.ParseInt(style.Color[1+i*2:3+i*2], 16, 32)
		if err != nil {
			return [3]int{}, fmt.Errorf("invalid HEX channel: %w", err)
		}
		rgb[i] = int(channel) * style.Brightness / 255
	}
	return rgb, nil
}

func (style FunctionStyle) legacyColor() (string, error) {
	rgb, err := style.RGB()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("#%02X%02X%02X", rgb[0], rgb[1], rgb[2]), nil
}

func LoadFunctionSettings(ctx context.Context, q db.Querier) (FunctionSettings, error) {
	raw, err := q.GetFlag(ctx, FunctionSettingsKey)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultFunctionSettings(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("load LED function settings: %w", err)
	}
	// Decode entries individually to distinguish a missing brightness from explicit zero.
	dec := json.NewDecoder(strings.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("LED function settings must be a JSON object")
	}
	settings := make(FunctionSettings)
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("LED function settings: %w", err)
		}
		name := token.(string)
		if _, exists := settings[name]; exists {
			return nil, fmt.Errorf("duplicate LED function %q", name)
		}
		var rawStyle json.RawMessage
		if err := dec.Decode(&rawStyle); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		var style struct {
			Color      *string `json:"color"`
			Brightness *int    `json:"brightness"`
		}
		if err := validateFunctionStyleKeys(rawStyle); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		sd := json.NewDecoder(bytes.NewReader(rawStyle))
		sd.DisallowUnknownFields()
		if err := sd.Decode(&style); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if style.Color == nil || style.Brightness == nil {
			return nil, fmt.Errorf("%s: color and brightness are required", name)
		}
		settings[name] = FunctionStyle{Color: *style.Color, Brightness: *style.Brightness}
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("LED function settings: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("LED function settings contain trailing JSON")
	}
	return NormalizeFunctionSettings(settings)
}

func validateFunctionStyleKeys(raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	token, err := dec.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("style must be an object")
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		key := token.(string)
		if (key != "color" && key != "brightness") || seen[key] {
			return fmt.Errorf("unknown or duplicate style field %q", key)
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func SaveFunctionSettings(ctx context.Context, q db.Querier, settings FunctionSettings) error {
	normalized, err := NormalizeFunctionSettings(settings)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return q.SetFlag(ctx, db.SetFlagParams{Key: FunctionSettingsKey, Value: string(raw)})
}
