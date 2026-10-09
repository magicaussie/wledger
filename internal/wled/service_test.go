package wled

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

func TestService_Locate(t *testing.T) {
	// Setup a mock WLED server
	receivedColor := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/state" {
			body, _ := io.ReadAll(r.Body)
			receivedColor = string(body)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	// Extract IP/Port from server URL (e.g. 127.0.0.1:12345)
	ip := server.URL[7:]

	// Setup DB
	dbConn, err := db.Open("file:wled_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()

	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	store := db.NewStore(dbConn)
	ctx := context.Background()

	// Seed Settings
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}
	err = store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate: sql.NullString{String: "#FF0000", Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to update colors: %v", err)
	}

	client := NewClient()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(store, client, logger)

	t.Run("LocateBin", func(t *testing.T) {
		c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test", IpAddress: ip})
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}

		cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
			Name:         "Cont",
			ControllerID: c.ID,
			SegmentID:    0,
		})
		if err != nil {
			t.Fatalf("failed to create container: %v", err)
		}

		b, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin: %v", err)
		}

		err = svc.LocateBin(ctx, c.ID, b)
		if err != nil {
			t.Fatalf("LocateBin failed: %v", err)
		}

		// Verify that client sent the correct color
		if receivedColor == "" {
			t.Error("No color received by mock server")
		}
	})

	t.Run("LocateBinMultiLedWidth", func(t *testing.T) {
		receivedColor = ""
		c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test2", IpAddress: ip})
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}

		cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
			Name:         "Cont2",
			ControllerID: c.ID,
			SegmentID:    0,
		})
		if err != nil {
			t.Fatalf("failed to create container: %v", err)
		}

		// Bin at 0-based LED index 685 spanning 15 LEDs (physical LEDs 686-700).
		b, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "B2", ContainerID: cont,
			LedIndex: sql.NullInt64{Int64: 685, Valid: true},
			Width:    sql.NullInt64{Int64: 15, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin: %v", err)
		}

		if err := svc.LocateBin(ctx, c.ID, b); err != nil {
			t.Fatalf("LocateBin failed: %v", err)
		}

		if !strings.Contains(receivedColor, `"i":[685,700`) {
			t.Errorf("expected individual LED range 685->700, got %s", receivedColor)
		}
	})

	t.Run("LocateBinWidthDefaultsToOne", func(t *testing.T) {
		receivedColor = ""
		c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test3", IpAddress: ip})
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}

		cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
			Name:         "Cont3",
			ControllerID: c.ID,
			SegmentID:    0,
		})
		if err != nil {
			t.Fatalf("failed to create container: %v", err)
		}

		// Width 0 / missing should behave as width 1: range end == index+1.
		b, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "B3", ContainerID: cont,
			LedIndex: sql.NullInt64{Int64: 685, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin: %v", err)
		}

		if err := svc.LocateBin(ctx, c.ID, b); err != nil {
			t.Fatalf("LocateBin failed: %v", err)
		}

		if !strings.Contains(receivedColor, `"i":[685,686`) {
			t.Errorf("expected single LED range 685->686, got %s", receivedColor)
		}
	})
}

