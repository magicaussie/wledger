package wled

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

type physicalMock struct {
	mu             sync.Mutex
	state          string
	config         string
	offline        bool
	postBodies     []map[string]any
	failPostNumber int
	postCount      int
}

func int64Pointer(value int64) *int64 { return &value }

func newPhysicalMock() *physicalMock {
	return &physicalMock{state: `{"on":true,"seg":[{"id":10,"start":0,"stop":100,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[20,30,40],[0,0,0],[0,0,0]]},{"id":20,"start":100,"stop":200,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[50,60,70],[0,0,0],[0,0,0]]}]}`, config: `{"hw":{"led":{"ins":[{"start":0,"len":100,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":100,"len":100,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`}
}

func (m *physicalMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/json/info":
		m.mu.Lock()
		offline := m.offline
		m.mu.Unlock()
		if offline {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":"offline"}`)
			return
		}
		fmt.Fprint(w, `{"leds":{"count":200}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/json/cfg":
		m.mu.Lock()
		config := m.config
		m.mu.Unlock()
		fmt.Fprint(w, config)
	case r.Method == http.MethodGet && r.URL.Path == "/json/state":
		m.mu.Lock()
		state := m.state
		m.mu.Unlock()
		fmt.Fprint(w, state)
	case r.Method == http.MethodPost && r.URL.Path == "/json/state":
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.postCount++
		m.postBodies = append(m.postBodies, body)
		fail := m.postCount == m.failPostNumber
		m.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"success":false}`)
			return
		}
		fmt.Fprint(w, `{"success":true}`)
	default:
		http.NotFound(w, r)
	}
}

func (m *physicalMock) posts() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]any(nil), m.postBodies...)
}

func (m *physicalMock) setState(state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
}

func (m *physicalMock) setConfig(config string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config
}

type physicalFixture struct {
	store      db.Store
	database   *sql.DB
	client     *Client
	service    *PhysicalLocateService
	mock       *physicalMock
	server     *httptest.Server
	partID     int64
	bin1       int64
	bin2       int64
	drawer1    int64
	drawer2    int64
	controller int64
}

type failingFinalSessionStore struct {
	db.Store
	failFinal bool
}

func (s *failingFinalSessionStore) SavePhysicalLocateSession(ctx context.Context, arg db.SavePhysicalLocateSessionParams) (int64, error) {
	if s.failFinal && arg.State == "stopped" {
		return 0, errors.New("simulated final session save failure")
	}
	return s.Store.SavePhysicalLocateSession(ctx, arg)
}

