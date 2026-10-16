package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/wled"
	"github.com/tuxedocurly/wledger/internal/wledsim"
)

func centreAuthenticatedCall(handle http.HandlerFunc, body string, owner int64) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/hardware/led-test-centre", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: owner, Role: "editor"}))
	w := httptest.NewRecorder()
	handle(w, r)
	return w
}

func TestCentreStartStylesAndDrawerWithSimulator(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newLEDHTTPFixture(t)
	ctx := context.Background()
	sim, err := wledsim.New(wledsim.Config{Count: 100, Outputs: []wledsim.Output{{Start: 0, Length: 40, Pins: []int{2}}, {Start: 40, Length: 60, Pins: []int{4}}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(sim)
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.conn.Exec(`UPDATE controllers SET ip_address=?,port=? WHERE id=1`, host, port); err != nil {
		t.Fatal(err)
	}
	if err = f.h.Locator.SaveControllerMappingVerification(ctx, 1, true); err != nil {
		t.Fatal(err)
	}
	if err = wled.SaveFunctionSettings(ctx, f.h.Queries, wled.FunctionSettings{"test_pixel": {Color: "#123456", Brightness: 0}, "locate_drawer": {Color: "#FF0000", Brightness: 255}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		pixel      int
		want       wledsim.RGB
	}{
		{"saved zero", `{"controller_id":1,"output_order":0,"start":0,"count":1,"duration_seconds":30,"function":"test_pixel"}`, 0, wledsim.RGB{}},
		{"custom color brightness", `{"controller_id":1,"output_order":1,"start":40,"count":1,"duration_seconds":30,"function":"test_pixel","color":"#804020","brightness":128}`, 40, wledsim.RGB{64, 32, 16}},
		{"explicit zero", `{"controller_id":1,"start":0,"count":1,"duration_seconds":30,"function":"test_pixel","color":"#FFFFFF","brightness":0}`, 0, wledsim.RGB{}},
		{"drawer no controller", `{"drawer_id":1,"duration_seconds":30,"function":"drawer_all"}`, 10, wledsim.RGB{255, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := centreAuthenticatedCall(f.h.HandleLEDStart, tc.body, 1)
			if w.Code != 200 {
				t.Fatalf("start=%d %s", w.Code, w.Body.String())
			}
			var result struct {
				Operation wled.PhysicalLocateSession `json:"operation"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			defer f.h.Locator.StopOperation(ctx, result.Operation.ID, 1)
			if got := sim.Pixels()[tc.pixel]; got != tc.want {
				t.Fatalf("pixel=%v want %v", got, tc.want)
			}
			denied := centreAuthenticatedCall(f.h.HandleLEDStop, fmt.Sprintf(`{"operation_id":%q}`, result.Operation.ID), 2)
			if denied.Code != 403 {
				t.Fatalf("wrong user stop=%d %s", denied.Code, denied.Body.String())
			}
			stopped := centreAuthenticatedCall(f.h.HandleLEDStop, fmt.Sprintf(`{"operation_id":%q}`, result.Operation.ID), 1)
			if stopped.Code != 200 {
				t.Fatalf("stop=%d %s", stopped.Code, stopped.Body.String())
			}
		})
	}
	// Emergency power-off is explicit. A new partial-range test must not wake
	// unowned LEDs; an explicit full-controller test can safely wake its range.
	off := centreAuthenticatedCall(f.h.HandleLEDEmergencyOff, `{"confirm":"EMERGENCY OFF"}`, 1)
	if off.Code != 200 || sim.State().On {
		t.Fatalf("emergency off=%d state=%+v", off.Code, sim.State())
	}
	partial := centreAuthenticatedCall(f.h.HandleLEDStart, `{"controller_id":1,"start":0,"count":1,"duration_seconds":30,"function":"test_pixel"}`, 1)
	if partial.Code != 409 || sim.State().On {
		t.Fatalf("partial test woke unowned controller: %d", partial.Code)
	}
	full := centreAuthenticatedCall(f.h.HandleLEDStart, `{"controller_id":1,"start":0,"count":100,"duration_seconds":30,"function":"test_pixel","color":"#FFFFFF","brightness":128}`, 1)
	if full.Code != 200 || !sim.State().On {
		t.Fatalf("full controller test=%d %s", full.Code, full.Body.String())
	}
	var fullResult struct {
		Operation wled.PhysicalLocateSession `json:"operation"`
	}
	if err := json.Unmarshal(full.Body.Bytes(), &fullResult); err != nil {
		t.Fatal(err)
	}
	defer f.h.Locator.StopOperation(ctx, fullResult.Operation.ID, 1)
	if got := sim.Pixels()[99]; got != (wledsim.RGB{128, 128, 128}) {
		t.Fatalf("last controller pixel=%v", got)
	}
}

func TestCentreStrictJSONEveryMutation(t *testing.T) {
	f := newLEDHTTPFixture(t)
	endpoints := map[string]http.HandlerFunc{"inspect": f.h.HandleLEDInspect, "start": f.h.HandleLEDStart, "stop": f.h.HandleLEDStop, "emergency-off": f.h.HandleLEDEmergencyOff, "mapping-preview": f.h.HandleLEDMappingPreview, "mapping-save": f.h.HandleLEDMappingSave, "bin-pixel": f.h.HandleLEDBinPixel}
	for name, handle := range endpoints {
		for _, tc := range []struct{ name, body string }{
			{"malformed", `{"`}, {"unknown", `{"unexpected":true}`}, {"trailing", `{} {}`}, {"oversize", strings.Repeat(" ", 129<<10) + `{}`},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				w := ledHTTPCall(t, handle, tc.body)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
					t.Fatal("error is not JSON")
				}
			})
		}
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("invalid JSON contacted controller")
	}
}

func TestCentreGETPageDataNeverContactController(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newLEDHTTPFixture(t)
	if err := i18n.InitWithDir("../../locales"); err != nil {
		t.Fatal(err)
	}
	for name, handle := range map[string]http.HandlerFunc{"page": f.h.HandleLEDTestCentre, "data": f.h.HandleLEDTestCentreData} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/hardware/led-test-centre", nil)
			r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: 1, Role: "editor"}))
			w := httptest.NewRecorder()
			handle(w, r)
			if w.Code != 200 {
				t.Fatalf("GET=%d %s", w.Code, w.Body.String())
			}
		})
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatalf("GET sent %d controller requests", f.hardwareCalls.Load())
	}
}

