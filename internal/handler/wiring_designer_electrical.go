package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/controllerconfig"
)

const wiringElectricalConfirmation = "APPLY WIRING"

type wiringElectricalRequest struct {
	Settings          controllerconfig.Settings `json:"settings"`
	ExpectedSignature string                    `json:"expected_signature"`
	Confirmation      string                    `json:"confirmation"`
}

func decodeWiringElectrical(w http.ResponseWriter, r *http.Request) (int64, wiringElectricalRequest, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid controller ID", 400)
		return 0, wiringElectricalRequest{}, false
	}
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "application/json is required", 415)
		return 0, wiringElectricalRequest{}, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req wiringElectricalRequest
	if err = dec.Decode(&req); err != nil {
		http.Error(w, "invalid electrical configuration: "+err.Error(), 400)
		return 0, req, false
	}
	if dec.Decode(new(any)) != io.EOF {
		http.Error(w, "one JSON object is required", 400)
		return 0, req, false
	}
	return id, req, true
}

func (h *Handler) HandleWiringDesignerElectricalReview(w http.ResponseWriter, r *http.Request) {
	id, req, ok := decodeWiringElectrical(w, r)
	if !ok {
		return
	}
	review, err := h.Hardware.ReviewWiringDesigner(r.Context(), id, req.Settings)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Electrical review rejected: " + err.Error() + ". Use Arrange Layout to change the drawing without changing existing mappings."})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(review)
}

func (h *Handler) HandleWiringDesignerElectricalApply(w http.ResponseWriter, r *http.Request) {
	id, req, ok := decodeWiringElectrical(w, r)
	if !ok {
		return
	}
	if req.Confirmation != wiringElectricalConfirmation {
		http.Error(w, "confirmation must be APPLY WIRING", 400)
		return
	}
	if err := h.Hardware.ApplyWiringDesigner(r.Context(), id, req.Settings, req.ExpectedSignature); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "Electrical configuration was not applied: " + err.Error() + ". Reload and review the current configuration before retrying."})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]bool{"applied": true})
}
