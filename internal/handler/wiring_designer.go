package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wiringdesigner"
	"github.com/tuxedocurly/wledger/web/pages"
)

func designerFlagKey(id int64) string { return fmt.Sprintf("led_wiring_designer_v1:%d", id) }

type wiringDesignerData struct {
	Controller db.Controller         `json:"controller"`
	Settings   any                   `json:"settings"`
	Layout     wiringdesigner.Layout `json:"layout"`
}

func (h *Handler) HandleWiringDesigner(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid controller ID", 400)
		return
	}
	controller, err := h.Hardware.GetController(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", 404)
		return
	}
	settings, err := h.Hardware.GetControllerSettings(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load controller output configuration", 500)
		return
	}
	pixelCounts := map[int64]int64{}
	dbOutputs, err := h.Queries.GetControllerOutputs(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load controller topology", 500)
		return
	}
	topologyOutputs := make([]wiringdesigner.TopologyOutput, 0, len(dbOutputs))
	for _, o := range dbOutputs {
		topologyOutputs = append(topologyOutputs, wiringdesigner.TopologyOutput{ID: o.ID, DisplayOrder: o.DisplayOrder, PixelCount: o.PixelCount, Direction: o.Direction, Pattern: o.Pattern, Rows: o.Rows, Columns: o.Columns, CustomOrder: o.CustomOrderJson})
	}
	layoutSignature := wiringdesigner.TopologySignature(topologyOutputs)
	for _, o := range settings.Outputs {
		pixelCounts[o.ID] = o.PixelCount
	}
	layout := wiringdesigner.Layout{Version: 1, Width: 1200, Height: 800, Outputs: []wiringdesigner.OutputLayout{}, Jumpers: []wiringdesigner.Jumper{}}
	if raw, e := h.Queries.GetFlag(r.Context(), designerFlagKey(id)); e == nil {
		if len(raw) > wiringdesigner.MaxMetadataBytes {
			http.Error(w, "saved designer metadata exceeds size limit", 500)
			return
		}
		if e = json.Unmarshal([]byte(raw), &layout); e != nil {
			http.Error(w, "saved designer metadata is invalid", 500)
			return
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		h.UIError.Respond(w, r, e, "Failed to read wiring designer metadata", 500)
		return
	}
	// Reconcile by stable output ID only. Deleted outputs are dropped from the
	// visual sheet; a newly-added electrical output gets a fresh visual tile.
	// Pixel-count edits retain position/rotation but reset the display grid, never
	// changing controller output configuration or saved drawer allocations.
	old := make(map[int64]wiringdesigner.OutputLayout, len(layout.Outputs))
	for _, item := range layout.Outputs {
		old[item.OutputID] = item
	}
	layout.Outputs = make([]wiringdesigner.OutputLayout, 0, len(settings.Outputs))
	for i, output := range settings.Outputs {
		item, exists := old[output.ID]
		if !exists {
			rows, axis := []int64{output.PixelCount}, "rows"
			if output.Pattern == "serpentine_rows" || output.Pattern == "serpentine_columns" {
				rowCount, rowLength := output.Rows, output.Columns
				if output.Pattern == "serpentine_columns" {
					axis = "columns"
					rowCount, rowLength = output.Columns, output.Rows
				}
				rows = make([]int64, rowCount)
				for row := range rows {
					rows[row] = rowLength
				}
			}
			item = wiringdesigner.OutputLayout{OutputID: output.ID, X: 40 + float64(i%4)*280, Y: 40 + float64(i/4)*220, Width: 240, Height: 180, Axis: axis, Rows: rows, Sections: []int64{output.PixelCount}}
		}
		if sum := func() int64 {
			var total int64
			for _, n := range item.Rows {
				total += n
			}
			return total
		}(); sum != output.PixelCount {
			item.Rows = []int64{output.PixelCount}
			item.PixelPositions = nil
		}
		if sum := func() int64 {
			var total int64
			for _, n := range item.Sections {
				total += n
			}
			return total
		}(); sum != output.PixelCount {
			item.Sections = []int64{output.PixelCount}
			item.SectionLayouts = nil
		}
		item.OutputID = output.ID
		if item.Axis == "" {
			item.Axis = "rows"
		}
		layout.Outputs = append(layout.Outputs, item)
	}
	layout.TopologySignature = layoutSignature
	layout.Height = max(layout.Height, max(800, float64((len(settings.Outputs)+3)/4)*220+40))
	if err := wiringdesigner.Validate(layout, pixelCounts); err != nil {
		http.Error(w, "saved graphical wiring metadata failed validation", 500)
		return
	}
	pages.WiringDesigner(id, wiringDesignerData{Controller: controller, Settings: settings, Layout: layout}, auth.GetUserFromRequest(r)).Render(r.Context(), w)
}

func (h *Handler) HandleWiringDesignerSave(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid controller ID", 400)
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "application/json is required", 415)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, wiringdesigner.MaxMetadataBytes+1)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var layout wiringdesigner.Layout
	if err := dec.Decode(&layout); err != nil {
		http.Error(w, "invalid graphical layout: "+err.Error(), 400)
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		http.Error(w, "one JSON layout object is required", 400)
		return
	}
	if layout.Revision < 0 || layout.Revision == int64(^uint64(0)>>1) {
		http.Error(w, "invalid layout revision", 400)
		return
	}
	settings, err := h.Hardware.GetControllerSettings(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", 404)
		return
	}
	pixels := map[int64]int64{}
	for _, o := range settings.Outputs {
		pixels[o.ID] = o.PixelCount
	}
	layout.Revision++
	if err := wiringdesigner.Validate(layout, pixels); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	encoded, err := json.Marshal(layout)
	if err != nil || len(encoded) > wiringdesigner.MaxMetadataBytes {
		http.Error(w, "graphical layout exceeds size limit", 413)
		return
	}
	// The metadata is a UI-only system flag and participates in existing full
	// backup/restore snapshots. Electrical output records are never written here.
	err = h.Queries.ExecTx(r.Context(), func(q db.Querier) error {
		currentOutputs, e := q.GetControllerOutputs(r.Context(), id)
		if e != nil {
			return e
		}
		currentTopology := make([]wiringdesigner.TopologyOutput, 0, len(currentOutputs))
		currentPixels := make(map[int64]int64, len(currentOutputs))
		for _, o := range currentOutputs {
			currentTopology = append(currentTopology, wiringdesigner.TopologyOutput{ID: o.ID, DisplayOrder: o.DisplayOrder, PixelCount: o.PixelCount, Direction: o.Direction, Pattern: o.Pattern, Rows: o.Rows, Columns: o.Columns, CustomOrder: o.CustomOrderJson})
			currentPixels[o.ID] = o.PixelCount
		}
		if layout.TopologySignature == "" || layout.TopologySignature != wiringdesigner.TopologySignature(currentTopology) {
			return fmt.Errorf("controller topology changed; reload before saving")
		}
		if err := wiringdesigner.Validate(layout, currentPixels); err != nil {
			return err
		}
		current, e := q.GetFlag(r.Context(), designerFlagKey(id))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var revision int64
		if e == nil {
			var saved wiringdesigner.Layout
			if json.Unmarshal([]byte(current), &saved) != nil {
				return wiringdesigner.ErrInvalid
			}
			revision = saved.Revision
		}
		if layout.Revision-1 != revision {
			return fmt.Errorf("designer layout changed in another session; reload before saving")
		}
		return q.SetFlag(r.Context(), db.SetFlagParams{Key: designerFlagKey(id), Value: string(encoded)})
	})
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(layout)
}
