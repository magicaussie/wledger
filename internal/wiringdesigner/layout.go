package wiringdesigner

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"sort"
	"strings"
)

const (
	MaxMetadataBytes   = 2 << 20
	MaxBackgroundBytes = 1 << 20
	MaxCanvasSize      = 10000
	MaxRows            = 1000
	MaxOutputPixels    = 100000
)

var ErrInvalid = errors.New("invalid graphical wiring layout")

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type SectionLayout struct {
	ID       string  `json:"id"`
	Start    int64   `json:"start"`
	Count    int64   `json:"count"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation int     `json:"rotation"`
}
type PixelPosition struct {
	Index int64   `json:"index"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}
type OutputLayout struct {
	OutputID       int64           `json:"output_id"`
	SectionLayouts []SectionLayout `json:"section_layouts,omitempty"`
	PixelPositions []PixelPosition `json:"pixel_positions,omitempty"`
	X              float64         `json:"x"`
	Y              float64         `json:"y"`
	Width          float64         `json:"width"`
	Height         float64         `json:"height"`
	Rotation       int             `json:"rotation"`
	Axis           string          `json:"axis"`
	Rows           []int64         `json:"rows"`
	Sections       []int64         `json:"sections"`
}
type Jumper struct {
	ID     string  `json:"id"`
	Points []Point `json:"points"`
}
type Background struct {
	DataURL string  `json:"data_url"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Opacity float64 `json:"opacity"`
}
type Layout struct {
	TopologySignature string         `json:"topology_signature"`
	Version           int            `json:"version"`
	Revision          int64          `json:"revision"`
	Width             float64        `json:"width"`
	Height            float64        `json:"height"`
	Outputs           []OutputLayout `json:"outputs"`
	Jumpers           []Jumper       `json:"jumpers"`
	Background        *Background    `json:"background,omitempty"`
}

func TopologySignature(outputs []TopologyOutput) string {
	canonical := append([]TopologyOutput(nil), outputs...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].ID < canonical[j].ID })
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:])
}

type TopologyOutput struct {
	ID           int64  `json:"id"`
	DisplayOrder int64  `json:"display_order"`
	PixelCount   int64  `json:"pixel_count"`
	Direction    string `json:"direction"`
	Pattern      string `json:"pattern"`
	Rows         int64  `json:"rows"`
	Columns      int64  `json:"columns"`
	CustomOrder  string `json:"custom_order"`
}

func Validate(l Layout, outputPixels map[int64]int64) error {
	if l.Version != 1 || l.Revision < 0 || !positive(l.Width) || !positive(l.Height) || l.Width > MaxCanvasSize || l.Height > MaxCanvasSize || len(l.Outputs) > 64 || len(l.Jumpers) > 1000 {
		return fmt.Errorf("%w: canvas version, dimensions or item count", ErrInvalid)
	}
	seen := make(map[int64]bool, len(l.Outputs))
	if len(l.Outputs) != len(outputPixels) {
		return fmt.Errorf("%w: output set must exactly match controller", ErrInvalid)
	}
	for _, o := range l.Outputs {
		pixels, exists := outputPixels[o.OutputID]
		if !exists || seen[o.OutputID] || pixels < 1 || pixels > MaxOutputPixels || !positive(o.X) && o.X != 0 || !positive(o.Y) && o.Y != 0 || !positive(o.Width) || !positive(o.Height) || o.X+o.Width > l.Width || o.Y+o.Height > l.Height || o.Rotation%90 != 0 || o.Rotation < 0 || o.Rotation > 270 || (o.Axis != "rows" && o.Axis != "columns") || len(o.Rows) == 0 || len(o.Rows) > MaxRows {
			return fmt.Errorf("%w: output geometry", ErrInvalid)
		}
		seen[o.OutputID] = true
		total := int64(0)
		for _, n := range o.Rows {
			if n < 1 || n > pixels || total > pixels-n {
				return fmt.Errorf("%w: row lengths", ErrInvalid)
			}
			total += n
		}
		if total != pixels {
			return fmt.Errorf("%w: row lengths must account for all output pixels", ErrInvalid)
		}
		if len(o.Sections) == 0 || len(o.Sections) > 1000 {
			return fmt.Errorf("%w: strip sections", ErrInvalid)
		}
		if len(o.SectionLayouts) > 1000 || len(o.PixelPositions) > MaxOutputPixels {
			return fmt.Errorf("%w: custom layout count", ErrInvalid)
		}
		ids := map[string]bool{}
		sortedSections := append([]SectionLayout(nil), o.SectionLayouts...)
		for _, s := range o.SectionLayouts {
			if len(s.ID) == 0 || len(s.ID) > 80 || ids[s.ID] || s.Start < 0 || s.Count < 1 || s.Count > pixels || s.Start > pixels-s.Count || !finite(s.X) || !finite(s.Y) || math.Abs(s.X) > l.Width || math.Abs(s.Y) > l.Height || s.Rotation < -360 || s.Rotation > 360 {
				return fmt.Errorf("%w: section layouts", ErrInvalid)
			}
			ids[s.ID] = true
		}
		if len(sortedSections) > 0 {
			sort.Slice(sortedSections, func(i, j int) bool { return sortedSections[i].Start < sortedSections[j].Start })
			var cursor int64
			if len(sortedSections) != len(o.Sections) {
				return fmt.Errorf("%w: section layouts must match sections", ErrInvalid)
			}
			for i, section := range sortedSections {
				if section.Start != cursor || section.Count != o.Sections[i] {
					return fmt.Errorf("%w: section layouts must exactly match section coverage", ErrInvalid)
				}
				cursor += section.Count
			}
			if cursor != pixels {
				return fmt.Errorf("%w: section layouts must cover output", ErrInvalid)
			}
		}
		refs := map[int64]bool{}
		for _, p := range o.PixelPositions {
			if p.Index < 0 || p.Index >= pixels || refs[p.Index] || !finite(p.X) || !finite(p.Y) || p.X < 0 || p.Y < 0 || p.X > l.Width || p.Y > l.Height {
				return fmt.Errorf("%w: pixel positions", ErrInvalid)
			}
			refs[p.Index] = true
		}

		sectionTotal := int64(0)
		for _, count := range o.Sections {
			if count < 1 || count > pixels || sectionTotal > pixels-count {
				return fmt.Errorf("%w: strip section lengths", ErrInvalid)
			}
			sectionTotal += count
		}
		if sectionTotal != pixels {
			return fmt.Errorf("%w: strip sections must account for output pixels", ErrInvalid)
		}
	}
	for _, j := range l.Jumpers {
		if len(j.ID) < 1 || len(j.ID) > 80 || len(j.Points) < 2 || len(j.Points) > 2000 {
			return fmt.Errorf("%w: jumper identity or point count", ErrInvalid)
		}
		for _, p := range j.Points {
			if !finite(p.X) || !finite(p.Y) || p.X < 0 || p.Y < 0 || p.X > l.Width || p.Y > l.Height {
				return ErrInvalid
			}
		}
	}
	if l.Background != nil {
		if err := validateBackground(*l.Background, l.Width, l.Height); err != nil {
			return err
		}
	}
	b, err := json.Marshal(l)
	if err != nil || len(b) > MaxMetadataBytes {
		return fmt.Errorf("%w: metadata size", ErrInvalid)
	}
	return nil
}
func validateBackground(b Background, w, h float64) error {
	if !strings.HasPrefix(b.DataURL, "data:image/") || len(b.DataURL) > MaxBackgroundBytes*2 {
		return fmt.Errorf("%w: background data URL", ErrInvalid)
	}
	kind, payload, ok := strings.Cut(strings.TrimPrefix(b.DataURL, "data:image/"), ";base64,")
	if !ok || (kind != "png" && kind != "jpeg") {
		return fmt.Errorf("%w: background must be PNG or JPEG", ErrInvalid)
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(raw) > MaxBackgroundBytes {
		return fmt.Errorf("%w: background size", ErrInvalid)
	}
	cfg, _, err := image.DecodeConfig(strings.NewReader(string(raw)))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return fmt.Errorf("%w: background dimensions", ErrInvalid)
	}
	if !finite(b.X) || !finite(b.Y) || !positive(b.Width) || !positive(b.Height) || b.X < 0 || b.Y < 0 || b.X+b.Width > w || b.Y+b.Height > h || !finite(b.Opacity) || b.Opacity < 0 || b.Opacity > 1 {
		return ErrInvalid
	}
	return nil
}
func positive(v float64) bool { return finite(v) && v > 0 }
func finite(v float64) bool   { return !math.IsNaN(v) && !math.IsInf(v, 0) }
