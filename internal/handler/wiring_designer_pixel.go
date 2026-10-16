package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
)

// HandleWiringDesignerPixel resolves saved addressing only. It never discovers
// controllers or sends commands, including when the selected pixel is unallocated.
func (h *Handler) HandleWiringDesignerPixel(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	pixel, pe := strconv.ParseInt(r.URL.Query().Get("pixel"), 10, 64)
	if e != nil || pe != nil || id <= 0 || pixel < 0 {
		http.Error(w, "invalid controller or zero-based pixel", 400)
		return
	}
	controller, e := h.Queries.GetController(r.Context(), id)
	if e != nil {
		http.Error(w, "controller not found", 404)
		return
	}
	if pixel >= controller.LedCount {
		http.Error(w, "pixel exceeds saved controller capacity", 400)
		return
	}
	type address struct {
		CabinetID int64  `json:"cabinet_id"`
		Cabinet   string `json:"cabinet"`
		Pixel     int64  `json:"pixel"`
	}
	addresses := []address{}
	warnings := []string{}
	cabinets, e := h.Queries.ListCabinets(r.Context())
	if e != nil {
		http.Error(w, "cannot load cabinets", 500)
		return
	}
	for _, cab := range cabinets {
		topology, err := cabinetpixels.Load(r.Context(), h.Queries, cab.ID)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		for _, c := range topology.Controllers {
			if c.Record.ID != id {
				continue
			}
			pieces, err := cabinetpixels.SplitTopologyRange(topology, c.Start, c.Pixels)
			if err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			for _, p := range pieces {
				if p.ControllerID != id || pixel+1 < p.LocalStart || pixel+1 >= p.LocalStart+p.Count {
					continue
				}
				offset := pixel + 1 - p.LocalStart
				for _, s := range c.Strings {
					if s.Record.ID == p.StringID && s.Direction == "reverse" && (s.Pattern == "linear" || s.Pattern == "") {
						offset = p.Count - 1 - offset
					}
				}
				addresses = append(addresses, address{cab.ID, cab.Name, p.CabinetStart + offset})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"controller_id": id, "controller_pixel": pixel, "cabinet_addresses": addresses, "warnings": warnings})
}