// TestService_GlobalOff verifies GlobalOff powers off every controller with a
// device-wide power-off payload, and keeps going when one controller fails.
func TestService_GlobalOff(t *testing.T) {
	var mu sync.Mutex
	var okBody string
	var okHit, failHit bool
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		okBody = string(b)
		okHit = true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer okServer.Close()

	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		failHit = true
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failServer.Close()

	dbConn, err := db.Open("file:globaloff_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	store := db.NewStore(dbConn)
	ctx := context.Background()

	if _, err := store.CreateController(ctx, db.CreateControllerParams{Name: "OK", IpAddress: okServer.URL[7:]}); err != nil {
		t.Fatalf("failed to create healthy controller: %v", err)
	}
	if _, err := store.CreateController(ctx, db.CreateControllerParams{Name: "FAIL", IpAddress: failServer.URL[7:]}); err != nil {
		t.Fatalf("failed to create failing controller: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.GlobalOff(ctx); err != nil {
		t.Fatalf("GlobalOff: %v", err)
	}

	// GlobalOff dispatches per controller in the background; wait for BOTH the
	// healthy and the failing controller to be attempted, with a bounded timeout.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := okHit && failHit
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !okHit {
		t.Fatal("GlobalOff did not reach the healthy controller")
	}
	if !failHit {
		t.Fatal("GlobalOff did not attempt the failing controller")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(okBody), &payload); err != nil {
		t.Fatalf("GlobalOff body is not valid JSON: %q: %v", okBody, err)
	}
	if on, ok := payload["on"].(bool); !ok || on {
		t.Errorf("GlobalOff must send on:false, got %v", payload["on"])
	}
	if _, ok := payload["seg"]; ok {
		t.Errorf("GlobalOff must not send a segment array, got %s", okBody)
	}
	if strings.Contains(okBody, "5000") {
		t.Errorf("GlobalOff must not contain the hardcoded 5000-pixel wipe, got %s", okBody)
	}
}

// TestService_LocateDrawerUsesAllocation verifies LocateDrawer lights the
// drawer's explicit segment-relative allocation, independent of its bins.
func TestService_LocateDrawerUsesAllocation(t *testing.T) {
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

	dbConn, err := db.Open("file:locate_drawer_alloc?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         "Drawer B",
		ControllerID: c.ID,
		SegmentID:    2,
		LedStart:     10,
		LedCount:     5,
		ConfigJson:   sql.NullString{String: `{"type":"linear","total":5}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.LocateDrawer(ctx, c.ID, cont); err != nil {
		t.Fatalf("LocateDrawer: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(body, `"i":[10,15`) {
		t.Errorf("expected allocation range 10->15, got %s", body)
	}
	if !strings.Contains(body, `"id":2`) {
		t.Errorf("expected segment id 2, got %s", body)
	}
}

// TestService_LocateDrawerUnallocated verifies a drawer without an allocation
// is not located.
func TestService_LocateDrawerUnallocated(t *testing.T) {
	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	dbConn, err := db.Open("file:locate_drawer_unalloc?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "No Alloc", ControllerID: c.ID, SegmentID: 0,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":5}`, Valid: true},
	})

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.LocateDrawer(ctx, c.ID, cont); err == nil {
		t.Fatal("expected error for unallocated drawer")
	}
	if hit {
		t.Error("no WLED request should be made for an unallocated drawer")
	}
}

// TestService_LocateFailsWhenCoordinateSpaceUnresolved verifies that bin-index
// LED operations fail safely (without sending a WLED command) when the
// coordinate space is unresolved, while global-off remains available.
func TestService_LocateFailsWhenCoordinateSpaceUnresolved(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	dbConn, err := db.Open("file:wled_unresolved?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "A", ControllerID: c.ID, SegmentID: 0, LedStart: 0, LedCount: 10,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	bin, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "a1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})

	if err := ledspace.Set(ctx, store, ledspace.Unresolved); err != nil {
		t.Fatalf("set unresolved: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := svc.LocateBin(ctx, c.ID, bin); !errors.Is(err, ErrCoordinateSpaceUnresolved) {
		t.Errorf("LocateBin: expected ErrCoordinateSpaceUnresolved, got %v", err)
	}
	if err := svc.LocatePart(ctx, 1); !errors.Is(err, ErrCoordinateSpaceUnresolved) {
		t.Errorf("LocatePart: expected ErrCoordinateSpaceUnresolved, got %v", err)
	}
	if err := svc.FlashError(ctx, c.ID, bin); !errors.Is(err, ErrCoordinateSpaceUnresolved) {
		t.Errorf("FlashError: expected ErrCoordinateSpaceUnresolved, got %v", err)
	}

	mu.Lock()
	if hits != 0 {
		t.Errorf("expected no WLED requests while unresolved, got %d", hits)
	}
	mu.Unlock()

	// Global-off must remain available.
	if err := svc.GlobalOff(ctx); err != nil {
		t.Errorf("GlobalOff must remain available: %v", err)
	}
}

// drawerTestServer returns a mock WLED server that records the last request body
// and a hit counter.
func drawerTestServer(t *testing.T) (*httptest.Server, func() (string, int)) {
	t.Helper()
	var mu sync.Mutex
	var body string
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return server, func() (string, int) {
		mu.Lock()
		defer mu.Unlock()
		return body, hits
	}
}

// TestService_LocateBinDrawerSpace verifies that in a drawer-relative database a
// bin's stored index is offset by its drawer's allocation start before being
// sent to WLED.
func TestService_LocateBinDrawerSpace(t *testing.T) {
	server, snapshot := drawerTestServer(t)
	defer server.Close()
	ip := server.URL[7:]

	dbConn, err := db.Open("file:locate_bin_drawer?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Drawer", ControllerID: c.ID, SegmentID: 2, LedStart: 10, LedCount: 5,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":5}`, Valid: true},
	})
	bin, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "b1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 3, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.LocateBin(ctx, c.ID, bin); err != nil {
		t.Fatalf("LocateBin: %v", err)
	}

	body, hits := snapshot()
	if hits != 1 {
		t.Fatalf("expected 1 WLED request, got %d", hits)
	}
	if !strings.Contains(body, `"i":[13,14`) {
		t.Errorf("expected drawer-relative index 3 to map to segment index 13, got %s", body)
	}
	if !strings.Contains(body, `"id":2`) {
		t.Errorf("expected segment id 2, got %s", body)
	}
}

