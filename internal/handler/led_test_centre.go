package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/internal/wled"
	"github.com/tuxedocurly/wledger/web/pages"
)

func ledJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func ledRequest(w http.ResponseWriter, r *http.Request, value any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		ledJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "Expected application/json"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		ledJSON(w, 400, map[string]string{"error": "Invalid JSON request: " + err.Error()})
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		ledJSON(w, 400, map[string]string{"error": "Expected one JSON object"})
		return false
	}
	return true
}
func ledAPIError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status = 404
	case errors.Is(err, wled.ErrLocateSessionOwned):
		status = 403
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = 504
	case errors.Is(err, wled.ErrPhysicalOutputDisabled), errors.Is(err, wled.ErrLocateSessionActive), errors.Is(err, wled.ErrLocateMappingUnverified), errors.Is(err, wled.ErrLocateTopology), errors.Is(err, wled.ErrNoDrawerLEDAllocation), errors.Is(err, wled.ErrLocateSessionStale), errors.Is(err, db.ErrPhysicalLocateActive), errors.Is(err, storage.ErrLEDAllocationInvalid), errors.Is(err, storage.ErrLEDAllocationStale), errors.Is(err, storage.ErrLEDControllerConfiguration):
		status = 409
	}
	ledJSON(w, status, map[string]string{"error": err.Error()})
}
func (h *Handler) HandleLEDTestCentre(w http.ResponseWriter, r *http.Request) {
	_ = pages.LEDTestCentre(auth.GetUserFromRequest(r)).Render(r.Context(), w)
}
func (h *Handler) ledLocator(w http.ResponseWriter) bool {
	if h.Locator == nil {
		ledJSON(w, 503, map[string]string{"error": "LED operation service unavailable"})
		return false
	}
	return true
}
func (h *Handler) HandleLEDInspect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ControllerID int64 `json:"controller_id"`
	}
	if !ledRequest(w, r, &in) || !h.ledLocator(w) {
		return
	}
	if in.ControllerID <= 0 {
		ledJSON(w, 400, map[string]string{"error": "Select a controller"})
		return
	}
	layout, err := h.Locator.DiscoverPhysicalOutputs(r.Context(), in.ControllerID)
	if err != nil {
		ledAPIError(w, err)
		return
	}
	type output struct {
		Order      int64   `json:"order"`
		Start      int64   `json:"start"`
		Length     int64   `json:"length"`
		FirstIndex int64   `json:"first_index"`
		LastIndex  int64   `json:"last_index"`
		Pins       []int64 `json:"pins"`
		Reversed   bool    `json:"reversed"`
	}
	outputs := []output{}
	for _, o := range layout.Outputs {
		outputs = append(outputs, output{o.Order, o.Start, o.Length, o.FirstIndex, o.LastIndex, o.Pins, o.Reversed})
	}
	ledJSON(w, 200, map[string]any{"controller_id": layout.ControllerID, "outputs": outputs, "capacity": layout.Capacity, "firmware": layout.Firmware})
}
func (h *Handler) HandleLEDStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ControllerID    int64  `json:"controller_id"`
		OutputOrder     *int   `json:"output_order"`
		Start           int64  `json:"start"`
		Count           int64  `json:"count"`
		Color           string `json:"color"`
		Brightness      *int   `json:"brightness"`
		DurationSeconds int64  `json:"duration_seconds"`
		Mode            string `json:"mode"`
		Function        string `json:"function"`
		DrawerID        int64  `json:"drawer_id"`
		CabinetID       int64  `json:"cabinet_id"`
		BinID           int64  `json:"bin_id"`
	}
	if !ledRequest(w, r, &in) || !h.ledLocator(w) {
		return
	}
	if in.DurationSeconds < 1 || in.DurationSeconds > 3600 || in.Start < 0 || in.DrawerID < 0 || in.CabinetID < 0 || (in.DrawerID == 0 && in.CabinetID == 0 && (in.Count < 1 || in.ControllerID <= 0)) {
		ledJSON(w, 400, map[string]string{"error": "Select a valid controller, pixel range and duration (1–3600 seconds)"})
		return
	}
	styleName := in.Function
	if strings.HasPrefix(styleName, "drawer_") {
		styleName = "locate_drawer"
	}
	styles, err := wled.LoadFunctionSettings(r.Context(), h.Queries)
	if err != nil {
		ledAPIError(w, err)
		return
	}
	style, ok := styles[styleName]
	if !ok {
		ledJSON(w, 400, map[string]string{"error": "Unknown LED function"})
		return
	}
	if in.Color != "" {
		style.Color, err = wled.NormalizeFunctionColor(in.Color)
		if err != nil {
			ledJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
	}
	if in.Brightness != nil {
		style.Brightness = *in.Brightness
	}
	if style.Brightness < 0 || style.Brightness > 255 {
		ledJSON(w, 400, map[string]string{"error": "Brightness must be 0–255"})
		return
	}
	if in.Mode == "" {
		in.Mode = "solid"
	}
	if in.Mode != "solid" && in.Mode != "flash" && in.Mode != "sequence" {
		ledJSON(w, 400, map[string]string{"error": "Invalid test mode"})
		return
	}
	session, err := h.Locator.StartTest(r.Context(), wled.TestRequest{ControllerID: in.ControllerID, OutputOrder: in.OutputOrder, Start: in.Start, Count: in.Count, DrawerID: in.DrawerID, BinID: in.BinID, CabinetID: in.CabinetID, Function: in.Function, Mode: in.Mode, Style: style, Duration: time.Duration(in.DurationSeconds) * time.Second}, auth.GetUserFromRequest(r).ID)
	if err != nil {
		ledAPIError(w, err)
		return
	}
	ledJSON(w, 200, map[string]any{"operation": session, "status": "Controller acknowledged the initial command. Physical LED behaviour is not verified. Refresh operations for sequence failures."})
}
func (h *Handler) HandleLEDStop(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OperationID string `json:"operation_id"`
	}
	if !ledRequest(w, r, &in) || !h.ledLocator(w) {
		return
	}
	if len(in.OperationID) == 0 || len(in.OperationID) > 128 {
		ledJSON(w, 400, map[string]string{"error": "Select an operation"})
		return
	}
	if err := h.Locator.StopOperation(r.Context(), in.OperationID, auth.GetUserFromRequest(r).ID); err != nil {
		ledAPIError(w, err)
		return
	}
	ledJSON(w, 200, map[string]string{"status": "Selected operation stopped; targeted cleanup acknowledged (or operation already resolved). Other operations were not stopped."})
}
func (h *Handler) HandleLEDEmergencyOff(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm string `json:"confirm"`
	}
	if !ledRequest(w, r, &in) || !h.ledLocator(w) {
		return
	}
	if in.Confirm != "EMERGENCY OFF" {
		ledJSON(w, 400, map[string]string{"error": "Type EMERGENCY OFF to explicitly confirm cancelling every operation and powering off all configured controllers"})
		return
	}
	results, err := h.Locator.EmergencyOff(r.Context())
	if err != nil {
		ledJSON(w, 502, map[string]any{"results": results, "error": fmt.Sprintf("Emergency off incomplete; retry required: %v", err)})
		return
	}
	ledJSON(w, 200, map[string]any{"results": results})
}