func TestCentreEmergencyRequiresExactConfirmation(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newLEDHTTPFixture(t)
	for _, body := range []string{`{}`, `{"confirm":""}`, `{"confirm":"emergency off"}`, `{"confirm":"EMERGENCY OFF "}`} {
		w := ledHTTPCall(t, f.h.HandleLEDEmergencyOff, body)
		if w.Code != 400 {
			t.Fatalf("unconfirmed request=%d %s", w.Code, w.Body.String())
		}
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("unconfirmed emergency contacted controller")
	}
}

func TestCentreStartRejectsInvalidInputsBeforeContact(t *testing.T) {
	f := newLEDHTTPFixture(t)
	for _, body := range []string{
		`{"controller_id":1,"count":1,"duration_seconds":0}`,
		`{"controller_id":1,"count":1,"duration_seconds":3601}`,
		`{"controller_id":1,"count":1,"duration_seconds":1,"function":"test_pixel","brightness":-1}`,
		`{"controller_id":1,"count":1,"duration_seconds":1,"function":"test_pixel","brightness":256}`,
		`{"controller_id":1,"count":1,"duration_seconds":1,"function":"test_pixel","mode":"rainbow"}`,
	} {
		w := ledHTTPCall(t, f.h.HandleLEDStart, body)
		if w.Code != 400 {
			t.Fatalf("invalid start=%d %s", w.Code, w.Body.String())
		}
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("invalid start contacted controller")
	}
}

func TestCentreFunctionSettingsCustomZeroAvailableInData(t *testing.T) {
	f := newLEDHTTPFixture(t)
	ctx := context.Background()
	if err := wled.SaveFunctionSettings(ctx, f.h.Queries, wled.FunctionSettings{"test_pixel": {Color: "#123456", Brightness: 0}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.h.HandleLEDTestCentreData(w, httptest.NewRequest(http.MethodGet, "/data", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"#123456"`) || !strings.Contains(w.Body.String(), `"brightness":0`) {
		t.Fatalf("saved zero style absent: %d %s", w.Code, w.Body.String())
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("settings data contacted controller")
	}
}
