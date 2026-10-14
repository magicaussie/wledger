package wled

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

type closeAfterAttemptStore struct {
	db.Store
	server *httptest.Server
	calls  int
}

func (s *closeAfterAttemptStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	s.calls++
	err := s.Store.ExecImmediateTx(ctx, fn)
	if s.calls == 2 && err == nil {
		s.server.Close()
	}
	return err
}

func TestPhysicalLocateConnectionRefusedBeforeReservationSendsNoPixels(t *testing.T) {
	f := newPhysicalFixture(t)
	initial := f.mock.pixels()
	f.server.Close()

	if _, err := f.service.LocateDrawerBin(context.Background(), f.drawer1, f.bin1, 7); err == nil {
		t.Fatal("Locate succeeded with a refused controller connection")
	}
	sessions, err := f.store.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("connection refusal before reservation left sessions: %+v", sessions)
	}
	if len(f.mock.posts()) != 0 || !reflect.DeepEqual(f.mock.pixels(), initial) {
		t.Fatal("connection refusal sent or applied an activation command")
	}
}

func TestPhysicalLocateConnectionRefusedAfterAttemptMarkerRetainsOwnership(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	initial := f.mock.pixels()
	store := &closeAfterAttemptStore{Store: f.store, server: f.server}
	service := NewPhysicalLocateService(store, NewClient())

	if _, err := service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err == nil {
		t.Fatal("Locate succeeded after the controller endpoint was closed before HTTP send")
	}
	sessions, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].State != "partial" {
		t.Fatalf("connection-refused durable session = %+v err=%v, want partial", sessions, err)
	}
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID)
	if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 1 || targets[0].CommandsJson == "[]" {
		t.Fatalf("connection refusal lost possible-delivery ownership or cleanup commands: %+v err=%v", targets, err)
	}
	if len(f.mock.posts()) != 0 || !reflect.DeepEqual(f.mock.pixels(), initial) {
		t.Fatal("refused activation was observed or applied by the controller")
	}
}

func TestPhysicalLocateTimeoutBeforeControllerReadsRequestRetainsOwnership(t *testing.T) {
	previousTimeout := physicalLocateControllerTimeout
	physicalLocateControllerTimeout = 80 * time.Millisecond
	defer func() { physicalLocateControllerTimeout = previousTimeout }()

	f := newPhysicalFixture(t)
	ctx := context.Background()
	initial := f.mock.pixels()
	f.mock.mu.Lock()
	f.mock.stallMethod = http.MethodPost
	f.mock.stallPath = "/json/state"
	f.mock.stallFor = time.Second
	f.mock.mu.Unlock()

	started := time.Now()
	if _, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err == nil {
		t.Fatal("Locate succeeded although the controller never read or acknowledged the request")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("controller timeout was not bounded: %v", elapsed)
	}
	sessions, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].State != "partial" {
		t.Fatalf("durable timeout session = %+v err=%v, want partial", sessions, err)
	}
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID)
	if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("timeout lost uncertain delivery ownership: %+v err=%v", targets, err)
	}
	if len(f.mock.posts()) != 0 || !reflect.DeepEqual(f.mock.pixels(), initial) {
		t.Fatal("unread timed-out requests were recorded as applied pixel commands")
	}

	f.mock.mu.Lock()
	f.mock.stallMethod = ""
	f.mock.stallPath = ""
	f.mock.mu.Unlock()
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("targeted cleanup after controller recovers: %v", err)
	}
	if len(f.mock.posts()) == 0 {
		t.Fatal("recovery did not issue the saved targeted cleanup")
	}
	for _, post := range f.mock.posts() {
		if _, global := post["on"]; global {
			t.Fatalf("timeout recovery used controller-wide state: %#v", post)
		}
	}
	final := f.mock.pixels()
	for _, target := range targets {
		var commands []physicalPixelCommand
		if err := json.Unmarshal([]byte(target.CommandsJson), &commands); err != nil {
			t.Fatal(err)
		}
		for _, command := range commands {
			offset := 0
			if command.SegmentID == 20 {
				offset = 100
			}
			for pixel := int(command.Start) + offset; pixel < int(command.Stop)+offset; pixel++ {
				if final[pixel] != [3]int{0, 0, 0} {
					t.Errorf("timed-out owned pixel %d remains lit: %v", pixel, final[pixel])
				}
			}
		}
	}
}

