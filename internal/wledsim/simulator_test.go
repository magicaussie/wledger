package wledsim

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTest(t *testing.T) (*Simulator, *httptest.Server) {
	t.Helper()
	s, err := New(Config{Count: 10, MAC: "02:11:22:33:44:55", Outputs: []Output{{Start: 0, Length: 4, Pins: []int{2, -1, -1}}, {Start: 4, Length: 6, Reverse: true, Pins: []int{4, -1, -1}, Order: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)
	return s, server
}

func request(t *testing.T, server *httptest.Server, method, path, body string, want int) []byte {
	t.Helper()
	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d want %d: %s", method, path, resp.StatusCode, want, data)
	}
	if resp.Header.Get("Content-Type") != "application/json" {
		t.Fatal("missing JSON content type")
	}
	return data
}

func TestDiscoveryAndIdentity(t *testing.T) {
	_, server := newTest(t)
	var info struct {
		MAC     string `json:"mac"`
		Version string `json:"ver"`
		Arch    string `json:"arch"`
		LEDs    struct {
			Count  int
			RGBW   bool
			Maxseg int
		}
	}
	if err := json.Unmarshal(request(t, server, "GET", "/json/info", "", 200), &info); err != nil {
		t.Fatal(err)
	}
	if info.MAC != "02:11:22:33:44:55" || info.LEDs.Count != 10 || info.LEDs.RGBW || info.LEDs.Maxseg != 32 || info.Version != "0.15.0" || info.Arch != "esp32" {
		t.Fatalf("info %+v", info)
	}
	var cfg struct {
		HW struct {
			LED struct {
				Ins   []Output
				Total int
			}
		}
	}
	if err := json.Unmarshal(request(t, server, "GET", "/json/cfg", "", 200), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.HW.LED.Total != 10 || len(cfg.HW.LED.Ins) != 2 || cfg.HW.LED.Ins[1].Start != 4 || !cfg.HW.LED.Ins[1].Reverse || cfg.HW.LED.Ins[1].Pins[0] != 4 {
		t.Fatalf("cfg %+v", cfg)
	}
	var state State
	if err := json.Unmarshal(request(t, server, "GET", "/json/state", "", 200), &state); err != nil {
		t.Fatal(err)
	}
	if !state.On || state.Segments[0].FX != 0 || !state.Segments[0].On || state.Segments[0].Stop != 10 {
		t.Fatalf("state %+v", state)
	}
	a, _ := New(Config{Count: 1})
	b, _ := New(Config{Count: 1})
	if a.config.MAC == b.config.MAC {
		t.Fatal("generated identities are identical")
	}
	mac, err := net.ParseMAC(a.config.MAC)
	if err != nil || mac[0]&1 != 0 || mac[0]&2 == 0 {
		t.Fatalf("generated MAC %v %v", mac, err)
	}
}

func TestPixelsBrightnessAndAtomicRejection(t *testing.T) {
	s, server := newTest(t)
	data := request(t, server, "POST", "/json/state", `{"on":true,"bri":128,"tt":0,"seg":[{"id":0,"on":true,"bri":128,"fx":0,"col":[[255,100,0]],"i":[2,4,[0,255,0],6,"0000ff"]}]}`, 200)
	var ack struct{ Success bool }
	if json.Unmarshal(data, &ack) != nil || !ack.Success {
		t.Fatalf("ack %s", data)
	}
	if s.Pixels()[0] != (RGB{255, 100, 0}) || s.Pixels()[2] != (RGB{0, 255, 0}) || s.Pixels()[3] != (RGB{0, 255, 0}) || s.Pixels()[4] != (RGB{255, 100, 0}) || s.Pixels()[6] != (RGB{0, 0, 255}) {
		t.Fatalf("pixels %v", s.Pixels())
	}
	if s.RenderedPixels()[0] != (RGB{64, 25, 0}) {
		t.Fatalf("rendered %v", s.RenderedPixels())
	}
	before, _ := json.Marshal(s.State())
	beforePixels := s.Pixels()
	for _, invalid := range []string{`null`, `[]`, `{"bri":256}`, `{"seg":{"id":0,"fx":1}}`, `{"on":false,"seg":{"id":0,"i":[9,11,[1,2,3]]}}`, `{"seg":{"id":0,"col":[[256,0,0]]}}`, `{"seg":{"id":32}}`, `{"seg":{"id":0,"i":[-1,[1,2,3]]}}`, `{"seg":{"id":0,"i":[2]}}`, `{"seg":{"id":0,"col":["bad"]}}`, `{"seg":{"id":0,"start":-1}}`, `{"on":true} trailing`} {
		request(t, server, "POST", "/json/state", invalid, 400)
		after, _ := json.Marshal(s.State())
		if string(before) != string(after) {
			t.Fatalf("state mutated by %s", invalid)
		}
		for i, p := range s.Pixels() {
			if p != beforePixels[i] {
				t.Fatalf("pixels mutated by %s", invalid)
			}
		}
	}
	request(t, server, "POST", "/json/state", `{"seg":{"id":0,"on":false}}`, 200)
	if s.RenderedPixels()[0] != (RGB{}) || s.Pixels()[0] == (RGB{}) {
		t.Fatal("segment off lost raw pixels or still rendered")
	}
	request(t, server, "POST", "/json/state", `{"on":false,"seg":{"id":0,"on":true}}`, 200)
	if s.RenderedPixels()[0] != (RGB{}) {
		t.Fatal("master off still rendered")
	}
	request(t, server, "POST", "/json/state", `{"on":true,"bri":0}`, 200)
	if s.RenderedPixels()[0] != (RGB{}) {
		t.Fatal("brightness zero still rendered")
	}
}

func TestBrightnessExtremes(t *testing.T) {
	s, server := newTest(t)
	for _, level := range []struct {
		body string
		want RGB
	}{
		{`{"bri":255,"seg":{"id":0,"bri":255,"col":[[255,120,30]]}}`, RGB{255, 120, 30}},
		{`{"seg":{"id":0,"bri":0}}`, RGB{}},
		{`{"bri":1,"seg":{"id":0,"bri":255}}`, RGB{1, 0, 0}},
	} {
		request(t, server, "POST", "/json/state", level.body, 200)
		if got := s.RenderedPixels()[0]; got != level.want {
			t.Fatalf("rendered %v want %v", got, level.want)
		}
	}
}

func TestOversizedRequest(t *testing.T) {
	s, server := newTest(t)
	request(t, server, "POST", "/json/state", strings.Repeat(" ", (1<<20)+1), 400)
	if len(s.Commands()) != 1 || len(s.Commands()[0].Body) != 1<<20 || !s.State().On {
		t.Fatal("oversized body not bounded or mutated state")
	}
}

func TestSegmentRelativeCoordinates(t *testing.T) {
	s, server := newTest(t)
	request(t, server, "POST", "/json/state", `{"seg":[{"id":0,"stop":4},{"id":7,"start":4,"stop":10,"i":[1,3,[9,8,7]]}]}`, 200)
	if s.Pixels()[1] != (RGB{}) || s.Pixels()[5] != (RGB{9, 8, 7}) || s.Pixels()[6] != (RGB{9, 8, 7}) || s.Pixels()[7] != (RGB{}) {
		t.Fatalf("relative pixels %v", s.Pixels())
	}
}

func TestFaultsTimeoutAndMethods(t *testing.T) {
	s, server := newTest(t)
	request(t, server, "GET", "/control", "", 404)
	request(t, server, "POST", "/json/cfg", `{}`, 405)
	request(t, server, "DELETE", "/json/state", "", 405)
	if err := s.SetFault(Fault{Offline: true}); err != nil {
		t.Fatal(err)
	}
	request(t, server, "POST", "/json/state", `{"on":false}`, 503)
	if !s.State().On {
		t.Fatal("offline request mutated state")
	}
	if err := s.SetFault(Fault{Status: 500}); err != nil {
		t.Fatal(err)
	}
	request(t, server, "GET", "/json/info", "", 500)
	if err := s.SetFault(Fault{Delay: 200 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/json/state", strings.NewReader(`{"on":false}`))
	resp, err := server.Client().Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected client deadline")
	}
	// A subsequent request serializes with any completed handler; wait beyond the
	// injected delay to prove the canceled command cannot mutate state later.
	time.Sleep(250 * time.Millisecond)
	if !s.State().On {
		t.Fatal("canceled request mutated state")
	}
	if err := s.SetFault(Fault{}); err != nil {
		t.Fatal(err)
	}
	request(t, server, "GET", "/json/info", "", 200)
	if len(s.Commands()) != 7 {
		t.Fatalf("command log %v", s.Commands())
	}
	if s.SetFault(Fault{Status: 200}) == nil || s.SetFault(Fault{Delay: -1}) == nil {
		t.Fatal("accepted invalid fault")
	}
}

func TestConcurrentAccessAndDefensiveCopies(t *testing.T) {
	s, server := newTest(t)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			request(t, server, "POST", "/json/state", `{"seg":{"id":0,"i":[0,2,[1,2,3]]}}`, 200)
			state := s.State()
			state.Segments[0].Colors[0] = RGB{99, 99, 99}
			pixels := s.Pixels()
			pixels[0] = RGB{99, 99, 99}
			commands := s.Commands()
			commands[0].Body[0] = '!'
			_ = s.RenderedPixels()
			if err := s.SetFault(Fault{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if len(s.Commands()) != 20 || s.Commands()[0].Body[0] != '{' || s.State().Segments[0].Colors[0] != (RGB{}) || s.Pixels()[0] != (RGB{1, 2, 3}) {
		t.Fatal("snapshot alias or lost commands")
	}
	for i, command := range s.Commands() {
		if command.Sequence != i+1 {
			t.Fatal("unordered command log")
		}
	}
}

func TestConfigValidationAndOwnership(t *testing.T) {
	for _, cfg := range []Config{{}, {Count: -1}, {Count: 1000001}, {Count: 1, MAC: "bad"}, {Count: 1, MAC: "01:00:00:00:00:01"}, {Count: 1, MAC: "00:00:00:00:00:00"}, {Count: 1, Outputs: []Output{{Length: 2, Pins: []int{2}}}}, {Count: 1, Outputs: []Output{{Length: 1}}}} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
	cfg := Config{Count: 3, Outputs: []Output{{Length: 3, Pins: []int{2}}}}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Outputs[0].Pins[0] = 99
	cfg.Outputs[0].Length = 99
	if s.config.Outputs[0].Pins[0] != 2 || s.config.Outputs[0].Length != 3 {
		t.Fatal("config aliases caller")
	}
}
