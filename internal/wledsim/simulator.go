// Package wledsim provides an in-memory, RGB-only subset of WLED's JSON API.
// It makes no outbound network calls and implements http.Handler for local tests.
package wledsim

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Output describes an electrical output in controller-wide coordinates.
type Output struct {
	Start   int   `json:"start"`
	Length  int   `json:"len"`
	Reverse bool  `json:"rev"`
	Pins    []int `json:"pin"`
	Order   int   `json:"order"`
	Type    int   `json:"type"`
	Skip    int   `json:"skip"`
}

type Config struct {
	Count int
	// MAC may be supplied for stable identity across restarts. Empty generates a
	// random locally administered unicast address, stable for this instance.
	MAC     string
	Outputs []Output
}

type RGB [3]uint8

type Segment struct {
	ID         int   `json:"id"`
	Start      int   `json:"start"`
	Stop       int   `json:"stop"`
	On         bool  `json:"on"`
	Brightness uint8 `json:"bri"`
	FX         int   `json:"fx"`
	Group      int   `json:"grp"`
	Space      int   `json:"spc"`
	Offset     int   `json:"of"`
	Reverse    bool  `json:"rev"`
	Mirror     bool  `json:"mi"`
	Colors     []RGB `json:"col"`
}

type State struct {
	On         bool      `json:"on"`
	Brightness uint8     `json:"bri"`
	Segments   []Segment `json:"seg"`
}

// Fault applies to future requests until replaced. Offline returns 503 (not a
// simulated TCP disconnect); Status optionally injects an HTTP error. Delay is
// context-aware and happens before any state mutation.
type Fault struct {
	Offline bool
	Status  int
	Delay   time.Duration
}

// Command records every received request, including rejected and canceled ones.
// Body is bounded to 1 MiB. Sequence reflects arrival/record order, not completion.
type Command struct {
	Sequence int
	Method   string
	Path     string
	Body     json.RawMessage
}

type Simulator struct {
	mu       sync.Mutex
	config   Config
	state    State
	pixels   []RGB
	fault    Fault
	commands []Command
}

func New(config Config) (*Simulator, error) {
	if config.Count <= 0 || config.Count > 1000000 {
		return nil, errors.New("count must be between 1 and 1000000")
	}
	if config.MAC == "" {
		b := make([]byte, 6)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		b[0] = (b[0] | 2) & 0xfe
		config.MAC = net.HardwareAddr(b).String()
	}
	mac, err := net.ParseMAC(config.MAC)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 || bytes.Equal(mac, make([]byte, 6)) {
		return nil, errors.New("MAC must be a nonzero six-byte unicast address")
	}
	config.MAC = mac.String()
	if len(config.Outputs) == 0 {
		config.Outputs = []Output{{Length: config.Count, Pins: []int{2}, Type: 22}}
	}
	outputs := make([]Output, len(config.Outputs))
	for i, output := range config.Outputs {
		if output.Start < 0 || output.Length <= 0 || output.Start > config.Count-output.Length {
			return nil, fmt.Errorf("output %d outside LED count", i)
		}
		if len(output.Pins) == 0 {
			return nil, fmt.Errorf("output %d needs pins", i)
		}
		output.Pins = append([]int(nil), output.Pins...)
		if output.Type == 0 {
			output.Type = 22
		}
		outputs[i] = output
	}
	config.Outputs = outputs
	return &Simulator{config: config, state: State{On: true, Brightness: 128, Segments: []Segment{{ID: 0, Stop: config.Count, On: true, Brightness: 255, Group: 1, Colors: []RGB{{}, {}, {}}}}}, pixels: make([]RGB, config.Count)}, nil
}

func (s *Simulator) SetFault(f Fault) error {
	if f.Delay < 0 || (f.Status != 0 && (f.Status < 400 || f.Status > 599)) {
		return errors.New("fault requires nonnegative delay and HTTP error status 400..599 or zero")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = f
	return nil
}

func cloneState(state State) State {
	state.Segments = append([]Segment(nil), state.Segments...)
	for i := range state.Segments {
		state.Segments[i].Colors = append([]RGB(nil), state.Segments[i].Colors...)
	}
	return state
}

func (s *Simulator) State() State { s.mu.Lock(); defer s.mu.Unlock(); return cloneState(s.state) }

// Pixels returns raw RGB values in controller-wide logical order; output reverse
// is reported in cfg, not applied a second time to logical coordinates.
func (s *Simulator) Pixels() []RGB {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RGB(nil), s.pixels...)
}

