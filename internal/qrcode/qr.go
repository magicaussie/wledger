// Package qrcode renders compact QR codes (PNG) for labels and bin markers.
package qrcode

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"rsc.io/qr"
)

// PNG renders a QR code encoding `content` as a PNG byte slice at the given
// module scale (pixels per module). A quiet-zone border is added around the
// modules for reliable scanning from printed labels. scale defaults to 12 when
// <= 0, which produces a comfortably large, crisp label at 300dpi.
func PNG(content string, scale int) ([]byte, error) {
	if content == "" {
		return nil, fmt.Errorf("empty QR content")
	}
	if scale <= 0 {
		scale = 12
	}

	c, err := qr.Encode(content, qr.M)
	if err != nil {
		return nil, fmt.Errorf("failed to encode QR: %w", err)
	}

	const quiet = 4 // modules of white border on each side (ISO/IEC 18004)
	modules := c.Size
	out := (modules + quiet*2) * scale

	img := image.NewGray(image.Rect(0, 0, out, out))
	for y := 0; y < out; y++ {
		for x := 0; x < out; x++ {
			mx := x/scale - quiet
			my := y/scale - quiet
			if c.Black(mx, my) {
				img.SetGray(x, y, color.Gray{Y: 0})
			} else {
				img.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}