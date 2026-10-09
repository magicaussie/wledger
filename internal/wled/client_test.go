package wled

import (
	"context"
	"database/sql"
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