// RenderedPixels applies master/segment on flags and brightness (integer floor).
// This is a deterministic approximation, not WLED gamma/current limiting.
func (s *Simulator) RenderedPixels() []RGB {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]RGB, len(s.pixels))
	if !s.state.On {
		return result
	}
	for _, seg := range s.state.Segments {
		if !seg.On {
			continue
		}
		for i := seg.Start; i < seg.Stop; i++ {
			for c := range 3 {
				result[i][c] = uint8(uint32(s.pixels[i][c]) * uint32(s.state.Brightness) * uint32(seg.Brightness) / (255 * 255))
			}
		}
	}
	return result
}

func (s *Simulator) Commands() []Command {
	s.mu.Lock()
	defer s.mu.Unlock()
	commands := append([]Command(nil), s.commands...)
	for i := range commands {
		commands[i].Body = append(json.RawMessage(nil), commands[i].Body...)
	}
	return commands
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func (s *Simulator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	s.mu.Lock()
	s.commands = append(s.commands, Command{Sequence: len(s.commands) + 1, Method: r.Method, Path: r.URL.Path, Body: append(json.RawMessage(nil), body[:min(len(body), 1<<20)]...)})
	fault := s.fault
	s.mu.Unlock()
	if err != nil || len(body) > 1<<20 {
		fail(w, http.StatusBadRequest, "invalid or oversized body")
		return
	}
	if fault.Delay > 0 {
		timer := time.NewTimer(fault.Delay)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
	}
	if r.Context().Err() != nil {
		return
	}
	if fault.Offline {
		fail(w, http.StatusServiceUnavailable, "simulator offline")
		return
	}
	if fault.Status != 0 {
		fail(w, fault.Status, "injected error")
		return
	}
	if r.URL.Path != "/json/info" && r.URL.Path != "/json/cfg" && r.URL.Path != "/json/state" {
		fail(w, 404, "unknown endpoint")
		return
	}
	if r.Method != http.MethodGet && !(r.Method == http.MethodPost && r.URL.Path == "/json/state") {
		w.Header().Set("Allow", "GET")
		if r.URL.Path == "/json/state" {
			w.Header().Set("Allow", "GET, POST")
		}
		fail(w, 405, "unsupported method")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/json/info":
		writeJSON(w, 200, map[string]any{"ver": "0.15.0", "arch": "esp32", "name": "WLEDger simulator", "mac": s.config.MAC, "leds": map[string]any{"count": s.config.Count, "rgbw": false, "maxseg": 32}})
	case "/json/cfg":
		writeJSON(w, 200, map[string]any{"hw": map[string]any{"led": map[string]any{"total": s.config.Count, "maxseg": 32, "ins": s.config.Outputs, "matrix": false, "map": 0, "ledmap": 0}}})
	case "/json/state":
		if r.Method == http.MethodGet {
			writeJSON(w, 200, s.state)
			return
		}
		state, pixels, err := s.update(body)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		s.state, s.pixels = state, pixels
		writeJSON(w, 200, map[string]bool{"success": true})
	}
}

type segmentPatch struct {
	ID         *int              `json:"id"`
	Start      *int              `json:"start"`
	Stop       *int              `json:"stop"`
	On         *bool             `json:"on"`
	Brightness *uint8            `json:"bri"`
	FX         *int              `json:"fx"`
	Colors     []json.RawMessage `json:"col"`
	Pixels     []json.RawMessage `json:"i"`
}

func color(raw json.RawMessage) (RGB, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		b, err := hex.DecodeString(text)
		if err != nil || len(b) != 3 {
			return RGB{}, errors.New("color must be six hex digits")
		}
		return RGB{b[0], b[1], b[2]}, nil
	}
	var channels []uint8
	if err := json.Unmarshal(raw, &channels); err != nil || len(channels) != 3 {
		return RGB{}, errors.New("color must be RGB")
	}
	return RGB{channels[0], channels[1], channels[2]}, nil
}