// TestService_LocateBinDrawerOutOfRange verifies that a drawer-relative bin
// index outside its drawer's allocation is rejected without contacting WLED.
func TestService_LocateBinDrawerOutOfRange(t *testing.T) {
	server, snapshot := drawerTestServer(t)
	defer server.Close()
	ip := server.URL[7:]

	dbConn, err := db.Open("file:locate_bin_drawer_range?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Drawer", ControllerID: c.ID, SegmentID: 0, LedStart: 0, LedCount: 5,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":5}`, Valid: true},
	})
	bin, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "b1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 5, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.LocateBin(ctx, c.ID, bin); err == nil {
		t.Fatal("expected an error for a drawer-relative index outside the allocation")
	}
	if _, hits := snapshot(); hits != 0 {
		t.Errorf("expected no WLED request, got %d", hits)
	}
}

// TestService_FlashErrorDrawerSpace verifies that FlashError offsets a
// drawer-relative index by its drawer's allocation start.
func TestService_FlashErrorDrawerSpace(t *testing.T) {
	server, snapshot := drawerTestServer(t)
	defer server.Close()
	ip := server.URL[7:]

	dbConn, err := db.Open("file:flash_drawer?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Drawer", ControllerID: c.ID, SegmentID: 1, LedStart: 20, LedCount: 10,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	bin, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "b1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 4, Valid: true}, Width: sql.NullInt64{Int64: 2, Valid: true},
	})
	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.FlashError(ctx, c.ID, bin); err != nil {
		t.Fatalf("FlashError: %v", err)
	}

	// Flash mode runs its blink loop in the background, so wait for the first
	// request with a bounded timeout.
	deadline := time.Now().Add(2 * time.Second)
	var body string
	var hits int
	for time.Now().Before(deadline) {
		body, hits = snapshot()
		if hits >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hits < 1 {
		t.Fatalf("expected at least 1 WLED request, got %d", hits)
	}
	if !strings.Contains(body, `"i":[24,26`) {
		t.Errorf("expected drawer-relative index 4 width 2 to map to segment range 24->26, got %s", body)
	}
}

// TestService_LocatePartDrawerSpace verifies that LocatePart resolves every
// assignment in a drawer-relative database using a consistent snapshot and
// offsets each drawer-relative index by its drawer's allocation start.
func TestService_LocatePartDrawerSpace(t *testing.T) {
	server, snapshot := drawerTestServer(t)
	defer server.Close()
	ip := server.URL[7:]

	dbConn, err := db.Open("file:locate_part_drawer?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Drawer", ControllerID: c.ID, SegmentID: 0, LedStart: 10, LedCount: 5,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":5}`, Valid: true},
	})
	bin, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "b1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 2, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})
	partID, err := store.CreatePart(ctx, db.CreatePartParams{Name: "P"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: bin, Valid: true}, Quantity: 1,
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.LocatePart(ctx, partID); err != nil {
		t.Fatalf("LocatePart: %v", err)
	}

	body, hits := snapshot()
	if hits != 1 {
		t.Fatalf("expected 1 WLED request, got %d", hits)
	}
	if !strings.Contains(body, `"i":[12,13`) {
		t.Errorf("expected drawer-relative index 2 to map to segment index 12, got %s", body)
	}
}

// TestService_LocateFailsWhenCoordinateSpaceUnknown verifies that an
// unrecognised persisted coordinate space is rejected rather than silently
// treated as segment, and no WLED command is sent.
func TestService_LocateFailsWhenCoordinateSpaceUnknown(t *testing.T) {
	server, snapshot := drawerTestServer(t)
	defer server.Close()
	ip := server.URL[7:]

	dbConn, err := db.Open("file:locate_unknown_space?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	c, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	cont, _ := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "A", ControllerID: c.ID, SegmentID: 0, LedStart: 0, LedCount: 10,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	bin, _ := store.CreateBin(ctx, db.CreateBinParams{
		Name: "a1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err := store.SetFlag(ctx, db.SetFlagParams{Key: ledspace.FlagKey, Value: "bogus"}); err != nil {
		t.Fatalf("set flag: %v", err)
	}

	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.LocateBin(ctx, c.ID, bin); err == nil {
		t.Fatal("expected an error for an unknown coordinate space")
	}
	if err := svc.FlashError(ctx, c.ID, bin); err == nil {
		t.Fatal("expected FlashError to reject an unknown coordinate space")
	}
	if _, hits := snapshot(); hits != 0 {
		t.Errorf("expected no WLED request, got %d", hits)
	}
}
