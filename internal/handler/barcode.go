package handler

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
)

func writeCode128(w http.ResponseWriter, value string) {
	encoded, err := code128.Encode(value)
	if err != nil {
		http.Error(w, "Failed to generate Code 128 barcode", http.StatusInternalServerError)
		return
	}
	scaled, err := barcode.Scale(encoded, 1200, 240)
	if err != nil {
		http.Error(w, "Failed to scale Code 128 barcode", http.StatusInternalServerError)
		return
	}
	bounds := scaled.Bounds()
	quietZone := max(40, bounds.Dx()/30)
	canvas := image.NewRGBA(image.Rect(0, 0, bounds.Dx()+2*quietZone, bounds.Dy()))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(quietZone, 0, quietZone+bounds.Dx(), bounds.Dy()), scaled, bounds.Min, draw.Src)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := png.Encode(w, canvas); err != nil {
		return
	}
}
