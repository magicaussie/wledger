package wled

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
)

func secondPhysicalStore(t *testing.T, fixture *physicalFixture) (db.Store, *sql.DB) {
	t.Helper()
	var seq int
	var name, path string
	if err := fixture.database.QueryRowContext(context.Background(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatalf("resolve disposable database path: %v", err)
	}
	conn, err := db.Open("file:" + path)
	if err != nil {
		t.Fatalf("open independent SQLite connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return db.NewStore(conn), conn
}

func disableProcessLocalGateForSecondInstance(t *testing.T, fixture *physicalFixture) {
	t.Helper()
	address := fixture.server.Listener.Addr().String()
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	var port int64
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	hardwareWriteLease.mu.Lock()
	hardwareWriteLease.physicalOwners = make(map[string]struct{})
	gate := writeGate(address, port)
	gate.mu.Lock()
	gate.reserved = false
	gate.mu.Unlock()
	hardwareWriteLease.mu.Unlock()
}

func awaitCoordinationSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func TestPhysicalLocateCrossConnectionClaimBlocksLegacyAndConfigurationWrites(t *testing.T) {
	fixture := newPhysicalFixture(t)
	store2, _ := secondPhysicalStore(t, fixture)
	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.mock.mu.Lock()
	fixture.mock.postEntered = entered
	fixture.mock.postRelease = release
	fixture.mock.mu.Unlock()

	locateResult := make(chan error, 1)
	go func() {
		_, err := fixture.service.LocateDrawerBin(context.Background(), fixture.drawer1, fixture.bin1, 7)
		locateResult <- err
	}()
	awaitCoordinationSignal(t, entered, "activation POST to enter its SQLite claim")
	disableProcessLocalGateForSecondInstance(t, fixture)

	legacyResult := make(chan error, 1)
	go func() {
		legacyResult <- NewClient(store2).SetState(context.Background(), fixture.server.Listener.Addr().String(), map[string]any{"on": true})
	}()

	cabinet, err := fixture.store.GetCabinetByAssignedController(context.Background(), fixture.controller)
	if err != nil {
		t.Fatal(err)
	}
	configurationResult := make(chan error, 1)
	go func() {
		configurationResult <- storage.NewLayoutService(store2).SaveDrawerLEDAllocations(
			context.Background(), cabinet.ID, fixture.drawer1, cabinet.ElectricalRevision,
			[]storage.LEDAllocationInput{{StartPixel: 97, PixelCount: 8}, {StartPixel: 150, PixelCount: 2}},
		)
	}()

	select {
	case err := <-legacyResult:
		t.Fatalf("legacy write escaped the held operation claim: %v", err)
	case err := <-configurationResult:
		t.Fatalf("configuration mutation escaped the held operation claim: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	if err := <-locateResult; err != nil {
		t.Fatalf("physical Locate: %v", err)
	}
	if err := <-legacyResult; !errors.Is(err, ErrHardwareBusy) {
		t.Fatalf("cross-connection legacy write error = %v, want ErrHardwareBusy", err)
	}
	if err := <-configurationResult; !errors.Is(err, db.ErrPhysicalLocateActive) {
		t.Fatalf("cross-connection configuration error = %v, want ErrPhysicalLocateActive", err)
	}

	fixture.mock.mu.Lock()
	posts := append([]map[string]any(nil), fixture.mock.postBodies...)
	fixture.mock.mu.Unlock()
	if len(posts) != 3 {
		t.Fatalf("got %d WLED writes, want exactly three targeted activation ranges", len(posts))
	}
	if _, exists := posts[0]["on"]; exists {
		t.Fatalf("physical Locate emitted an unexpected controller-wide state: %#v", posts[0])
	}
	if _, exists := posts[0]["seg"]; !exists {
		t.Fatalf("physical Locate did not use targeted segment/range state: %#v", posts[0])
	}
	status, found, err := NewPhysicalLocateService(store2, NewClient()).SessionStatusForDrawer(context.Background(), fixture.drawer1)
	if err != nil || !found || status.State != "active" {
		t.Fatalf("durable session after competing writes = %+v, found=%v, err=%v", status, found, err)
	}
}

func TestPhysicalLocateStopRacingActivationCompletesTargetedCleanupInOrder(t *testing.T) {
	fixture := newPhysicalFixture(t)
	store2, _ := secondPhysicalStore(t, fixture)
	stopper := NewPhysicalLocateService(store2, NewClient())
	entered := make(chan struct{})
	release := make(chan struct{})
	fixture.mock.mu.Lock()
	fixture.mock.postEntered = entered
	fixture.mock.postRelease = release
	fixture.mock.mu.Unlock()

	locateResult := make(chan error, 1)
	go func() {
		_, err := fixture.service.LocateDrawerBin(context.Background(), fixture.drawer1, fixture.bin1, 7)
		locateResult <- err
	}()
	awaitCoordinationSignal(t, entered, "Locate activation command claim")
	reserved, err := store2.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reserved) != 1 || reserved[0].State != "activating" {
		t.Fatalf("session state before activation POST = %+v", reserved)
	}
	reservedTargets, err := store2.ListPhysicalLocateSessionTargets(context.Background(), reserved[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reservedTargets) != 1 || reservedTargets[0].MayHaveReceived != 1 {
		t.Fatalf("activation attempt was not durably marked before POST: %+v", reservedTargets)
	}
	stopResult := make(chan error, 1)
	go func() { stopResult <- stopper.StopDrawer(context.Background(), fixture.drawer1, 7) }()
	select {
	case err := <-stopResult:
		t.Fatalf("Stop completed before the in-flight activation claim finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-locateResult; err != nil {
		t.Fatalf("Locate while Stop waited: %v", err)
	}
	if err := <-stopResult; err != nil {
		t.Fatalf("Stop after activation: %v", err)
	}

	sessions, err := store2.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != "stopped" {
		t.Fatalf("durable Locate/Stop state = %+v", sessions)
	}
	targets, err := store2.ListPhysicalLocateSessionTargets(context.Background(), sessions[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].MayHaveReceived != 0 {
		t.Fatalf("durable target state after Stop = %+v", targets)
	}
	fixture.mock.mu.Lock()
	posts := append([]map[string]any(nil), fixture.mock.postBodies...)
	fixture.mock.mu.Unlock()
	if len(posts) != 6 {
		t.Fatalf("got %d WLED writes, want three activations followed by three targeted cleanup commands", len(posts))
	}
	for i, post := range posts {
		if _, exists := post["on"]; exists {
			t.Fatalf("Locate/Stop emitted controller-wide command at write %d: %#v", i, post)
		}
		segments, ok := post["seg"].([]any)
		if !ok || len(segments) != 1 {
			t.Fatalf("write %d was not range-targeted: %#v", i, post)
		}
		pixelData, ok := segments[0].(map[string]any)["i"].([]any)
		if !ok || len(pixelData) != 3 {
			t.Fatalf("write %d has no individual-pixel range: %#v", i, post)
		}
		color, ok := pixelData[2].([]any)
		if !ok || len(color) != 3 {
			t.Fatalf("write %d has no RGB pixel color: %#v", i, post)
		}
		wantOff := i >= 3
		isOff := color[0] == float64(0) && color[1] == float64(0) && color[2] == float64(0)
		if wantOff != isOff {
			t.Fatalf("write %d order/color is wrong: %#v", i, post)
		}
	}
}

func TestPhysicalLocateTwoConnectionsConcurrentLocateHasOneDurableOwner(t *testing.T) {
	fixture := newPhysicalFixture(t)
	store2, _ := secondPhysicalStore(t, fixture)
	service2 := NewPhysicalLocateService(store2, NewClient())
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, action := range []func() error{
		func() error {
			_, err := fixture.service.LocateDrawerBin(context.Background(), fixture.drawer1, fixture.bin1, 7)
			return err
		},
		func() error {
			_, err := service2.LocateDrawerBin(context.Background(), fixture.drawer2, fixture.bin2, 7)
			return err
		},
	} {
		wait.Add(1)
		go func(action func() error) {
			defer wait.Done()
			<-start
			results <- action()
		}(action)
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Locate attempt: %v", err)
		}
	}

	sessions, err := store2.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, session := range sessions {
		if session.State == "active" || session.State == "activating" || session.State == "partial" || session.State == "stopping" {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("durable active session count = %d, sessions=%+v", active, sessions)
	}
	targets, err := store2.ListPhysicalLocateSessionTargets(context.Background(), sessions[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("final durable target ownership = %+v", targets)
	}

	fixture.mock.mu.Lock()
	posts := append([]map[string]any(nil), fixture.mock.postBodies...)
	fixture.mock.mu.Unlock()
	for _, post := range posts {
		if _, exists := post["on"]; exists {
			t.Fatalf("concurrent Locate emitted GlobalOff/controller power state: %#v", post)
		}
		if _, exists := post["seg"]; !exists {
			t.Fatalf("concurrent Locate emitted a non-targeted command: %#v", post)
		}
	}
}

func TestPersistCannotClearUnresolvedTargetOwnership(t *testing.T) {
	fixture := newPhysicalFixture(t)
	if _, err := fixture.service.LocateDrawerBin(context.Background(), fixture.drawer1, fixture.bin1, 7); err != nil {
		t.Fatal(err)
	}
	current := fixture.service.getSession(fixture.controller)
	if current == nil {
		t.Fatal("active durable session missing from service")
	}
	stale := *current
	stale.targets = append([]physicalControllerTarget(nil), current.targets...)
	stale.targets[0].MayHaveReceived = false
	if err := fixture.service.persistSession(context.Background(), &stale); err == nil {
		t.Fatal("session persistence cleared target ownership without acknowledged targeted cleanup")
	}
	durable, err := fixture.store.ListPhysicalLocateSessionTargets(context.Background(), current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(durable) != 1 || durable[0].MayHaveReceived != 1 {
		t.Fatalf("unresolved durable target ownership was lost: %+v", durable)
	}
}

func TestPhysicalLocateTwoRecoveryWorkersShareTargetOwnership(t *testing.T) {
	fixture := newPhysicalFixture(t)
	store2, _ := secondPhysicalStore(t, fixture)
	if _, err := fixture.service.LocateDrawerBin(context.Background(), fixture.drawer1, fixture.bin1, 7); err != nil {
		t.Fatal(err)
	}
	workerA := NewPhysicalLocateService(fixture.store, NewClient())
	workerB := NewPhysicalLocateService(store2, NewClient())
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, worker := range []*PhysicalLocateService{workerA, workerB} {
		wait.Add(1)
		go func(worker *PhysicalLocateService) {
			defer wait.Done()
			<-start
			results <- worker.StopDrawer(context.Background(), fixture.drawer1, 7)
		}(worker)
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent recovery: %v", err)
		}
	}

	sessions, err := store2.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != "stopped" {
		t.Fatalf("durable recovery state = %+v", sessions)
	}
	targets, err := store2.ListPhysicalLocateSessionTargets(context.Background(), sessions[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].MayHaveReceived != 0 {
		t.Fatalf("durable recovery target = %+v", targets)
	}
	fixture.mock.mu.Lock()
	posts := append([]map[string]any(nil), fixture.mock.postBodies...)
	fixture.mock.mu.Unlock()
	if len(posts) != 6 {
		t.Fatalf("command count = %d, want three activation ranges and three targeted cleanup ranges: %#v", len(posts), posts)
	}
	for _, post := range posts {
		if _, exists := post["on"]; exists {
			t.Fatalf("recovery emitted unexpected GlobalOff/controller power state: %#v", post)
		}
	}
}

type failAfterCleanupCommandStore struct {
	db.Store
	claimCount int
	failAt     int
}

func (s *failAfterCleanupCommandStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	s.claimCount++
	claimNumber := s.claimCount
	return s.Store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := fn(q); err != nil {
			return err
		}
		if claimNumber == s.failAt {
			return errors.New("simulated SQLite commit failure after acknowledged cleanup")
		}
		return nil
	})
}

func TestPhysicalLocateCleanupCommitFailureRetainsDurableOwnership(t *testing.T) {
	fixture := newPhysicalFixture(t)
	if _, err := fixture.service.LocateDrawerBin(context.Background(), fixture.drawer1, fixture.bin1, 7); err != nil {
		t.Fatal(err)
	}

	store := &failAfterCleanupCommandStore{Store: fixture.store, failAt: 2}
	fixture.service.store = store
	fixture.service.client.SetOperationStore(store)
	if err := fixture.service.StopDrawer(context.Background(), fixture.drawer1, 7); err == nil {
		t.Fatal("Stop succeeded despite the simulated cleanup transaction commit failure")
	}

	sessions, err := fixture.store.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != "partial" {
		t.Fatalf("durable state after acknowledged cleanup/failed commit = %+v, want partial", sessions)
	}
	targets, err := fixture.store.ListPhysicalLocateSessionTargets(context.Background(), sessions[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("failed cleanup commit cleared durable ownership: %+v", targets)
	}
	posts := fixture.mock.posts()
	if len(posts) != 6 {
		t.Fatalf("mock recorded %d requests, want three activation ranges and three acknowledged targeted cleanup ranges", len(posts))
	}
	for i, post := range posts {
		if _, exists := post["on"]; exists {
			t.Fatalf("write %d unexpectedly used controller-wide state: %#v", i, post)
		}
		if _, exists := post["seg"]; !exists {
			t.Fatalf("write %d was not targeted to a saved segment/range: %#v", i, post)
		}
	}
}

func TestPhysicalLocateCancellationDuringAcceptedActivationRetainsOwnership(t *testing.T) {
	fixture := newPhysicalFixture(t)
	accepted := make(chan struct{})
	responseRelease := make(chan struct{})
	fixture.mock.mu.Lock()
	fixture.mock.postAccepted = accepted
	fixture.mock.postResponseRelease = responseRelease
	fixture.mock.failPostNumber = 2 // the first cleanup acknowledgement fails
	fixture.mock.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	locateResult := make(chan error, 1)
	go func() {
		_, err := fixture.service.LocateDrawerBin(ctx, fixture.drawer1, fixture.bin1, 7)
		locateResult <- err
	}()
	awaitCoordinationSignal(t, accepted, "mock WLED acceptance of activation before its response")

	sessions, err := fixture.store.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != "activating" {
		t.Fatalf("durable state during in-flight activation = %+v", sessions)
	}
	targets, err := fixture.store.ListPhysicalLocateSessionTargets(context.Background(), sessions[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("activation was not reserved before WLED accepted it: %+v", targets)
	}

	cancel()
	close(responseRelease)
	if err := <-locateResult; err == nil {
		t.Fatal("canceled activation with unconfirmed cleanup reported success")
	}
	sessions, err = fixture.store.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != "partial" {
		t.Fatalf("durable state after canceled accepted activation = %+v, want partial", sessions)
	}
	targets, err = fixture.store.ListPhysicalLocateSessionTargets(context.Background(), sessions[0].SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("cancellation/failed cleanup cleared pixel ownership: %+v", targets)
	}
	posts := fixture.mock.posts()
	if len(posts) != 2 {
		t.Fatalf("mock recorded %d POSTs, want accepted activation and one failed targeted cleanup", len(posts))
	}
	for i, post := range posts {
		if _, exists := post["on"]; exists {
			t.Fatalf("write %d unexpectedly used controller-wide state: %#v", i, post)
		}
		if _, exists := post["seg"]; !exists {
			t.Fatalf("write %d was not targeted: %#v", i, post)
		}
	}
}

func TestLegacyWLEDWriteFailsClosedWhenSQLiteClaimIsBusy(t *testing.T) {
	fixture := newPhysicalFixture(t)
	store2, conn2 := secondPhysicalStore(t, fixture)
	conn2.SetMaxOpenConns(1)
	if _, err := conn2.ExecContext(context.Background(), "PRAGMA busy_timeout = 100"); err != nil {
		t.Fatal(err)
	}

	held := make(chan struct{})
	release := make(chan struct{})
	claimResult := make(chan error, 1)
	go func() {
		claimResult <- db.WithOperationClaim(context.Background(), fixture.store, func(db.Querier) error {
			close(held)
			<-release
			return nil
		})
	}()
	awaitCoordinationSignal(t, held, "SQLite writer claim")

	before := len(fixture.mock.posts())
	client := NewClient(store2)
	if err := client.SetState(context.Background(), fixture.server.Listener.Addr().String(), map[string]any{"on": true}); err == nil {
		close(release)
		<-claimResult
		t.Fatal("legacy WLED write succeeded while BEGIN IMMEDIATE was busy")
	}
	close(release)
	if err := <-claimResult; err != nil {
		t.Fatalf("release SQLite claim: %v", err)
	}
	if got := len(fixture.mock.posts()); got != before {
		t.Fatalf("SQLite claim failure still sent %d WLED POST(s)", got-before)
	}
}
