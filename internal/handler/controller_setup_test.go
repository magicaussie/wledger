package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestControllerSetupDiscoveryOnlyReadsExplicitTarget(t *testing.T) {
	calls := []string{}
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/json/info":
			_, _ = w.Write([]byte(`{"ver":"0.15.0","arch":"esp32","leds":{"count":16}}`))
		case "/json/cfg":
			_, _ = w.Write([]byte(`{"hw":{"led":{"total":16,"ins":[{"start":0,"len":9,"pin":[2],"type":22},{"start":9,"len":7,"pin":[4],"type":22,"rev":true}]}}}`))
		default:
			t.Error("unexpected device request")
			w.WriteHeader(404)
		}
	}))
	defer device.Close()
	u, _ := url.Parse(device.URL)
	port, _ := strconv.ParseInt(u.Port(), 10, 64)
	body, _ := json.Marshal(map[string]any{"name": "Explicit discovery", "ip_address": u.Hostname(), "port": port})
	req := httptest.NewRequest("POST", "/hardware/setup/discover", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	(&Handler{}).HandleControllerSetupDiscover(w, req)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if len(calls) != 2 || calls[0] != "GET /json/info" || calls[1] != "GET /json/cfg" {
		t.Fatal(calls)
	}
	var result struct {
		Count   int64 `json:"led_count"`
		Outputs []struct {
			Count   int64 `json:"pixel_count"`
			Reverse bool  `json:"hardware_reversed"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Count != 16 || len(result.Outputs) != 2 || result.Outputs[1].Count != 7 || !result.Outputs[1].Reverse {
		t.Fatal(w.Body.String())
	}
}

func TestControllerSetupDiscoveryDoesNotFollowRedirects(t *testing.T) {
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++; w.WriteHeader(500) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	u, _ := url.Parse(redirect.URL)
	port, _ := strconv.Atoi(u.Port())
	body, _ := json.Marshal(map[string]any{"name": "Redirect", "ip_address": u.Hostname(), "port": port})
	req := httptest.NewRequest("POST", "/hardware/setup/discover", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	(&Handler{}).HandleControllerSetupDiscover(w, req)
	if w.Code != 502 || targetCalls != 0 {
		t.Fatalf("status=%d redirected calls=%d", w.Code, targetCalls)
	}
}