func newPhysicalFixture(t *testing.T) *physicalFixture {
	t.Helper()
	mock := newPhysicalMock()
	server := httptest.NewServer(mock)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int64
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open("file:" + t.TempDir() + "/physical-locate.db")
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		database.Close()
		server.Close()
		t.Fatal(err)
	}
	store := db.NewStore(database)
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateColors(ctx, db.UpdateColorsParams{ColorLocate: sql.NullString{String: "#FF0000", Valid: true}}); err != nil {
		t.Fatal(err)
	}
	controllerID, err := store.CreateController(ctx, db.CreateControllerParams{Name: "mock", IpAddress: host, Port: sql.NullInt64{Int64: port, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{LedCount: 200, ID: controllerID.ID, LedCount_2: 0}); err != nil {
		t.Fatal(err)
	}

	cabinetID, err := store.CreateCabinet(ctx, db.CreateCabinetParams{Name: "Mock cabinet", ControllerID: sql.NullInt64{Int64: controllerID.ID, Valid: true}, CoordinateSystem: "grid"})
	if err != nil {
		t.Fatal(err)
	}
	drawer1, err := store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer 1", Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	drawer2, err := store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer 2", DisplayOrder: 1, X: 12, Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	bin1, err := store.CreateBin(ctx, db.CreateBinParams{Name: "Bin 1", Width: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	bin2, err := store.CreateBin(ctx, db.CreateBinParams{Name: "Bin 2", Width: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: bin1, DrawerID: drawer1, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: bin2, DrawerID: drawer2, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO parts(name) VALUES ('mock part')`); err != nil {
		t.Fatal(err)
	}
	var part int64
	if err := database.QueryRowContext(ctx, `SELECT id FROM parts WHERE name='mock part'`).Scan(&part); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part, BinID: sql.NullInt64{Int64: bin1, Valid: true}, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part, BinID: sql.NullInt64{Int64: bin2, Valid: true}, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	for _, allocation := range []db.CreateDrawerLEDAllocationParams{
		{CabinetID: cabinetID, DrawerID: drawer1, ControllerID: controllerID.ID, StartPixel: 97, PixelCount: 8, DisplayOrder: 0},
		{CabinetID: cabinetID, DrawerID: drawer1, ControllerID: controllerID.ID, StartPixel: 150, PixelCount: 2, DisplayOrder: 1},
		{CabinetID: cabinetID, DrawerID: drawer2, ControllerID: controllerID.ID, StartPixel: 10, PixelCount: 2, DisplayOrder: 0},
	} {
		if _, err := store.CreateDrawerLEDAllocation(ctx, allocation); err != nil {
			t.Fatal(err)
		}
	}
	client := NewClient()
	fixture := &physicalFixture{store: store, database: database, client: client, mock: mock, server: server, partID: part, bin1: bin1, bin2: bin2, drawer1: drawer1, drawer2: drawer2, controller: controllerID.ID}
	fixture.service = NewPhysicalLocateService(store, client)
	if err := fixture.service.SaveControllerMappingVerification(ctx, controllerID.ID, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if session := fixture.service.getSession(controllerID.ID); session != nil {
			if session.timer != nil {
				session.timer.Stop()
			}
			setPhysicalReservation(session.ID, controllerID.ID, host, port, false)
		}
		server.Close()
		database.Close()
	})
	return fixture
}

func TestPhysicalLocateBlocksLegacyWritersUntilConfirmedStop(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	containerID, err := f.store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "legacy segment", ControllerID: f.controller, SegmentID: 10, LedStart: 0, LedCount: 200,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":200}`, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyBin, err := f.store.CreateBin(ctx, db.CreateBinParams{
		Name: "legacy bin", ContainerID: sql.NullInt64{Int64: containerID, Valid: true},
		LedIndex: sql.NullInt64{Int64: 5, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: f.partID, BinID: sql.NullInt64{Int64: legacyBin, Valid: true}, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	if err := ledspace.Set(ctx, f.store, ledspace.Drawer); err != nil {
		t.Fatal(err)
	}

	port := int64(f.server.Listener.Addr().(*net.TCPAddr).Port)
	aliasController, err := f.store.CreateController(ctx, db.CreateControllerParams{
		Name: "loopback alias", IpAddress: "localhost", Port: sql.NullInt64{Int64: port, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := NewService(f.store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := f.service.LocateInventoryBin(ctx, f.partID, f.bin1, 7); err != nil {
		t.Fatal(err)
	}
	before := len(f.mock.posts())
	for name, action := range map[string]func() error{
		"legacy bin locate":    func() error { return legacy.LocateBin(ctx, f.controller, legacyBin) },
		"legacy drawer locate": func() error { return legacy.LocateDrawer(ctx, f.controller, containerID) },
		"part locate":          func() error { return legacy.LocatePart(ctx, f.partID) },
		"FlashError":           func() error { return legacy.FlashError(ctx, f.controller, legacyBin) },
		"GlobalOff":            func() error { return legacy.GlobalOff(ctx) },
		"raw state update from another Client": func() error {
			return NewClient().SetState(ctx, f.server.Listener.Addr().String(), map[string]any{"on": false})
		},
		"controller record with changed address and port": func() error {
			controller, err := f.store.GetController(ctx, f.controller)
			if err != nil {
				return err
			}
			controller.IpAddress = "127.0.0.1"
			controller.Port.Int64++
			controller.Port.Valid = true
			return NewClient().ApplyController(ctx, controller, 10, 0, 1, State{Color: "#00FF00", Mode: ModeSolid})
		},
	} {
		if err := action(); !errors.Is(err, ErrHardwareBusy) {
			t.Errorf("%s returned %v, want ErrHardwareBusy", name, err)
		}
	}
	if got := len(f.mock.posts()); got != before {
		t.Fatalf("legacy writers sent %d WLED commands while physical Locate was active", got-before)
	}

	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteController(ctx, aliasController.ID); err != nil {
		t.Fatal(err)
	}
	if err := legacy.LocateBin(ctx, f.controller, legacyBin); err != nil {
		t.Fatalf("legacy Locate should resume after acknowledged cleanup: %v", err)
	}
	if err := legacy.GlobalOff(ctx); err != nil {
		t.Fatalf("GlobalOff should succeed after cleanup: %v", err)
	}
	posts := f.mock.posts()
	if len(posts) != before+5 {
		t.Fatalf("got %d writes after cleanup, want three physical range-off commands, legacy Locate, and GlobalOff", len(posts)-before)
	}
	globalOff := posts[len(posts)-1]
	if globalOff["on"] != false {
		t.Fatalf("GlobalOff did not issue its explicit device-wide power-off: %#v", globalOff)
	}
}

func TestMapPhysicalRangesControllerBoundariesAndDisjointAllocations(t *testing.T) {
	mock := newPhysicalMock()
	var info physicalInfo
	info.LEDs.Count = 200
	var cfg physicalConfig
	cfg.Hardware.LED.Inputs = []physicalOutput{
		{Start: int64Pointer(0), Length: 100, Pins: json.RawMessage(`[2,-1,-1]`), Type: 22},
		{Start: int64Pointer(100), Length: 100, Pins: json.RawMessage(`[4,-1,-1]`), Type: 22},
	}
	var state physicalState
	if err := json.Unmarshal([]byte(mock.state), &state); err != nil {
		t.Fatal(err)
	}
	commands, _, err := mapPhysicalRanges([]ControllerRange{{Start: 97, Count: 8}, {Start: 150, Count: 2}}, db.Controller{LedCount: 200}, 200, info, cfg, state, "#FF0000", locateBrightness)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 {
		t.Fatalf("got %d commands, want 3 segment/range intersections: %+v", len(commands), commands)
	}
	want := []physicalPixelCommand{{SegmentID: 10, Start: 96, Stop: 100}, {SegmentID: 20, Start: 0, Stop: 4}, {SegmentID: 20, Start: 49, Stop: 51}}
	for i := range want {
		if commands[i].SegmentID != want[i].SegmentID || commands[i].Start != want[i].Start || commands[i].Stop != want[i].Stop {
			t.Errorf("command %d = %+v, want %+v", i, commands[i], want[i])
		}
	}
}

func TestPhysicalLocateSendsExactSegmentRelativeCommandsAndTurnsOffOnlyOwnedRanges(t *testing.T) {
	f := newPhysicalFixture(t)
	session, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if session.ControllerID != f.controller || session.DrawerID != f.drawer1 || session.BinID != f.bin1 || session.State != "active" || session.Brightness != locateBrightness {
		t.Fatalf("unexpected session: %+v", session)
	}
	posts := f.mock.posts()
	if len(posts) != 3 {
		t.Fatalf("locate posted %d commands, want 3: %#v", len(posts), posts)
	}
	assertPixelCommand(t, posts[0], 10, 96, 100, []any{float64(128), float64(0), float64(0)})
	assertPixelCommand(t, posts[1], 20, 0, 4, []any{float64(128), float64(0), float64(0)})
	assertPixelCommand(t, posts[2], 20, 49, 51, []any{float64(128), float64(0), float64(0)})
	for i, body := range posts {
		if _, exists := body["on"]; exists {
			t.Errorf("command %d changed controller power", i)
		}
	}
	if err := f.service.StopDrawer(context.Background(), f.drawer1, 8); !errors.Is(err, ErrLocateSessionOwned) {
		t.Fatalf("other user Stop = %v, want owner check failure", err)
	}
	if got := len(f.mock.posts()); got != 3 {
		t.Fatalf("unauthorized Stop sent %d commands, want none", got-3)
	}
	if err := f.service.StopDrawer(context.Background(), f.drawer1, 7); err != nil {
		t.Fatal(err)
	}
	if err := f.service.StopDrawer(context.Background(), f.drawer1, 7); err != nil {
		t.Fatalf("repeated Stop should be idempotent: %v", err)
	}
	posts = f.mock.posts()
	if len(posts) != 6 {
		t.Fatalf("after Stop got %d commands, want 6 total", len(posts))
	}
	assertPixelCommand(t, posts[3], 10, 96, 100, []any{float64(0), float64(0), float64(0)})
	assertPixelCommand(t, posts[4], 20, 0, 4, []any{float64(0), float64(0), float64(0)})
	assertPixelCommand(t, posts[5], 20, 49, 51, []any{float64(0), float64(0), float64(0)})
	session, ok := f.service.SessionForDrawer(f.drawer1)
	if !ok || session.State != "stopped" || session.LastOperation != "WLED acknowledged range-off commands for this drawer" {
		t.Fatalf("stop was not recorded as confirmed: %+v, %v", session, ok)
	}
}

func TestPhysicalLocateSwitchesDrawerAndValidatesSelectedInventoryLocation(t *testing.T) {
	f := newPhysicalFixture(t)
	first, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin1, 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7)
	if err != nil {
		t.Fatal(err)
	}
	if first.DrawerID == second.DrawerID || second.BinID != f.bin2 || second.State != "active" {
		t.Fatalf("did not switch selected physical location: first=%+v second=%+v", first, second)
	}
	posts := f.mock.posts()
	if len(posts) != 7 { // 3 first activation + 3 range-off commands + 1 second activation
		t.Fatalf("drawer switch issued %d commands, want 7", len(posts))
	}
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, 99999, 7); err == nil {
		t.Fatal("missing inventory assignment should not locate")
	}
}

func TestPhysicalLocateFailsBeforeWritesForMissingBinOrUnverifiedMapping(t *testing.T) {
	f := newPhysicalFixture(t)
	unattached, err := f.store.CreateBin(context.Background(), db.CreateBinParams{Name: "Unattached"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreatePartAssignment(context.Background(), db.CreatePartAssignmentParams{PartID: f.partID, BinID: sql.NullInt64{Int64: unattached, Valid: true}, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, unattached, 7); !errors.Is(err, ErrPhysicalBinMissing) {
		t.Fatalf("got %v, want missing physical bin association", err)
	}
	if _, err := f.store.UpdateControllerLocateMappingVerification(context.Background(), db.UpdateControllerLocateMappingVerificationParams{ID: f.controller, LocateMappingVerified: false, LocateMappingFingerprint: ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin1, 7); err != ErrLocateMappingUnverified {
		t.Fatalf("got error %v, want ErrLocateMappingUnverified", err)
	}
	if posts := f.mock.posts(); len(posts) != 0 {
		t.Fatalf("unsafe configuration sent hardware commands: %#v", posts)
	}
}

func TestPhysicalLocateRejectsStaleTopologyAndPartialCommandFailure(t *testing.T) {
	t.Run("stale segment topology", func(t *testing.T) {
		f := newPhysicalFixture(t)
		if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err != nil {
			t.Fatal(err)
		}
		f.mock.setState(`{"on":true,"seg":[{"id":10,"start":0,"stop":90,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[20,30,40]]},{"id":20,"start":90,"stop":200,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[50,60,70]]}]}`)
		before := len(f.mock.posts())
		if err := f.service.StopDrawer(context.Background(), f.drawer2, 7); err != ErrLocateSessionStale {
			t.Fatalf("got %v, want stale-config error", err)
		}
		if got := len(f.mock.posts()); got != before {
			t.Fatalf("stale config sent cleanup write: before=%d after=%d", before, got)
		}
		session, _ := f.service.SessionForDrawer(f.drawer2)
		if session.State != "partial" {
			t.Fatalf("stale cleanup claimed safe stop: %+v", session)
		}
	})
	t.Run("partial command", func(t *testing.T) {
		f := newPhysicalFixture(t)
		f.mock.failPostNumber = 2
		if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin1, 7); err == nil {
			t.Fatal("second segment command failure should be returned")
		}
		session, ok := f.service.SessionForDrawer(f.drawer1)
		if !ok || session.State != "failed" || session.LastError == "" {
			t.Fatalf("partial failure state not recorded: %+v", session)
		}
		posts := f.mock.posts()
		if len(posts) != 5 { // 2 attempted activation writes and 3 range-off cleanup commands
			t.Fatalf("got %d commands after cleanup, want 5", len(posts))
		}
	})
}

func TestPhysicalLocateRecoversSessionAfterServiceRestart(t *testing.T) {
	f := newPhysicalFixture(t)
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err != nil {
		t.Fatal(err)
	}
	restarted := NewPhysicalLocateService(f.store, NewClient())
	if session, ok := restarted.SessionForDrawer(f.drawer2); !ok || session.State != "active" {
		t.Fatalf("persisted Locate session was not recovered: %+v, %v", session, ok)
	}
	if err := restarted.StopDrawer(context.Background(), f.drawer2, 7); err != nil {
		t.Fatal(err)
	}
	session, _ := restarted.SessionForDrawer(f.drawer2)
	if session.State != "stopped" {
		t.Fatalf("restarted service did not confirm cleanup: %+v", session)
	}
	before := len(f.mock.posts())
	if err := f.service.StopDrawer(context.Background(), f.drawer2, 7); err != nil {
		t.Fatalf("stale service Stop after durable completion: %v", err)
	}
	if got := len(f.mock.posts()); got != before {
		t.Fatalf("stale terminal callback sent %d writes", got-before)
	}
	if old := f.service.getSession(f.controller); old != nil && old.timer != nil {
		old.timer.Stop()
	}
}

func TestPhysicalLocateFinalizationFailureRemainsVisibleAndRetries(t *testing.T) {
	f := newPhysicalFixture(t)
	store := &failingFinalSessionStore{Store: f.store, failFinal: true}
	f.service = NewPhysicalLocateService(store, NewClient())
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err != nil {
		t.Fatal(err)
	}
	if err := f.service.StopDrawer(context.Background(), f.drawer2, 7); err == nil {
		t.Fatal("durable finalization failure should be reported")
	}
	status, ok, err := f.service.SessionStatusForDrawer(context.Background(), f.drawer2)
	if err != nil || !ok || status.State != "partial" || !strings.Contains(status.LastError, "durable session finalization failed") {
		t.Fatalf("finalization failure was hidden: status=%+v found=%v err=%v", status, ok, err)
	}
	store.failFinal = false
	if err := f.service.StopDrawer(context.Background(), f.drawer2, 7); err != nil {
		t.Fatalf("retry after finalization failure: %v", err)
	}
	status, ok, err = f.service.SessionStatusForDrawer(context.Background(), f.drawer2)
	if err != nil || !ok || status.State != "stopped" {
		t.Fatalf("cleanup did not settle after retry: status=%+v found=%v err=%v", status, ok, err)
	}
	if current := f.service.getSession(f.controller); current != nil && current.timer != nil {
		current.timer.Stop()
	}
}

func TestPhysicalLocateTimeoutTurnsOffOwnedRanges(t *testing.T) {
	previousTimeout := locateSessionTimeout
	locateSessionTimeout = 25 * time.Millisecond
	defer func() { locateSessionTimeout = previousTimeout }()
	f := newPhysicalFixture(t)
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if session, ok := f.service.SessionForDrawer(f.drawer2); ok && session.State == "stopped" {
			if got := len(f.mock.posts()); got != 2 {
				t.Fatalf("timeout issued %d writes, want activation+restore", got)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	session, _ := f.service.SessionForDrawer(f.drawer2)
	t.Fatalf("timeout did not send range-off commands; session=%+v", session)
}

func TestPhysicalLocateMappingVerificationFailsClosedWithoutOutputStartsAndPins(t *testing.T) {
	f := newPhysicalFixture(t)
	f.mock.setConfig(`{"hw":{"led":{"ins":[{"len":100,"rev":false,"skip":0},{"len":100,"rev":false,"skip":0}]}}}`)
	if _, err := f.service.VerifyControllerMapping(context.Background(), f.controller); !errors.Is(err, ErrLocateTopology) {
		t.Fatalf("got %v, want missing output-geometry error", err)
	}
	if got := len(f.mock.posts()); got != 0 {
		t.Fatalf("read-only mapping verification sent %d pixel writes", got)
	}
}

func TestPhysicalLocateRequiresUnchangedAttestedOutputMapping(t *testing.T) {
	f := newPhysicalFixture(t)
	f.mock.setConfig(`{"hw":{"led":{"ins":[{"start":0,"len":100,"rev":false,"skip":0,"type":22,"pin":[6,-1,-1]},{"start":100,"len":100,"rev":false,"skip":0,"type":22,"pin":[8,-1,-1]}]}}}`)
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); !errors.Is(err, ErrLocateMappingUnverified) {
		t.Fatalf("got %v, want changed-attestation error", err)
	}
	if got := len(f.mock.posts()); got != 0 {
		t.Fatalf("changed mapping received %d state writes", got)
	}
}

func TestPhysicalLocateRejectsIncompletePersistedCleanupCommands(t *testing.T) {
	f := newPhysicalFixture(t)
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ExecContext(context.Background(), `UPDATE physical_locate_sessions SET commands_json='[]' WHERE controller_id=?`, f.controller); err != nil {
		t.Fatal(err)
	}
	restarted := NewPhysicalLocateService(f.store, NewClient())
	before := len(f.mock.posts())
	if err := restarted.StopDrawer(context.Background(), f.drawer2, 7); !errors.Is(err, ErrLocateSessionStale) {
		t.Fatalf("got %v, want corrupt-recovery refusal", err)
	}
	if got := len(f.mock.posts()); got != before {
		t.Fatalf("incomplete recovery commands sent writes: before=%d after=%d", before, got)
	}
	if old := f.service.getSession(f.controller); old != nil && old.timer != nil {
		old.timer.Stop()
	}
	if recovered := restarted.getSession(f.controller); recovered != nil && recovered.timer != nil {
		recovered.timer.Stop()
	}
}

func TestPhysicalLocateRejectsChangedOutputConfigurationOnStop(t *testing.T) {
	f := newPhysicalFixture(t)
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err != nil {
		t.Fatal(err)
	}
	f.mock.setConfig(`{"hw":{"led":{"ins":[{"start":0,"len":80,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":80,"len":120,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`)
	before := len(f.mock.posts())
	if err := f.service.StopDrawer(context.Background(), f.drawer2, 7); !errors.Is(err, ErrLocateSessionStale) {
		t.Fatalf("got %v, want changed-output stale-config error", err)
	}
	if got := len(f.mock.posts()); got != before {
		t.Fatalf("changed output config received cleanup writes: before=%d after=%d", before, got)
	}
}

func TestPhysicalLocateControllerOfflineAndUnsafeSegmentSendNoPixels(t *testing.T) {
	t.Run("offline", func(t *testing.T) {
		f := newPhysicalFixture(t)
		f.mock.mu.Lock()
		f.mock.offline = true
		f.mock.mu.Unlock()
		if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err == nil {
			t.Fatal("offline controller should fail")
		}
		if got := len(f.mock.posts()); got != 0 {
			t.Fatalf("offline controller received %d state writes", got)
		}
	})
	t.Run("active effect", func(t *testing.T) {
		f := newPhysicalFixture(t)
		f.mock.setState(`{"on":true,"seg":[{"id":10,"start":0,"stop":100,"on":true,"fx":12,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[20,30,40]]},{"id":20,"start":100,"stop":200,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[50,60,70]]}]}`)
		if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); !errors.Is(err, ErrLocateTopology) {
			t.Fatalf("got %v, want unsafe-segment error", err)
		}
		if got := len(f.mock.posts()); got != 0 {
			t.Fatalf("unsafe segment received %d state writes", got)
		}
	})
	t.Run("segment offset", func(t *testing.T) {
		f := newPhysicalFixture(t)
		f.mock.setState(`{"on":true,"seg":[{"id":10,"start":0,"stop":100,"of":1,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[20,30,40]]},{"id":20,"start":100,"stop":200,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[50,60,70]]}]}`)
		if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin1, 7); !errors.Is(err, ErrLocateTopology) {
			t.Fatalf("got %v, want nonzero segment-offset error", err)
		}
		if got := len(f.mock.posts()); got != 0 {
			t.Fatalf("offset segment received %d state writes", got)
		}
	})
	t.Run("custom output map", func(t *testing.T) {
		f := newPhysicalFixture(t)
		f.mock.setConfig(`{"hw":{"led":{"ins":[{"start":0,"len":100,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":100,"len":100,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}],"ledmap":[0,1,2]}}}`)
		if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); !errors.Is(err, ErrLocateTopology) {
			t.Fatalf("got %v, want custom-map safety error", err)
		}
		if got := len(f.mock.posts()); got != 0 {
			t.Fatalf("custom mapping received %d state writes", got)
		}
	})
}

func TestStaleServiceCannotStopReplacementSession(t *testing.T) {
	f := newPhysicalFixture(t)
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin1, 7); err != nil {
		t.Fatal(err)
	}
	staleService := NewPhysicalLocateService(f.store, NewClient())
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err != nil {
		t.Fatal(err)
	}
	before := len(f.mock.posts())
	if err := staleService.StopDrawer(context.Background(), f.drawer1, 7); err != nil {
		t.Fatalf("stale Stop should be idempotent after session refresh: %v", err)
	}
	if got := len(f.mock.posts()); got != before {
		t.Fatalf("stale service sent %d writes, want no additional command", got-before)
	}
	if active, ok := f.service.SessionForDrawer(f.drawer2); !ok || active.State != "active" {
		t.Fatalf("replacement Locate session was disturbed: %+v, %v", active, ok)
	}
	if err := f.service.StopDrawer(context.Background(), f.drawer2, 7); err != nil {
		t.Fatal(err)
	}
	if stale := staleService.getSession(f.controller); stale != nil && stale.timer != nil {
		stale.timer.Stop()
	}
}

func TestPhysicalLocateConcurrentRequestsAreSerialized(t *testing.T) {
	f := newPhysicalFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, binID := range []int64{f.bin1, f.bin2} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			_, err := f.service.LocateInventoryBin(context.Background(), f.partID, id, 7)
			errs <- err
		}(binID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Locate failed: %v", err)
		}
	}
	active := 0
	for _, drawerID := range []int64{f.drawer1, f.drawer2} {
		if session, ok := f.service.SessionForDrawer(drawerID); ok && session.State == "active" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("got %d active drawers, want exactly one", active)
	}
}

func assertPixelCommand(t *testing.T, body map[string]any, segmentID int, start, stop int, color []any) {
	t.Helper()
	segments, ok := body["seg"].([]any)
	if !ok || len(segments) != 1 {
		t.Fatalf("seg payload = %#v", body["seg"])
	}
	segment, ok := segments[0].(map[string]any)
	if !ok {
		t.Fatalf("segment payload = %#v", segments[0])
	}
	if int(segment["id"].(float64)) != segmentID {
		t.Errorf("segment id = %v, want %d", segment["id"], segmentID)
	}
	if len(segment) != 2 {
		t.Errorf("command modified segment properties beyond id/i: %#v", segment)
	}
	indices, ok := segment["i"].([]any)
	if !ok || len(indices) != 3 {
		t.Fatalf("individual pixel range = %#v", segment["i"])
	}
	if int(indices[0].(float64)) != start || int(indices[1].(float64)) != stop {
		t.Errorf("segment-relative range = %v..%v, want %d..%d", indices[0], indices[1], start, stop)
	}
	gotColor, ok := indices[2].([]any)
	if !ok || len(gotColor) != len(color) {
		t.Fatalf("color = %#v", indices[2])
	}
	for i := range color {
		if gotColor[i].(float64) != color[i].(float64) {
			t.Errorf("color = %v, want %v", gotColor, color)
		}
	}
}
