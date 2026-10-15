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
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

type physicalMock struct {
	mu                        sync.Mutex
	count                     int
	info                      string
	state                     string
	config                    string
	offline                   bool
	stallMethod               string
	stallPath                 string
	stallFor                  time.Duration
	malformedGETPath          string
	postBodies                []map[string]any
	postRawBodies             []string
	postContentTypes          []string
	postApplied               []bool
	failPostNumber            int
	failPostStatus            int
	failBeforeApplyPostNumber int
	dropResponsePostNumber    int
	postCount                 int
	requestLog                []string
	postEntered               chan struct{}
	postRelease               chan struct{}
	postAccepted              chan struct{}
	postResponseRelease       chan struct{}
	applyPostEntered          chan struct{}
	applyPostRelease          chan struct{}
	delayApplyPostNumber      int
	framebuffer               [][3]int
}

func int64Pointer(value int64) *int64 { return &value }

func newPhysicalMock() *physicalMock {
	mock := &physicalMock{state: `{"on":true,"seg":[{"id":10,"start":0,"stop":100,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[20,30,40],[0,0,0],[0,0,0]]},{"id":20,"start":100,"stop":200,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"col":[[50,60,70],[0,0,0],[0,0,0]]}]}`, config: `{"hw":{"led":{"ins":[{"start":0,"len":100,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":100,"len":100,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`}
	mock.setFramebufferCapacity(200)
	return mock
}

