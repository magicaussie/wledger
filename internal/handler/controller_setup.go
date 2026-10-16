package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/wiringdesigner"
	"github.com/tuxedocurly/wledger/web/pages"
)

type controllerSetupRequest struct {
	Name      string                    `json:"name"`
	IpAddress string                    `json:"ip_address"`
	Port      int64                     `json:"port"`
	Settings  controllerconfig.Settings `json:"settings"`
	Layout    wiringdesigner.Layout     `json:"layout"`
}

func setupJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func decodeSetup(w http.ResponseWriter, r *http.Request, value any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		setupJSON(w, 415, map[string]string{"error": "application/json is required"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, wiringdesigner.MaxMetadataBytes+128*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		setupJSON(w, 400, map[string]string{"error": "Invalid setup draft: " + err.Error()})
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		setupJSON(w, 400, map[string]string{"error": "Submit one setup object"})
		return false
	}
	return true
}

func (h *Handler) HandleControllerSetup(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Controller db.Controller             `json:"controller"`
		Settings   controllerconfig.Settings `json:"settings"`
		Layout     wiringdesigner.Layout     `json:"layout"`
		IsSetup    bool                      `json:"is_setup"`
	}{
		Controller: db.Controller{Port: sql.NullInt64{Int64: 80, Valid: true}}, Settings: controllerconfig.Settings{ControllerType: "wled", ConnectionTimeoutMS: 2000, Outputs: []controllerconfig.Output{}}, Layout: wiringdesigner.Layout{Version: 1, Width: 1200, Height: 800, Outputs: []wiringdesigner.OutputLayout{}, Jumpers: []wiringdesigner.Jumper{}}, IsSetup: true,
	}
	w.Header().Set("Cache-Control", "no-store")
	_ = pages.WiringDesigner(0, data, auth.GetUserFromRequest(r)).Render(r.Context(), w)
}

func (h *Handler) HandleControllerSetupSave(w http.ResponseWriter, r *http.Request) {
	var input controllerSetupRequest
	if !decodeSetup(w, r, &input) {
		return
	}
	id, err := h.Hardware.CreateControllerWithLayout(r.Context(), db.CreateControllerParams{Name: input.Name, IpAddress: input.IpAddress, Port: sql.NullInt64{Int64: input.Port, Valid: true}}, input.Settings, input.Layout)
	if err != nil {
		setupJSON(w, 400, map[string]string{"error": "Nothing was saved: " + err.Error() + ". Correct the draft and retry."})
		return
	}
	setupJSON(w, 200, map[string]int64{"id": id})
}

// Discovery is reached only through an explicit administrator POST. Neither
// setup rendering nor saving calls it. No WLED state/configuration is written.
func (h *Handler) HandleControllerSetupDiscover(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string `json:"name"`
		IpAddress string `json:"ip_address"`
		Port      int64  `json:"port"`
	}
	if !decodeSetup(w, r, &input) {
		return
	}
	_, host, err := hardware.ValidateControllerSettings(input.Name, input.IpAddress, input.Port)
	if err != nil {
		setupJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, ResponseHeaderTimeout: 2 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, out any) error {
		request, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, strconv.FormatInt(input.Port, 10))+path, nil)
		if e != nil {
			return e
		}
		request.Header.Set("Accept", "application/json")
		response, e := client.Do(request)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return fmt.Errorf("%s returned HTTP %d", path, response.StatusCode)
		}
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
		if e != nil {
			return e
		}
		if len(raw) > 1024*1024 {
			return fmt.Errorf("%s exceeds the discovery size limit", path)
		}
		return json.Unmarshal(raw, out)
	}
	var info struct {
		Version string `json:"ver"`
		Arch    string `json:"arch"`
		LEDs    struct {
			Count int64 `json:"count"`
		} `json:"leds"`
	}
	var cfg struct {
		Hardware struct {
			LED struct {
				Total  int64 `json:"total"`
				Inputs []struct {
					Start   int64   `json:"start"`
					Length  int64   `json:"len"`
					Pins    []int64 `json:"pin"`
					Reverse bool    `json:"rev"`
					Type    int64   `json:"type"`
				} `json:"ins"`
			} `json:"led"`
		} `json:"hw"`
	}
	if err = get("/json/info", &info); err == nil {
		err = get("/json/cfg", &cfg)
	}
	if err != nil {
		setupJSON(w, 502, map[string]string{"error": "Discovery unavailable: " + err.Error() + ". Continue with manual setup; nothing was changed."})
		return
	}
	type output struct {
		Name       string  `json:"name"`
		PixelCount int64   `json:"pixel_count"`
		Start      int64   `json:"hardware_start"`
		Pins       []int64 `json:"pins"`
		Reverse    bool    `json:"hardware_reversed"`
	}
	outputs := []output{}
	var total int64
	valid := info.LEDs.Count > 0 && len(cfg.Hardware.LED.Inputs) > 0 && len(cfg.Hardware.LED.Inputs) <= 64
	for i, item := range cfg.Hardware.LED.Inputs {
		if item.Length < 1 || item.Length > controllerconfig.MaxPixelsPerOutput || item.Start != total {
			valid = false
			break
		}
		outputs = append(outputs, output{fmt.Sprintf("Output %d", i+1), item.Length, item.Start, item.Pins, item.Reverse})
		total += item.Length
	}
	if total != info.LEDs.Count || (cfg.Hardware.LED.Total > 0 && cfg.Hardware.LED.Total != total) {
		valid = false
	}
	if !valid {
		setupJSON(w, 422, map[string]string{"error": "Reported output ranges/counts are incomplete or unsupported. Continue manually and review the controller's physical outputs; nothing was changed."})
		return
	}
	kind := "wled"
	if info.Arch == "esp32" {
		kind = "wled-esp32"
	} else if info.Arch == "esp8266" {
		kind = "wled-esp8266"
	}
	setupJSON(w, 200, map[string]any{"led_count": info.LEDs.Count, "firmware": info.Version, "controller_type": kind, "outputs": outputs})
}
