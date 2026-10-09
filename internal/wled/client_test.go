package wled

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestClient_ApplySolid(t *testing.T) {
	var mu sync.Mutex
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	c := NewClient()
	if err := c.Apply(context.Background(), ip, 0, 3, 4, State{Color: "#112233", Mode: ModeSolid}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body, "17,34,51") { // #112233
		t.Errorf("expected colour #112233, got %s", body)
	}
	if !strings.Contains(body, `"i":[3,7`) {
		t.Errorf("expected LED range 3->7, got %s", body)
	}
}

func TestService_FlashError(t *testing.T) {
	// Shorten the blink loop for the test.
	origTimes, origInterval := flashTimes, flashInterval
	flashTimes, flashInterval = 2, 5*time.Millisecond
	defer func() { flashTimes, flashInterval = origTimes, origInterval }()

	var mu sync.Mutex
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/state" {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, string(b))
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	dbConn, err := db.Open("file:flash_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	if err := store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorError: sql.NullString{String: "#ABCDEF", Valid: true},
	}); err != nil {
		t.Fatalf("update colors: %v", err)
	}

	c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	cont, err := store.CreateContainer(ctx, db.CreateContainerParams{Name: "Cont", ControllerID: c.ID, SegmentID: 0})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	bin, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        "B",
		ContainerID: cont,
		LedIndex:    sql.NullInt64{Int64: 5, Valid: true},
		Width:       sql.NullInt64{Int64: 2, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := svc.FlashError(ctx, c.ID, bin); err != nil {
		t.Fatalf("FlashError: %v", err)
	}

	// Wait for the background blink loop to finish.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(bodies)
		mu.Unlock()
		if n >= flashTimes*2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) < flashTimes*2 {
		t.Fatalf("expected at least %d state requests, got %d", flashTimes*2, len(bodies))
	}
	joined := strings.Join(bodies, "\n")
	if !strings.Contains(joined, "171,205,239") { // #ABCDEF
		t.Errorf("expected error colour #ABCDEF in flash, got: %s", joined)
	}
	if !strings.Contains(joined, "0,0,0") {
		t.Errorf("expected off frames in flash")
	}
	if !strings.Contains(joined, `"i":[5,7`) {
		t.Errorf("expected LED range 5->7, got: %s", joined)
	}
}

// TestClient_ClearPayload verifies Clear performs a device-wide power-off and
// sends no segment array or individual-pixel range.
func TestClient_ClearPayload(t *testing.T) {
	var mu sync.Mutex
	var body, method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		method = r.Method
		path = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	if err := NewClient().Clear(context.Background(), ip); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if method != http.MethodPost {
		t.Errorf("Clear must POST, got %s", method)
	}
	if path != "/json/state" {
		t.Errorf("Clear must target /json/state, got %s", path)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("Clear body is not valid JSON: %q: %v", body, err)
	}
	if on, ok := payload["on"].(bool); !ok || on {
		t.Errorf("Clear must send on:false, got %v", payload["on"])
	}
	if live, ok := payload["live"].(bool); !ok || live {
		t.Errorf("Clear must send live:false, got %v", payload["live"])
	}
	if tt, ok := payload["tt"].(float64); !ok || tt != 0 {
		t.Errorf("Clear must send tt:0, got %v", payload["tt"])
	}
	if _, ok := payload["seg"]; ok {
		t.Errorf("Clear must not send a segment array, got %s", body)
	}
	if _, ok := payload["i"]; ok {
		t.Errorf("Clear must not send an individual-pixel range, got %s", body)
	}
	if strings.Contains(body, "5000") {
		t.Errorf("Clear must not contain the hardcoded 5000-pixel wipe, got %s", body)
	}
}

// TestClient_ClearNoSegmentDiscovery verifies Clear needs no LED-count or
// segment discovery: it issues exactly one /json/state request and never
// queries /json/info.
func TestClient_ClearNoSegmentDiscovery(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	if err := NewClient().Clear(context.Background(), ip); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/json/state" {
		t.Fatalf("Clear should issue exactly one /json/state request, got %v", paths)
	}
}

// TestClient_ClearHTTPError verifies an HTTP error from the controller is
// propagated.
func TestClient_ClearHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	ip := server.URL[7:]

	if err := NewClient().Clear(context.Background(), ip); err == nil {
		t.Fatal("Clear should return an error on HTTP 500")
	}
}

// TestClient_ClearUnavailableController verifies an unreachable controller is
// reported as an error rather than silently succeeding.
func TestClient_ClearUnavailableController(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ip := server.URL[7:]
	server.Close() // now unreachable

	if err := NewClient().Clear(context.Background(), ip); err == nil {
		t.Fatal("Clear should return an error when the controller is unreachable")
	}
}

// TestClient_ApplyDoesNotPowerOff ensures locate-style highlighting still turns
// the device on and paints a segment-relative range, i.e. it stays distinct
// from Clear and keeps working with nonzero segment IDs and offsets.
func TestClient_ApplyDoesNotPowerOff(t *testing.T) {
	var mu sync.Mutex
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	if err := NewClient().Apply(context.Background(), ip, 2, 10, 5, State{Color: "#00FF00", Mode: ModeSolid}); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("Apply body is not valid JSON: %q: %v", body, err)
	}
	if on, ok := payload["on"].(bool); !ok || !on {
		t.Errorf("Apply must send on:true, got %v", payload["on"])
	}
	if !strings.Contains(body, `"i":[10,15`) {
		t.Errorf("Apply must send the segment-relative range 10->15, got %s", body)
	}
	if !strings.Contains(body, `"id":2`) {
		t.Errorf("Apply must target the requested segment id 2, got %s", body)
	}
	if strings.Contains(body, `"on":false`) {
		t.Errorf("Apply must not power the device off, got %s", body)
	}
}