func (m *physicalMock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	m.mu.Lock()
	m.requestLog = append(m.requestLog, r.Method+" "+r.URL.Path)
	stall := m.stallMethod == r.Method && m.stallPath == r.URL.Path
	stallFor := m.stallFor
	m.mu.Unlock()
	if stall {
		timer := time.NewTimer(stallFor)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
	}
	m.mu.Lock()
	malformedGET := r.Method == http.MethodGet && m.malformedGETPath == r.URL.Path
	m.mu.Unlock()
	if malformedGET {
		fmt.Fprint(w, "not-json")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/json/info":
		m.mu.Lock()
		offline := m.offline
		count := m.count
		info := m.info
		m.mu.Unlock()
		if offline {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"error":"offline"}`)
			return
		}
		if info != "" {
			fmt.Fprint(w, info)
			return
		}
		if count == 0 {
			count = 200
		}
		fmt.Fprintf(w, `{"leds":{"count":%d}}`, count)
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
		m.mu.Lock()
		entered, release := m.postEntered, m.postRelease
		m.postEntered, m.postRelease = nil, nil
		m.mu.Unlock()
		if entered != nil {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.postCount++
		m.postBodies = append(m.postBodies, body)
		m.postRawBodies = append(m.postRawBodies, string(data))
		m.postContentTypes = append(m.postContentTypes, r.Header.Get("Content-Type"))
		fail := m.postCount == m.failPostNumber
		failBeforeApply := m.postCount == m.failBeforeApplyPostNumber
		delayApply := m.postCount == m.delayApplyPostNumber
		dropResponse := m.postCount == m.dropResponsePostNumber
		accepted, responseRelease := m.postAccepted, m.postResponseRelease
		applyEntered, applyRelease := m.applyPostEntered, m.applyPostRelease
		m.postAccepted, m.postResponseRelease = nil, nil
		if delayApply {
			m.applyPostEntered, m.applyPostRelease = nil, nil
			m.mu.Unlock()
			if applyEntered != nil {
				close(applyEntered)
			}
			if applyRelease != nil {
				<-applyRelease
			}
			m.mu.Lock()
		}
		if !failBeforeApply {
			if err := applyMockPixelPayload(m.framebuffer, m.state, m.config, body); err != nil {
				m.mu.Unlock()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
		}
		m.postApplied = append(m.postApplied, !failBeforeApply)
		m.mu.Unlock()
		if accepted != nil {
			close(accepted)
			select {
			case <-responseRelease:
			case <-r.Context().Done():
				return
			}
		}
		if dropResponse {
			if connection, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = connection.Close()
			}
			return
		}
		if fail || failBeforeApply {
			status := http.StatusServiceUnavailable
			m.mu.Lock()
			if fail && m.failPostStatus != 0 {
				status = m.failPostStatus
			}
			m.mu.Unlock()
			w.WriteHeader(status)
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

func (m *physicalMock) setInfo(info string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.info = info
}

func (m *physicalMock) rawPosts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.postRawBodies...)
}

func (m *physicalMock) postTypes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.postContentTypes...)
}

func (m *physicalMock) setState(state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
}

func (m *physicalMock) pixels() [][3]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][3]int(nil), m.framebuffer...)
}

func (m *physicalMock) setFramebufferCapacity(capacity int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.framebuffer = make([][3]int, capacity)
	for pixel := range m.framebuffer {
		m.framebuffer[pixel] = [3]int{20 + pixel%50, 30 + pixel%60, 40 + pixel%70}
	}
}

func (m *physicalMock) appliedPosts() []bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]bool(nil), m.postApplied...)
}

func applyMockPixelPayload(framebuffer [][3]int, controllerState, controllerConfig string, payload map[string]any) error {
	segments, ok := payload["seg"].([]any)
	if !ok {
		if on, isBool := payload["on"].(bool); isBool && !on {
			for pixel := range framebuffer {
				framebuffer[pixel] = [3]int{0, 0, 0}
			}
			return nil
		}
		return errors.New("targeted pixel payload has no segment list")
	}
	var state struct {
		Segments []struct {
			ID      int64 `json:"id"`
			Start   int   `json:"start"`
			Stop    int   `json:"stop"`
			Reverse bool  `json:"rev"`
		} `json:"seg"`
	}
	if err := json.Unmarshal([]byte(controllerState), &state); err != nil {
		return fmt.Errorf("decode current mock segment geometry: %w", err)
	}
	var config physicalConfig
	if err := json.Unmarshal([]byte(controllerConfig), &config); err != nil {
		return fmt.Errorf("decode current mock output geometry: %w", err)
	}
	for _, rawSegment := range segments {
		segment, ok := rawSegment.(map[string]any)
		if !ok {
			return errors.New("targeted pixel payload has an invalid segment")
		}
		segmentID, ok := segment["id"].(float64)
		if !ok {
			return errors.New("targeted pixel segment has no numeric id")
		}
		var geometry struct {
			ID      int64
			Start   int
			Stop    int
			Reverse bool
		}
		found := false
		for _, candidate := range state.Segments {
			if candidate.ID == int64(segmentID) {
				geometry.ID, geometry.Start, geometry.Stop, geometry.Reverse = candidate.ID, candidate.Start, candidate.Stop, candidate.Reverse
				found = true
				break
			}
		}
		offset, capacity := geometry.Start, geometry.Stop-geometry.Start
		if !found || offset < 0 || capacity < 0 {
			return fmt.Errorf("unsupported mock segment id or geometry %v", segmentID)
		}
		values, ok := segment["i"].([]any)
		if !ok || len(values) != 3 {
			return errors.New("targeted pixel segment has no [start,stop,color] value")
		}
		start, startOK := values[0].(float64)
		stop, stopOK := values[1].(float64)
		color, colorOK := values[2].([]any)
		if !startOK || !stopOK || !colorOK || len(color) != 3 {
			return errors.New("targeted pixel range has invalid coordinates or RGB color")
		}
		if start < 0 || stop < start || stop > float64(capacity) {
			return fmt.Errorf("targeted mock range %v-%v is outside segment capacity %d", start, stop, capacity)
		}
		var rgb [3]int
		for channel := range rgb {
			value, ok := color[channel].(float64)
			if !ok || value < 0 || value > 255 {
				return errors.New("targeted mock RGB component is invalid")
			}
			rgb[channel] = int(value)
		}
		for segmentPixel := int(start); segmentPixel < int(stop); segmentPixel++ {
			controllerPixel := offset + segmentPixel
			if geometry.Reverse {
				controllerPixel = geometry.Stop - 1 - segmentPixel
			}
			physicalPixel := controllerPixel
			for _, output := range config.Hardware.LED.Inputs {
				if output.Start == nil || controllerPixel < int(*output.Start) || int64(controllerPixel) >= *output.Start+output.Length {
					continue
				}
				if output.Reverse {
					physicalPixel = int(*output.Start + output.Length - 1 - int64(controllerPixel) + *output.Start)
				}
				break
			}
			if physicalPixel < 0 || physicalPixel >= len(framebuffer) {
				return fmt.Errorf("targeted mock pixel %d is outside controller capacity %d", physicalPixel, len(framebuffer))
			}
			framebuffer[physicalPixel] = rgb
		}
	}
	return nil
}

func (m *physicalMock) setConfig(config string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.config = config
}

func (m *physicalMock) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requestLog)
}

func (m *physicalMock) requestsSince(index int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= len(m.requestLog) {
		return nil
	}
	return append([]string(nil), m.requestLog[index:]...)
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

type failingFinalSessionQuerier struct {
	db.Querier
	failFinal bool
}

func (s *failingFinalSessionStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	return s.Store.ExecImmediateTx(ctx, func(q db.Querier) error {
		return fn(failingFinalSessionQuerier{Querier: q, failFinal: s.failFinal})
	})
}

func (q failingFinalSessionQuerier) SavePhysicalLocateSession(ctx context.Context, arg db.SavePhysicalLocateSessionParams) (int64, error) {
	if q.failFinal && arg.State == "stopped" {
		return 0, errors.New("simulated final session save failure")
	}
	return q.Querier.SavePhysicalLocateSession(ctx, arg)
}

func (q failingFinalSessionQuerier) UpdatePhysicalLocateSessionCAS(ctx context.Context, arg db.UpdatePhysicalLocateSessionCASParams) (int64, error) {
	if q.failFinal && arg.State == "stopped" {
		return 0, errors.New("simulated final session save failure")
	}
	return q.Querier.UpdatePhysicalLocateSessionCAS(ctx, arg)
}

func currentPhysicalReservationOwnerIDs() []string {
	hardwareWriteLease.mu.Lock()
	defer hardwareWriteLease.mu.Unlock()
	owners := make([]string, 0, len(hardwareWriteLease.physicalOwners))
	for owner := range hardwareWriteLease.physicalOwners {
		owners = append(owners, owner)
	}
	return owners
}

func clearFixturePhysicalReservations(t *testing.T, fixture *physicalFixture) {
	t.Helper()
	sessions, err := fixture.store.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Errorf("list durable sessions during fixture cleanup: %v", err)
		return
	}
	for _, session := range sessions {
		targets, err := fixture.store.ListPhysicalLocateSessionTargets(context.Background(), session.SessionID)
		if err != nil {
			t.Errorf("list targets for session %s during fixture cleanup: %v", session.SessionID, err)
			continue
		}
		if len(targets) == 0 {
			setPhysicalReservation(session.SessionID, session.ControllerID, session.ControllerAddress, session.ControllerPort, false)
			continue
		}
		for _, target := range targets {
			setPhysicalReservation(session.SessionID, target.ControllerID, target.ControllerAddress, target.ControllerPort, false)
		}
	}
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
	if err := store.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinetID, ControllerID: controllerID.ID, Slot: 0}); err != nil {
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
		{CabinetID: cabinetID, DrawerID: drawer1, StartPixel: 97, PixelCount: 8, DisplayOrder: 0},
		{CabinetID: cabinetID, DrawerID: drawer1, StartPixel: 150, PixelCount: 2, DisplayOrder: 1},
		{CabinetID: cabinetID, DrawerID: drawer2, StartPixel: 10, PixelCount: 2, DisplayOrder: 0},
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
			setSessionReservations(session, false)
		}
		clearFixturePhysicalReservations(t, fixture)
		if owners := currentPhysicalReservationOwnerIDs(); len(owners) != 0 {
			t.Errorf("%s left process-local physical write reservations after fixture cleanup: %v", t.Name(), owners)
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
			return NewClient(f.store).SetState(ctx, f.server.Listener.Addr().String(), map[string]any{"on": false})
		},
		"controller record with changed address and port": func() error {
			controller, err := f.store.GetController(ctx, f.controller)
			if err != nil {
				return err
			}
			controller.IpAddress = "127.0.0.1"
			controller.Port.Int64++
			controller.Port.Valid = true
			return NewClient(f.store).ApplyController(ctx, controller, 10, 0, 1, State{Color: "#00FF00", Mode: ModeSolid})
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

func TestPhysicalLocateAppliesSavedSerpentineMappingAndReversedWLEDOutputExactly(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	var outputID int64
	if err := f.database.QueryRowContext(ctx, `INSERT INTO controller_outputs (controller_id,display_order,name,pixel_count,direction,pattern,rows,columns,custom_order_json) VALUES (?,0,'Serpentine output',200,'reverse','serpentine_rows',10,20,'[]') RETURNING id`, f.controller).Scan(&outputID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ExecContext(ctx, `INSERT INTO cabinet_led_strings (cabinet_id,controller_id,name,display_order,pixel_count,layout_complete,controller_output_id) VALUES (1,?,'Serpentine output',0,200,1,?)`, f.controller, outputID); err != nil {
		t.Fatal(err)
	}
	f.mock.setConfig(`{"hw":{"led":{"ins":[{"start":0,"len":90,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":90,"len":60,"rev":true,"skip":0,"type":22,"pin":[4,-1,-1]},{"start":150,"len":50,"rev":false,"skip":0,"type":22,"pin":[16,-1,-1]}]}}}`)
	f.mock.mu.Lock()
	f.mock.state = `{"on":true,"seg":[{"id":10,"start":0,"stop":75,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"of":0,"col":[[20,30,40],[0,0,0],[0,0,0]]},{"id":20,"start":75,"stop":150,"on":true,"fx":0,"grp":1,"spc":0,"rev":true,"mi":false,"of":0,"col":[[50,60,70],[0,0,0],[0,0,0]]},{"id":30,"start":150,"stop":200,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"of":0,"col":[[70,80,90],[0,0,0],[0,0,0]]}]}`
	f.mock.mu.Unlock()
	if err := f.service.SaveControllerMappingVerification(ctx, f.controller, true); err != nil {
		t.Fatalf("verify read-only reversed WLED output mapping: %v", err)
	}

	initial := f.mock.pixels()
	session, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7)
	if err != nil {
		t.Fatalf("locate with saved serpentine/reverse mapping and reversed WLED output: %v", err)
	}
	if session.State != "active" {
		t.Fatalf("Locate state = %q, want active", session.State)
	}
	wantLocate := []string{
		`{"seg":[{"i":[49,51,[128,0,0]],"id":10}]}`,
		`{"seg":[{"i":[10,14,[128,0,0]],"id":20}]}`,
		`{"seg":[{"i":[66,70,[128,0,0]],"id":20}]}`,
	}
	if got := f.mock.rawPosts(); !reflect.DeepEqual(got, wantLocate) {
		t.Fatalf("Locate JSON commands = %q, want exact reversed-output/segment-boundary commands %q", got, wantLocate)
	}
	selected := map[int]bool{49: true, 50: true, 80: true, 81: true, 82: true, 83: true, 100: true, 101: true, 102: true, 103: true}
	locatedFrames := f.mock.pixels()
	for pixel, before := range initial {
		want := before
		if selected[pixel] {
			want = [3]int{128, 0, 0}
		}
		if got := locatedFrames[pixel]; got != want {
			t.Fatalf("physical pixel %d after Locate = %v, want %v", pixel, got, want)
		}
	}
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("stop exact mapped Locate: %v", err)
	}
	wantPosts := append(append([]string(nil), wantLocate...),
		`{"seg":[{"i":[49,51,[0,0,0]],"id":10}]}`,
		`{"seg":[{"i":[10,14,[0,0,0]],"id":20}]}`,
		`{"seg":[{"i":[66,70,[0,0,0]],"id":20}]}`,
	)
	if got := f.mock.rawPosts(); !reflect.DeepEqual(got, wantPosts) {
		t.Fatalf("Locate/Stop JSON commands = %q, want exact saved targeted commands %q", got, wantPosts)
	}
	frames := f.mock.pixels()
	for pixel, before := range initial {
		want := before
		if selected[pixel] {
			want = [3]int{0, 0, 0}
		}
		if frames[pixel] != want {
			t.Errorf("physical pixel %d after Stop = %v, want %v", pixel, frames[pixel], want)
		}
	}
}

func TestPhysicalMappingFingerprintChecksReportedControllerCapabilities(t *testing.T) {
	var info physicalInfo
	info.LEDs.Count = 8
	info.Architecture = "esp32"
	var cfg physicalConfig
	cfg.Hardware.LED.Inputs = []physicalOutput{
		{Start: int64Pointer(0), Length: 4, Pins: json.RawMessage(`[2,-1,-1]`), Type: 22},
		{Start: int64Pointer(4), Length: 4, Pins: json.RawMessage(`[4,-1,-1]`), Type: 22},
	}
	if _, err := physicalMappingFingerprint(db.Controller{ControllerType: "wled-esp8266"}, info, cfg, 8); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("architecture mismatch error = %v, want specific controller type conflict", err)
	}
	info.Architecture = "esp8266"
	if _, err := physicalMappingFingerprint(db.Controller{ControllerType: "wled-esp8266"}, info, cfg, 8); err == nil || !strings.Contains(err.Error(), "exceeding the 1-output capability") {
		t.Fatalf("output capability error = %v, want explicit one-output limit", err)
	}
	info.Architecture = "esp32"
	if _, err := physicalMappingFingerprint(db.Controller{ControllerType: "wled-esp32"}, info, cfg, 8); err != nil {
		t.Fatalf("matching read-only WLED architecture/output capability was rejected: %v", err)
	}
}

func TestPhysicalLocateAppliesExplicitCustomPermutationToExactPixels(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	customOrder := make([]int64, 200)
	for logical := range customOrder {
		customOrder[logical] = int64((logical + 1) % len(customOrder))
	}
	encodedOrder, err := json.Marshal(customOrder)
	if err != nil {
		t.Fatal(err)
	}
	var outputID int64
	if err := f.database.QueryRowContext(ctx, `INSERT INTO controller_outputs (controller_id,display_order,name,pixel_count,direction,pattern,rows,columns,custom_order_json) VALUES (?,0,'Custom permuted output',200,'forward','custom',1,1,?) RETURNING id`, f.controller, string(encodedOrder)).Scan(&outputID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ExecContext(ctx, `INSERT INTO cabinet_led_strings (cabinet_id,controller_id,name,display_order,pixel_count,layout_complete,controller_output_id) VALUES (1,?,'Custom permuted output',0,200,1,?)`, f.controller, outputID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ExecContext(ctx, `DELETE FROM drawer_led_allocations WHERE drawer_id=?`, f.drawer1); err != nil {
		t.Fatal(err)
	}
	for _, allocation := range []db.CreateDrawerLEDAllocationParams{
		{CabinetID: 1, DrawerID: f.drawer1, StartPixel: 197, PixelCount: 4, DisplayOrder: 0},
		{CabinetID: 1, DrawerID: f.drawer1, StartPixel: 50, PixelCount: 2, DisplayOrder: 1},
	} {
		if _, err := f.store.CreateDrawerLEDAllocation(ctx, allocation); err != nil {
			t.Fatal(err)
		}
	}
	f.mock.setConfig(`{"hw":{"led":{"ins":[{"start":0,"len":90,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":90,"len":60,"rev":true,"skip":0,"type":22,"pin":[4,-1,-1]},{"start":150,"len":50,"rev":false,"skip":0,"type":22,"pin":[16,-1,-1]}]}}}`)
	if err := f.service.SaveControllerMappingVerification(ctx, f.controller, true); err != nil {
		t.Fatalf("verify controller before custom-permutation Locate: %v", err)
	}

	initial := f.mock.pixels()
	if _, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err != nil {
		t.Fatalf("Locate custom permutation: %v", err)
	}
	wantLocate := []string{
		`{"seg":[{"i":[0,1,[128,0,0]],"id":10}]}`,
		`{"seg":[{"i":[50,52,[128,0,0]],"id":10}]}`,
		`{"seg":[{"i":[97,100,[128,0,0]],"id":20}]}`,
	}
	if got := f.mock.rawPosts(); !reflect.DeepEqual(got, wantLocate) {
		t.Fatalf("custom-permutation Locate JSON = %q, want exact disjoint physical pixels %q", got, wantLocate)
	}
	selected := map[int]bool{0: true, 50: true, 51: true, 197: true, 198: true, 199: true}
	located := f.mock.pixels()
	for pixel, before := range initial {
		want := before
		if selected[pixel] {
			want = [3]int{128, 0, 0}
		}
		if located[pixel] != want {
			t.Errorf("custom permutation physical pixel %d = %v, want %v", pixel, located[pixel], want)
		}
	}
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("Stop custom-permutation Locate: %v", err)
	}
	cleared := f.mock.pixels()
	for pixel, before := range initial {
		want := before
		if selected[pixel] {
			want = [3]int{0, 0, 0}
		}
		if cleared[pixel] != want {
			t.Errorf("custom permutation physical pixel %d after Stop = %v, want %v", pixel, cleared[pixel], want)
		}
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
	commands, _, err := mapPhysicalRanges([]ControllerRange{{Start: 97, Count: 8}, {Start: 150, Count: 2}}, db.Controller{LedCount: 200, ControllerType: "wled"}, 200, info, cfg, state, "#FF0000", locateBrightness)
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

func TestMapPhysicalRangesComposesWLEDOutputAndSegmentReversal(t *testing.T) {
	var info physicalInfo
	info.MAC = "AA:BB:CC:DD:EE:FF"
	info.LEDs.Count = 8
	var cfg physicalConfig
	cfg.Hardware.LED.Inputs = []physicalOutput{
		{Start: int64Pointer(0), Length: 4, Reverse: true, Pins: json.RawMessage(`[2,-1,-1]`), Type: 22},
		{Start: int64Pointer(4), Length: 4, Pins: json.RawMessage(`[4,-1,-1]`), Type: 22},
	}
	var state physicalState
	if err := json.Unmarshal([]byte(`{"on":true,"seg":[{"id":3,"start":0,"stop":4,"on":true,"fx":0,"grp":1,"spc":0,"rev":true,"mi":false,"of":0},{"id":9,"start":4,"stop":8,"on":true,"fx":0,"grp":1,"spc":0,"rev":false,"mi":false,"of":0}]}`), &state); err != nil {
		t.Fatal(err)
	}
	commands, _, err := mapPhysicalRanges([]ControllerRange{{Start: 1, Count: 2}, {Start: 4, Count: 1}, {Start: 7, Count: 2}}, db.Controller{LedCount: 8, ControllerType: "wled"}, 8, info, cfg, state, "#00FF00", locateBrightness)
	if err != nil {
		t.Fatalf("map reversed device outputs and WLED segments: %v", err)
	}
	want := []physicalPixelCommand{
		{SegmentID: 3, Start: 0, Stop: 2},
		{SegmentID: 3, Start: 3, Stop: 4},
		{SegmentID: 9, Start: 2, Stop: 4},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %+v, want exact segment-local commands %+v", commands, want)
	}
}

func TestPhysicalLocateWLED015FixturesExactWireProtocol(t *testing.T) {
	f := newPhysicalFixture(t)
	readFixture := func(name string) string {
		t.Helper()
		data, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatalf("read WLED firmware fixture %s: %v", name, err)
		}
		return string(data)
	}
	f.mock.setInfo(readFixture("wled-0.15-info.json"))
	f.mock.setConfig(readFixture("wled-0.15-cfg.json"))
	f.mock.setState(readFixture("wled-0.15-state.json"))
	if _, err := f.database.ExecContext(context.Background(), `UPDATE controllers SET mac_address='AA:BB:CC:DD:EE:FF' WHERE id=?`, f.controller); err != nil {
		t.Fatalf("save fixture controller identity: %v", err)
	}

	requestBase := f.mock.requestCount()
	if err := f.service.SaveControllerMappingVerification(context.Background(), f.controller, true); err != nil {
		t.Fatalf("verify firmware-shaped /json/info and /json/cfg fixtures: %v", err)
	}
	if got := f.mock.requestsSince(requestBase); !reflect.DeepEqual(got, []string{"GET /json/info", "GET /json/cfg"}) {
		t.Fatalf("mapping verification requests = %v, want GET /json/info then GET /json/cfg only", got)
	}

	initial := f.mock.pixels()
	session, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin1, 7)
	if err != nil {
		t.Fatalf("Locate using firmware-shaped WLED responses: %v", err)
	}
	if session.State != "active" || session.ControllerID != f.controller {
		t.Fatalf("firmware fixture Locate session = %+v", session)
	}
	if err := f.service.StopDrawer(context.Background(), f.drawer1, 7); err != nil {
		t.Fatalf("Stop firmware fixture Locate: %v", err)
	}

	wantRequests := []string{
		"GET /json/info", "GET /json/cfg",
		"GET /json/info", "GET /json/cfg", "GET /json/state",
		"POST /json/state", "POST /json/state", "POST /json/state",
		"GET /json/info", "GET /json/cfg", "GET /json/state",
		"POST /json/state", "POST /json/state", "POST /json/state",
	}
	if got := f.mock.requestsSince(requestBase); !reflect.DeepEqual(got, wantRequests) {
		t.Fatalf("WLED protocol sequence = %v, want %v", got, wantRequests)
	}
	wantBodies := []string{
		`{"seg":[{"i":[96,100,[128,0,0]],"id":10}]}`,
		`{"seg":[{"i":[0,4,[128,0,0]],"id":20}]}`,
		`{"seg":[{"i":[49,51,[128,0,0]],"id":20}]}`,
		`{"seg":[{"i":[96,100,[0,0,0]],"id":10}]}`,
		`{"seg":[{"i":[0,4,[0,0,0]],"id":20}]}`,
		`{"seg":[{"i":[49,51,[0,0,0]],"id":20}]}`,
	}
	if got := f.mock.rawPosts(); !reflect.DeepEqual(got, wantBodies) {
		t.Fatalf("WLED state POST bodies = %q, want exact targeted protocol payloads %q", got, wantBodies)
	}
	for index, contentType := range f.mock.postTypes() {
		if contentType != "application/json" {
			t.Errorf("POST %d Content-Type = %q, want application/json", index+1, contentType)
		}
	}
	frames := f.mock.pixels()
	for pixel, before := range initial {
		owned := (pixel >= 96 && pixel < 104) || (pixel >= 149 && pixel < 151)
		if owned {
			if frames[pixel] != [3]int{0, 0, 0} {
				t.Errorf("owned pixel %d remained %v after Stop", pixel, frames[pixel])
			}
		} else if frames[pixel] != before {
			t.Errorf("unowned pixel %d changed from %v to %v", pixel, before, frames[pixel])
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
	if !ok || session.State != "stopped" || session.LastOperation != "WLED acknowledged targeted cleanup on all affected controllers" {
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
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin1, 7); !errors.Is(err, ErrLocateMappingUnverified) {
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
		if err := f.service.StopDrawer(context.Background(), f.drawer2, 7); !errors.Is(err, ErrLocateSessionStale) {
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

func TestPhysicalLocateMappingVerificationReportsOfflineControllerDetails(t *testing.T) {
	f := newPhysicalFixture(t)
	f.mock.mu.Lock()
	f.mock.offline = true
	f.mock.mu.Unlock()

	_, err := f.service.VerifyControllerMapping(context.Background(), f.controller)
	if err == nil || !strings.Contains(err.Error(), "GET /json/info") || !strings.Contains(err.Error(), "503 Service Unavailable") || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline verification error = %v, want endpoint, status and firmware detail", err)
	}
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

func TestPhysicalLocateSessionBlocksElectricalConfigurationWrites(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	if _, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := f.service.WithoutActiveSession(ctx, func() error { called = true; return nil }); !errors.Is(err, ErrLocateSessionActive) {
		t.Fatalf("configuration write during active Locate = %v, want active-session refusal", err)
	}
	if called {
		t.Fatal("configuration write callback ran while Locate owned pixels")
	}
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatal(err)
	}
	if err := f.service.WithoutActiveSession(ctx, func() error { called = true; return nil }); err != nil {
		t.Fatalf("configuration write after confirmed Stop: %v", err)
	}
	if !called {
		t.Fatal("configuration write callback did not run after Stop")
	}
}

func TestPhysicalLocateUsesTheDrawerAssignedSecondaryController(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryMock := newPhysicalMock()
	secondaryServer := httptest.NewServer(secondaryMock)
	t.Cleanup(secondaryServer.Close)
	secondaryHost, secondaryPortText, err := net.SplitHostPort(secondaryServer.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var secondaryPort int64
	if _, err := fmt.Sscan(secondaryPortText, &secondaryPort); err != nil {
		t.Fatal(err)
	}
	secondary, err := f.store.CreateController(ctx, db.CreateControllerParams{Name: "secondary mock", IpAddress: secondaryHost, Port: sql.NullInt64{Int64: secondaryPort, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{LedCount: 200, ID: secondary.ID, LedCount_2: 0}); err != nil {
		t.Fatal(err)
	}
	cabinet, err := f.store.GetCabinet(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinet.ID, ControllerID: secondary.ID, Slot: 1}); err != nil {
		t.Fatal(err)
	}
	drawer, err := f.store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinet.ID, Label: "Secondary drawer", DisplayOrder: 2, X: 24, Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	bin, err := f.store.CreateBin(ctx, db.CreateBinParams{Name: "Secondary bin", Width: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: bin, DrawerID: drawer, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: cabinet.ID, DrawerID: drawer, StartPixel: 297, PixelCount: 8}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SaveControllerMappingVerification(ctx, secondary.ID, true); err != nil {
		t.Fatal(err)
	}
	secondaryRequestsBeforeLocate := secondaryMock.requestCount()
	primaryRequestsBeforeLocate := f.mock.requestCount()
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil {
		t.Fatalf("locate on secondary controller: %v", err)
	}
	if f.mock.requestCount() != primaryRequestsBeforeLocate {
		t.Fatalf("Locate contacted primary controller endpoint; requests: %v", f.mock.requestsSince(primaryRequestsBeforeLocate))
	}
	if got := secondaryMock.requestsSince(secondaryRequestsBeforeLocate); len(got) < 4 || got[0] != "GET /json/info" || got[1] != "GET /json/cfg" || got[2] != "GET /json/state" || !strings.HasPrefix(got[3], "POST /json/state") {
		t.Fatalf("secondary controller endpoint requests = %v, want inspect and pixel write there", got)
	}
	if session.ControllerID != secondary.ID {
		t.Fatalf("Locate selected controller %d, want secondary controller %d", session.ControllerID, secondary.ID)
	}
	if err := f.service.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("stop secondary-controller drawer: %v", err)
	}
	if f.mock.requestCount() != primaryRequestsBeforeLocate {
		t.Fatalf("Stop contacted primary controller endpoint; requests: %v", f.mock.requestsSince(primaryRequestsBeforeLocate))
	}
	secondaryRequests := secondaryMock.requestsSince(secondaryRequestsBeforeLocate)
	if len(secondaryRequests) < 7 || !strings.HasPrefix(secondaryRequests[len(secondaryRequests)-1], "POST /json/state") {
		t.Fatalf("Stop did not write its range-off command to the original controller: %v", secondaryRequests)
	}
	if got := f.service.getSession(secondary.ID); got == nil || got.State != "stopped" {
		t.Fatalf("secondary controller session = %+v, want stopped", got)
	}
}

func TestPhysicalLocateRejectsIncompletePersistedCleanupCommands(t *testing.T) {
	f := newPhysicalFixture(t)
	if _, err := f.service.LocateInventoryBin(context.Background(), f.partID, f.bin2, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.ExecContext(context.Background(), `UPDATE physical_locate_session_targets SET commands_json='[]' WHERE session_id=(SELECT session_id FROM physical_locate_sessions WHERE controller_id=?)`, f.controller); err != nil {
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

// discoveryFixture is a minimal read-only fixture for output discovery. It only
// needs a controller and a mock WLED endpoint, so it deliberately avoids the
// drawer/bin/allocation setup used by physical Locate session tests.
type discoveryFixture struct {
	store      db.Store
	database   *sql.DB
	client     *Client
	service    *PhysicalLocateService
	mock       *physicalMock
	server     *httptest.Server
	controller int64
}

func newDiscoveryFixture(t *testing.T) *discoveryFixture {
	t.Helper()
	mock := newPhysicalMock()
	server := httptest.NewServer(mock)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	var port int64
	if _, err := fmt.Sscan(portText, &port); err != nil {
		server.Close()
		t.Fatal(err)
	}
	database, err := db.Open("file:" + t.TempDir() + "/physical-discovery.db")
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
		database.Close()
		server.Close()
		t.Fatal(err)
	}
	controller, err := store.CreateController(ctx, db.CreateControllerParams{Name: "mock", IpAddress: host, Port: sql.NullInt64{Int64: port, Valid: true}})
	if err != nil {
		database.Close()
		server.Close()
		t.Fatal(err)
	}
	if _, err := store.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{LedCount: 200, ID: controller.ID, LedCount_2: 0}); err != nil {
		database.Close()
		server.Close()
		t.Fatal(err)
	}
	client := NewClient()
	t.Cleanup(func() {
		server.Close()
		database.Close()
	})
	return &discoveryFixture{
		store:      store,
		database:   database,
		client:     client,
		service:    NewPhysicalLocateService(store, client),
		mock:       mock,
		server:     server,
		controller: controller.ID,
	}
}

func assertReadOnlyDiscovery(t *testing.T, mock *physicalMock, since int) {
	t.Helper()
	requests := mock.requestsSince(since)
	if len(requests) == 0 {
		t.Fatal("discovery issued no HTTP requests")
	}
	seenInfo, seenConfig := false, false
	for _, request := range requests {
		switch request {
		case "GET /json/info":
			seenInfo = true
		case "GET /json/cfg":
			seenConfig = true
		default:
			t.Fatalf("discovery issued non-read-only request %q; only GET /json/info and GET /json/cfg are permitted", request)
		}
	}
	if !seenInfo || !seenConfig {
		t.Fatalf("discovery must GET /json/info and /json/cfg; got %v", requests)
	}
	if got := len(mock.posts()); got != 0 {
		t.Fatalf("read-only discovery sent %d POST requests", got)
	}
}

func TestDiscoverPhysicalOutputsReportsOrderedExplicitGeometry(t *testing.T) {
	f := newDiscoveryFixture(t)
	f.mock.setConfig(`{"hw":{"led":{"ins":[{"start":0,"len":90,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":90,"len":60,"rev":true,"skip":0,"type":22,"pin":[4,5,-1]},{"start":150,"len":50,"rev":false,"skip":0,"type":22,"pin":[16,-1,-1]}]}}}`)
	before := f.mock.requestCount()

	layout, err := f.service.DiscoverPhysicalOutputs(context.Background(), f.controller)
	if err != nil {
		t.Fatalf("discover physical outputs: %v", err)
	}
	if layout.ControllerID != f.controller || layout.Capacity != 200 {
		t.Fatalf("layout identity = %+v, want controller %d capacity 200", layout, f.controller)
	}
	if layout.Fingerprint == "" {
		t.Fatal("expected a non-empty mapping fingerprint")
	}
	want := []PhysicalOutput{
		{Order: 0, Start: 0, Length: 90, Pins: []int64{2}, FirstIndex: 1, LastIndex: 90},
		{Order: 1, Start: 90, Length: 60, Pins: []int64{4, 5}, FirstIndex: 91, LastIndex: 150, Reversed: true},
		{Order: 2, Start: 150, Length: 50, Pins: []int64{16}, FirstIndex: 151, LastIndex: 200},
	}
	if !reflect.DeepEqual(layout.Outputs, want) {
		t.Fatalf("outputs = %+v, want %+v", layout.Outputs, want)
	}

	var sequence []int64
	for _, output := range layout.Outputs {
		if output.FirstIndex != output.Start+1 || output.LastIndex != output.Start+output.Length {
			t.Fatalf("output %d numbering %d..%d is not the 1-based inclusive range for start %d length %d", output.Order, output.FirstIndex, output.LastIndex, output.Start, output.Length)
		}
		for index := output.FirstIndex; index <= output.LastIndex; index++ {
			sequence = append(sequence, index)
		}
	}
	if int64(len(sequence)) != layout.Capacity {
		t.Fatalf("controller-wide numbering covers %d LEDs, want %d", len(sequence), layout.Capacity)
	}
	for i, index := range sequence {
		if index != int64(i+1) {
			t.Fatalf("controller-wide index sequence[%d] = %d, want %d", i, index, i+1)
		}
	}
	assertReadOnlyDiscovery(t, f.mock, before)
}

func TestDiscoverPhysicalOutputsSingleOutputUsesOneBasedInclusiveNumbering(t *testing.T) {
	f := newDiscoveryFixture(t)
	f.mock.setConfig(`{"hw":{"led":{"ins":[{"start":0,"len":200,"rev":false,"skip":0,"type":22,"pin":[16,-1,-1]}]}}}`)
	before := f.mock.requestCount()

	layout, err := f.service.DiscoverPhysicalOutputs(context.Background(), f.controller)
	if err != nil {
		t.Fatalf("discover physical outputs: %v", err)
	}
	want := []PhysicalOutput{{Order: 0, Start: 0, Length: 200, Pins: []int64{16}, FirstIndex: 1, LastIndex: 200}}
	if !reflect.DeepEqual(layout.Outputs, want) {
		t.Fatalf("outputs = %+v, want %+v", layout.Outputs, want)
	}
	assertReadOnlyDiscovery(t, f.mock, before)
}

func TestDiscoverPhysicalOutputsRejectsMalformedOrIncompleteGeometry(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{
			name:   "gapped layout",
			config: `{"hw":{"led":{"ins":[{"start":0,"len":100,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":110,"len":100,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`,
		},
		{
			name:   "missing start",
			config: `{"hw":{"led":{"ins":[{"len":100,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":100,"len":100,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`,
		},
		{
			name:   "unsorted start",
			config: `{"hw":{"led":{"ins":[{"start":100,"len":100,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":0,"len":100,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`,
		},
		{
			name:   "missing pin",
			config: `{"hw":{"led":{"ins":[{"start":0,"len":100,"rev":false,"skip":0,"type":22},{"start":100,"len":100,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`,
		},
		{
			name:   "no active pin",
			config: `{"hw":{"led":{"ins":[{"start":0,"len":200,"rev":false,"skip":0,"type":22,"pin":[-1,-1,-1]}]}}}`,
		},
		{
			name:   "duplicate pin",
			config: `{"hw":{"led":{"ins":[{"start":0,"len":100,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":100,"len":100,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]}]}}}`,
		},

		{
			name:   "skipped leds",
			config: `{"hw":{"led":{"ins":[{"start":0,"len":200,"rev":false,"skip":1,"type":22,"pin":[2,-1,-1]}]}}}`,
		},
		{
			name:   "zero length",
			config: `{"hw":{"led":{"ins":[{"start":0,"len":0,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]}]}}}`,
		},
		{
			name:   "no outputs",
			config: `{"hw":{"led":{"ins":[]}}}`,
		},
		{
			name:   "total exceeds capacity",
			config: `{"hw":{"led":{"ins":[{"start":0,"len":150,"rev":false,"skip":0,"type":22,"pin":[2,-1,-1]},{"start":150,"len":100,"rev":false,"skip":0,"type":22,"pin":[4,-1,-1]}]}}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDiscoveryFixture(t)
			f.mock.setConfig(tc.config)
			before := f.mock.requestCount()
			if _, err := f.service.DiscoverPhysicalOutputs(context.Background(), f.controller); !errors.Is(err, ErrLocateTopology) {
				t.Fatalf("got %v, want ErrLocateTopology", err)
			}
			assertReadOnlyDiscovery(t, f.mock, before)
		})
	}
}

func TestDiscoverPhysicalOutputsRejectsCapacityMismatch(t *testing.T) {
	f := newDiscoveryFixture(t)
	// The controller is saved with 300 LEDs while WLED reports 200.
	if _, err := f.store.UpdateControllerLedCount(context.Background(), db.UpdateControllerLedCountParams{LedCount: 300, ID: f.controller, LedCount_2: 200}); err != nil {
		t.Fatal(err)
	}
	before := f.mock.requestCount()
	if _, err := f.service.DiscoverPhysicalOutputs(context.Background(), f.controller); !errors.Is(err, ErrLocateTopology) {
		t.Fatalf("got %v, want capacity-mismatch ErrLocateTopology", err)
	}
	assertReadOnlyDiscovery(t, f.mock, before)
}

func TestDiscoverPhysicalOutputsRejectsIdentityMismatch(t *testing.T) {
	f := newDiscoveryFixture(t)
	if _, err := f.database.ExecContext(context.Background(), `UPDATE controllers SET mac_address=? WHERE id=?`, "AA:BB:CC:DD:EE:FF", f.controller); err != nil {
		t.Fatal(err)
	}
	before := f.mock.requestCount()
	if _, err := f.service.DiscoverPhysicalOutputs(context.Background(), f.controller); !errors.Is(err, ErrLocateTopology) {
		t.Fatalf("got %v, want identity-mismatch ErrLocateTopology", err)
	}
	assertReadOnlyDiscovery(t, f.mock, before)
}

func TestDiscoverPhysicalOutputsRejectsInvalidOrUnknownControllerBeforeReading(t *testing.T) {
	f := newDiscoveryFixture(t)
	before := f.mock.requestCount()
	if _, err := f.service.DiscoverPhysicalOutputs(context.Background(), 0); err == nil {
		t.Fatal("expected an error for a non-positive controller id")
	}
	if _, err := f.service.DiscoverPhysicalOutputs(context.Background(), 999999); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("got %v, want sql.ErrNoRows for an unknown controller", err)
	}
	if got := f.mock.requestsSince(before); got != nil {
		t.Fatalf("invalid controller discovery issued HTTP requests: %v", got)
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
