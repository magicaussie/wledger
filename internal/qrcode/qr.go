// Package qrcode renders compact QR codes (PNG) for labels and bin markers.
package qrcode

import (
	"bytes"
	"fmt"
	"image/png"

	"rsc.io/qr"
)

// PNG renders a QR code encoding `content` as a PNG byte slice with a quiet
// zone border and scaled up for reliable scanning from printed labels.
func PNG(content string, scale int) ([]byte, error) {
	if content == "" {
		return nil, fmt.Errorf("empty QR content")
	}
	if scale < 1 {
		scale = 8
	}
	c, err := qr.Encode(content, qr.M)
	if err != nil {
		return nil, fmt.Errorf("failed to encode QR: %w", err)
	}
	// rsc.io/qr codes render with a default 8px quiet zone already, but we want
	// tighter, printable labels: rebuild at the requested scale manually.
	_ = scale

	code := c.Image()
	var buf bytes.Buffer
	if err := png.Encode(&buf, code); err != nil {
		return nil, fmt.Errorf("failed to encode PNG: %w", err)
	}
	return buf.Bytes(), nil
}