func TestPhysicalLocateControllerReceivesRequestButNeverResponds(t *testing.T) {
	previousTimeout := physicalLocateControllerTimeout
	physicalLocateControllerTimeout = 100 * time.Millisecond
	defer func() { physicalLocateControllerTimeout = previousTimeout }()

	f := newPhysicalFixture(t)
	ctx := context.Background()
	accepted := make(chan struct{})
	f.mock.mu.Lock()
	f.mock.postAccepted = accepted
	f.mock.postResponseRelease = make(chan struct{})
	f.mock.failPostNumber = 2 // the first targeted cleanup is applied but answered with HTTP 503
	f.mock.mu.Unlock()

	result := make(chan error, 1)
	go func() {
		_, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7)
		result <- err
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("mock controller did not accept the full activation request")
	}
	if err := <-result; err == nil {
		t.Fatal("timed-out controller response with failed cleanup reported success")
	}
	sessions, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].State != "partial" {
		t.Fatalf("lost-response session = %+v err=%v, want partial", sessions, err)
	}
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID)
	if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("lost-response/failed-cleanup target ownership = %+v err=%v", targets, err)
	}
	if applied := f.mock.appliedPosts(); len(applied) != 2 || !applied[0] || !applied[1] {
		t.Fatalf("mock application evidence = %v, want applied activation plus applied but failed cleanup", applied)
	}

	f.mock.mu.Lock()
	f.mock.postResponseRelease = nil
	f.mock.failPostNumber = 0
	f.mock.mu.Unlock()
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("recovery after response timeout: %v", err)
	}
	if marker, err := f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID); err != nil || len(marker) != 1 || marker[0].MayHaveReceived != 0 {
		t.Fatalf("recovery failed to resolve uncertain target marker: %+v err=%v", marker, err)
	}
}

func TestPhysicalLocateMalformedControllerJSONFailsBeforeReservation(t *testing.T) {
	f := newPhysicalFixture(t)
	f.mock.mu.Lock()
	f.mock.malformedGETPath = "/json/info"
	f.mock.mu.Unlock()
	initial := f.mock.pixels()

	if _, err := f.service.LocateDrawerBin(context.Background(), f.drawer1, f.bin1, 7); err == nil {
		t.Fatal("Locate accepted malformed controller JSON")
	}
	sessions, err := f.store.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 || len(f.mock.posts()) != 0 || !reflect.DeepEqual(f.mock.pixels(), initial) {
		t.Fatalf("malformed preflight changed ownership or LEDs: sessions=%+v posts=%v", sessions, f.mock.posts())
	}
}

func TestPhysicalLocateHTTP500AfterApplicationIsCleanedBySavedRanges(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	initial := f.mock.pixels()
	f.mock.failPostNumber = 1
	f.mock.failPostStatus = http.StatusInternalServerError

	if _, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err == nil {
		t.Fatal("Locate hid the controller's HTTP 500")
	}
	sessions, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].State != "failed" {
		t.Fatalf("HTTP 500 cleanup result = %+v err=%v, want failed after confirmed targeted cleanup", sessions, err)
	}
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID)
	if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 0 {
		t.Fatalf("HTTP 500 cleanup did not resolve ownership: %+v err=%v", targets, err)
	}
	if len(f.mock.posts()) < 2 {
		t.Fatalf("controller error produced %d writes, want activation error plus cleanup", len(f.mock.posts()))
	}
	for _, post := range f.mock.posts() {
		if _, global := post["on"]; global {
			t.Fatalf("HTTP 500 recovery used controller-wide state: %#v", post)
		}
	}
	final := f.mock.pixels()
	for pixel, got := range final {
		if got != initial[pixel] && got != [3]int{0, 0, 0} {
			t.Errorf("HTTP 500 changed pixel %d to unexpected color %v", pixel, got)
		}
	}
}

func TestPhysicalLocateRepeatedStopRetainsOwnershipUntilControllerReconnects(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	if _, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err != nil {
		t.Fatal(err)
	}
	f.mock.mu.Lock()
	f.mock.offline = true
	f.mock.mu.Unlock()

	for attempt := 1; attempt <= 2; attempt++ {
		if err := f.service.StopDrawer(ctx, f.drawer1, 7); err == nil {
			t.Fatalf("Stop attempt %d succeeded while the controller was unavailable", attempt)
		}
		sessions, err := f.store.ListPhysicalLocateSessions(ctx)
		if err != nil || len(sessions) != 1 || sessions[0].State != "partial" {
			t.Fatalf("offline Stop attempt %d state=%+v err=%v", attempt, sessions, err)
		}
		targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID)
		if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 1 {
			t.Fatalf("offline Stop attempt %d lost durable ownership: %+v err=%v", attempt, targets, err)
		}
	}

	f.mock.mu.Lock()
	f.mock.offline = false
	f.mock.mu.Unlock()
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("Stop after controller reconnect: %v", err)
	}
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("repeated Stop after confirmed cleanup: %v", err)
	}
	if len(f.mock.posts()) != 6 {
		t.Fatalf("repeated recovery issued %d writes, want three activations and three saved cleanup ranges", len(f.mock.posts()))
	}
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, f.service.getSession(f.controller).ID)
	if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 0 {
		t.Fatalf("reconnected target ownership = %+v err=%v", targets, err)
	}
	for _, post := range f.mock.posts() {
		if _, global := post["on"]; global {
			t.Fatalf("repeated Stop sent GlobalOff: %#v", post)
		}
	}
}