func (s *Simulator) update(body []byte) (State, []RGB, error) {
	var patch struct {
		On         *bool           `json:"on"`
		Brightness *uint8          `json:"bri"`
		Segments   json.RawMessage `json:"seg"`
	}
	if len(bytes.TrimSpace(body)) == 0 || bytes.TrimSpace(body)[0] != '{' {
		return State{}, nil, errors.New("expected state object")
	}
	if err := json.Unmarshal(body, &patch); err != nil {
		return State{}, nil, err
	}
	state, pixels := cloneState(s.state), append([]RGB(nil), s.pixels...)
	if patch.On != nil {
		state.On = *patch.On
	}
	if patch.Brightness != nil {
		state.Brightness = *patch.Brightness
	}
	var patches []segmentPatch
	if len(patch.Segments) > 0 {
		raw := bytes.TrimSpace(patch.Segments)
		if raw[0] == '{' {
			var p segmentPatch
			if err := json.Unmarshal(raw, &p); err != nil {
				return state, nil, err
			}
			patches = []segmentPatch{p}
		} else if err := json.Unmarshal(raw, &patches); err != nil {
			return state, nil, err
		}
	}
	for _, p := range patches {
		id := 0
		if p.ID != nil {
			id = *p.ID
		}
		if id < 0 || id >= 32 {
			return state, nil, errors.New("segment id outside 0..31")
		}
		index := -1
		for i := range state.Segments {
			if state.Segments[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			if p.Start == nil || p.Stop == nil {
				return state, nil, errors.New("new segment needs start and stop")
			}
			state.Segments = append(state.Segments, Segment{ID: id, On: true, Brightness: 255, Group: 1, Colors: []RGB{{}, {}, {}}})
			index = len(state.Segments) - 1
		}
		seg := &state.Segments[index]
		if p.Start != nil {
			seg.Start = *p.Start
		}
		if p.Stop != nil {
			seg.Stop = *p.Stop
		}
		if seg.Start < 0 || seg.Stop <= seg.Start || seg.Stop > len(pixels) {
			return state, nil, errors.New("invalid segment bounds")
		}
		for i, other := range state.Segments {
			if i != index && seg.Start < other.Stop && other.Start < seg.Stop {
				return state, nil, errors.New("overlapping segments unsupported")
			}
		}
		if p.On != nil {
			seg.On = *p.On
		}
		if p.Brightness != nil {
			seg.Brightness = *p.Brightness
		}
		if p.FX != nil && *p.FX != 0 {
			return state, nil, errors.New("only solid effect fx=0 supported")
		}
		if len(p.Colors) > 3 {
			return state, nil, errors.New("at most three segment colors")
		}
		for i, raw := range p.Colors {
			c, err := color(raw)
			if err != nil {
				return state, nil, err
			}
			seg.Colors[i] = c
		}
		if len(p.Colors) > 0 {
			for i := seg.Start; i < seg.Stop; i++ {
				pixels[i] = seg.Colors[0]
			}
		}
		for i := 0; i < len(p.Pixels); {
			var start int
			if err := json.Unmarshal(p.Pixels[i], &start); err != nil {
				return state, nil, errors.New("pixel command needs index")
			}
			i++
			stop := start + 1
			if i < len(p.Pixels) {
				var end int
				if json.Unmarshal(p.Pixels[i], &end) == nil {
					stop = end
					i++
				}
			}
			if i >= len(p.Pixels) || start < 0 || stop <= start || stop > seg.Stop-seg.Start {
				return state, nil, errors.New("invalid segment-relative pixel range")
			}
			c, err := color(p.Pixels[i])
			if err != nil {
				return state, nil, err
			}
			i++
			for j := start; j < stop; j++ {
				pixels[seg.Start+j] = c
			}
		}
	}
	return state, pixels, nil
}
