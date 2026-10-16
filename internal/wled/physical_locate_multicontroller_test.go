package wled

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func addSecondaryPhysicalTarget(t *testing.T, f *physicalFixture, label string) (int64, *physicalMock) {
	t.Helper()
	ctx := context.Background()
	mock := newPhysicalMock()
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int64
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	controller, err := f.store.CreateController(ctx, db.CreateControllerParams{Name: label, IpAddress: host, Port: sql.NullInt64{Int64: port, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{LedCount: 200, ID: controller.ID, LedCount_2: 0}); err != nil {
		t.Fatal(err)
	}
	cabinet, err := f.store.GetCabinet(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinet.ID, ControllerID: controller.ID, Slot: 1}); err != nil {
		t.Fatal(err)
	}
	return controller.ID, mock
}

func addTertiaryPhysicalTarget(t *testing.T, f *physicalFixture, label string) (int64, *physicalMock) {
	t.Helper()
	ctx := context.Background()
	mock := newPhysicalMock()
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int64
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	controller, err := f.store.CreateController(ctx, db.CreateControllerParams{Name: label, IpAddress: host, Port: sql.NullInt64{Int64: port, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{LedCount: 200, ID: controller.ID, LedCount_2: 0}); err != nil {
		t.Fatal(err)
	}
	cabinet, err := f.store.GetCabinet(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinet.ID, ControllerID: controller.ID, Slot: 2}); err != nil {
		t.Fatal(err)
	}
	return controller.ID, mock
}

func makeThreeControllerDrawer(t *testing.T, f *physicalFixture) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	drawer, err := f.store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: 1, Label: "Three-controller drawer", DisplayOrder: 4, X: 30, Y: 30, Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	bin, err := f.store.CreateBin(ctx, db.CreateBinParams{Name: "Three-controller bin", Width: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: bin, DrawerID: drawer, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: 1, DrawerID: drawer, StartPixel: 195, PixelCount: 210}); err != nil {
		t.Fatal(err)
	}
	return drawer, bin
}

func makeCrossControllerDrawer(t *testing.T, f *physicalFixture) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	drawer, err := f.store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: 1, Label: "Cross-controller drawer", DisplayOrder: 4, X: 30, Y: 30, Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	bin, err := f.store.CreateBin(ctx, db.CreateBinParams{Name: "Cross-controller bin", Width: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: bin, DrawerID: drawer, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: 1, DrawerID: drawer, StartPixel: 195, PixelCount: 12}); err != nil {
		t.Fatal(err)
	}
	return drawer, bin
}

func durablePhysicalTargets(t *testing.T, f *physicalFixture, sessionID string) map[int64]db.PhysicalLocateSessionTarget {
	t.Helper()
	targets, err := f.store.ListPhysicalLocateSessionTargets(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	byController := make(map[int64]db.PhysicalLocateSessionTarget, len(targets))
	for _, target := range targets {
		byController[target.ControllerID] = target
	}
	return byController
}

type retryDeadlineStore struct {
	db.Store
	blockNextLookup atomic.Bool
	activeLookups   atomic.Int32
	maxLookups      atomic.Int32
	lookupStarted   chan struct{}
	lookupCanceled  chan struct{}
}

func (s *retryDeadlineStore) GetController(ctx context.Context, id int64) (db.Controller, error) {
	active := s.activeLookups.Add(1)
	defer s.activeLookups.Add(-1)
	for max := s.maxLookups.Load(); active > max; max = s.maxLookups.Load() {
		if s.maxLookups.CompareAndSwap(max, active) {
			break
		}
	}
	if s.blockNextLookup.CompareAndSwap(true, false) {
		select {
		case s.lookupStarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
		select {
		case s.lookupCanceled <- struct{}{}:
		default:
		}
		return db.Controller{}, ctx.Err()
	}
	return s.Store.GetController(ctx, id)
}

func awaitBoundedRecoveryTimeout(t *testing.T, service *PhysicalLocateService, blockedStore *retryDeadlineStore, controllerID int64, retryBudget, controllerTimeout time.Duration) *physicalSessionState {
	t.Helper()
	select {
	case <-blockedStore.lookupStarted:
	case <-time.After(time.Second):
		t.Fatal("automatic recovery cleanup did not begin controller lookup")
	}
	startedAt := time.Now()
	select {
	case <-blockedStore.lookupCanceled:
	case <-time.After(controllerTimeout + time.Second):
		t.Fatal("automatic recovery cleanup did not honor its retry deadline")
	}
	if elapsed := time.Since(startedAt); elapsed > retryBudget+100*time.Millisecond {
		t.Fatalf("automatic recovery cleanup exceeded its overall deadline: elapsed=%v budget=%v", elapsed, retryBudget)
	}

	physicalLocateCoordinator.Lock()
	service.retryMu.Lock()
	pending := service.getSession(controllerID)
	if pending == nil || pending.timer == nil || pending.LastOperation != "cleanup withheld because session ownership was not confirmed" {
		service.retryMu.Unlock()
		physicalLocateCoordinator.Unlock()
		t.Fatalf("timed-out recovery cleanup did not schedule another retry: %+v", pending)
	}
	pending.timer.Stop()
	pending.timer = nil
	service.retryMu.Unlock()
	physicalLocateCoordinator.Unlock()
	return pending
}

func TestPhysicalLocateRestartRecoveryCleanupUsesBoundedRetry(t *testing.T) {
	previousLocateTimeout := locateSessionTimeout
	previousControllerTimeout := physicalLocateControllerTimeout
	previousRetryTimeout := physicalLocateCleanupRetryTimeout
	previousRetryDelay := physicalLocateCleanupRetryDelay
	locateSessionTimeout = time.Minute
	physicalLocateControllerTimeout = 750 * time.Millisecond
	retryBudget := 125 * time.Millisecond
	physicalLocateCleanupRetryTimeout = retryBudget
	physicalLocateCleanupRetryDelay = time.Hour
	defer func() {
		locateSessionTimeout = previousLocateTimeout
		physicalLocateControllerTimeout = previousControllerTimeout
		physicalLocateCleanupRetryTimeout = previousRetryTimeout
		physicalLocateCleanupRetryDelay = previousRetryDelay
	}()

	f := newPhysicalFixture(t)
	ctx := context.Background()
	session, err := f.service.LocateInventoryBin(ctx, f.partID, f.bin2, 7)
	if err != nil {
		t.Fatalf("activate Locate before restart: %v", err)
	}
	physicalLocateCoordinator.Lock()
	f.service.retryMu.Lock()
	if active := f.service.getSession(f.controller); active != nil && active.timer != nil {
		active.timer.Stop()
		active.timer = nil
	}
	f.service.retryMu.Unlock()
	physicalLocateCoordinator.Unlock()

	locateSessionTimeout = time.Millisecond
	if _, err := f.database.ExecContext(ctx, `UPDATE physical_operation_metadata SET expires_at=? WHERE session_id=?`, time.Now().Add(-time.Second), session.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	blockedStore := &retryDeadlineStore{
		Store: f.store, lookupStarted: make(chan struct{}, 1), lookupCanceled: make(chan struct{}, 1),
	}
	blockedStore.blockNextLookup.Store(true)
	restarted := NewPhysicalLocateService(blockedStore, NewClient())
	awaitBoundedRecoveryTimeout(t, restarted, blockedStore, f.controller, retryBudget, physicalLocateControllerTimeout)
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 1 {
		t.Fatalf("restart-recovery timeout lost durable target ownership: %+v", targets[f.controller])
	}
	if got := len(f.mock.posts()); got != 1 {
		t.Fatalf("timed-out restart recovery sent cleanup without database verification: posts=%d", got)
	}

	locateSessionTimeout = previousLocateTimeout
	restarted.store = f.store
	if err := restarted.StopDrawer(ctx, f.drawer2, 7); err != nil {
		t.Fatalf("complete cleanup after bounded restart recovery: %v", err)
	}
	if got := len(f.mock.posts()); got != 2 {
		t.Fatalf("restart recovery retry sent %d total writes, want activation plus cleanup", got)
	}
}

func TestPhysicalLocateSessionExpiryUsesBoundedRetry(t *testing.T) {
	previousLocateTimeout := locateSessionTimeout
	previousControllerTimeout := physicalLocateControllerTimeout
	previousRetryTimeout := physicalLocateCleanupRetryTimeout
	previousRetryDelay := physicalLocateCleanupRetryDelay
	locateSessionTimeout = time.Hour
	physicalLocateControllerTimeout = 750 * time.Millisecond
	retryBudget := 125 * time.Millisecond
	physicalLocateCleanupRetryTimeout = retryBudget
	physicalLocateCleanupRetryDelay = time.Hour
	defer func() {
		locateSessionTimeout = previousLocateTimeout
		physicalLocateControllerTimeout = previousControllerTimeout
		physicalLocateCleanupRetryTimeout = previousRetryTimeout
		physicalLocateCleanupRetryDelay = previousRetryDelay
	}()

	f := newPhysicalFixture(t)
	ctx := context.Background()
	session, err := f.service.LocateInventoryBin(ctx, f.partID, f.bin2, 7)
	if err != nil {
		t.Fatalf("activate Locate before expiry: %v", err)
	}
	blockedStore := &retryDeadlineStore{
		Store: f.store, lookupStarted: make(chan struct{}, 1), lookupCanceled: make(chan struct{}, 1),
	}
	blockedStore.blockNextLookup.Store(true)
	f.service.store = blockedStore
	physicalLocateCoordinator.Lock()
	f.service.retryMu.Lock()
	active := f.service.getSession(f.controller)
	if active == nil || active.timer == nil || !active.timer.Reset(time.Millisecond) {
		f.service.retryMu.Unlock()
		physicalLocateCoordinator.Unlock()
		t.Fatal("could not accelerate the normal session-expiry callback")
	}
	f.service.retryMu.Unlock()
	physicalLocateCoordinator.Unlock()
	awaitBoundedRecoveryTimeout(t, f.service, blockedStore, f.controller, retryBudget, physicalLocateControllerTimeout)
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 1 {
		t.Fatalf("session-expiry timeout lost durable target ownership: %+v", targets[f.controller])
	}
	if got := len(f.mock.posts()); got != 1 {
		t.Fatalf("timed-out expiry sent cleanup without database verification: posts=%d", got)
	}

	locateSessionTimeout = previousLocateTimeout
	f.service.store = f.store
	if err := f.service.StopDrawer(ctx, f.drawer2, 7); err != nil {
		t.Fatalf("complete cleanup after bounded expiry callback: %v", err)
	}
	if got := len(f.mock.posts()); got != 2 {
		t.Fatalf("expiry retry sent %d total writes, want activation plus cleanup", got)
	}
}

func TestPhysicalLocateAutomaticRetryDeadlineAndSerialization(t *testing.T) {
	previousControllerTimeout := physicalLocateControllerTimeout
	previousRetryTimeout := physicalLocateCleanupRetryTimeout
	previousRetryDelay := physicalLocateCleanupRetryDelay
	physicalLocateControllerTimeout = time.Second
	retryBudget := 150 * time.Millisecond
	physicalLocateCleanupRetryTimeout = retryBudget
	physicalLocateCleanupRetryDelay = 250 * time.Millisecond
	defer func() {
		physicalLocateControllerTimeout = previousControllerTimeout
		physicalLocateCleanupRetryTimeout = previousRetryTimeout
		physicalLocateCleanupRetryDelay = previousRetryDelay
	}()

	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, _ := addSecondaryPhysicalTarget(t, f, "bounded retry secondary")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7); err != nil {
		t.Fatalf("activate cross-controller Locate: %v", err)
	}

	blockedStore := &retryDeadlineStore{
		Store: f.store, lookupStarted: make(chan struct{}, 1), lookupCanceled: make(chan struct{}, 1),
	}
	blockedStore.blockNextLookup.Store(true)
	f.service.store = blockedStore
	session := f.service.getSession(f.controller)
	f.service.scheduleCleanupRetry(session)
	select {
	case <-blockedStore.lookupStarted:
	case <-time.After(time.Second):
		t.Fatal("automatic cleanup retry did not begin controller lookup")
	}
	startedAt := time.Now()

	// Give the competing attempt a shorter budget so it expires while waiting on
	// the existing coordinator lock rather than entering a second lookup.
	physicalLocateCleanupRetryTimeout = 25 * time.Millisecond
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- f.service.runScheduledCleanupRetry(f.controller, 7, session.ID)
	}()

	select {
	case <-blockedStore.lookupCanceled:
	case <-time.After(time.Second):
		t.Fatal("automatic retry did not cancel the stalled database lookup")
	}
	if err := <-secondDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("serialized second retry = %v, want its overall deadline", err)
	}
	if elapsed := time.Since(startedAt); elapsed > retryBudget+100*time.Millisecond {
		t.Fatalf("automatic retry exceeded its overall deadline: elapsed=%v budget=%v", elapsed, retryBudget)
	}
	if got := blockedStore.maxLookups.Load(); got != 1 {
		t.Fatalf("concurrent controller lookups=%d, want retries serialized at one", got)
	}

	physicalLocateCoordinator.Lock()
	f.service.retryMu.Lock()
	pending := f.service.getSession(f.controller)
	if pending == nil || pending.timer == nil || pending.LastOperation != "cleanup withheld because session ownership was not confirmed" {
		f.service.retryMu.Unlock()
		physicalLocateCoordinator.Unlock()
		t.Fatalf("timed-out retry did not retain a scheduled retry: %+v", pending)
	}
	pending.timer.Stop()
	pending.timer = nil
	f.service.retryMu.Unlock()
	physicalLocateCoordinator.Unlock()
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 1 || targets[secondaryID].MayHaveReceived != 1 {
		t.Fatalf("timeout lost durable target ownership: primary=%+v secondary=%+v", targets[f.controller], targets[secondaryID])
	}

	// Recovery from the durable active session after a process restart can safely
	// complete the cleanup once the database responds again.
	restarted := NewPhysicalLocateService(f.store, NewClient())
	if err := restarted.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("cleanup after restart following timed-out retry: %v", err)
	}
	recoveredTargets := durablePhysicalTargets(t, f, session.ID)
	if recoveredTargets[f.controller].MayHaveReceived != 0 || recoveredTargets[secondaryID].MayHaveReceived != 0 {
		t.Fatalf("restart did not persist cleanup resolution: primary=%+v secondary=%+v", recoveredTargets[f.controller], recoveredTargets[secondaryID])
	}
}

func TestPhysicalLocateAutomaticRetryCleansHealthyTargetWithinBudget(t *testing.T) {
	previousControllerTimeout := physicalLocateControllerTimeout
	previousRetryTimeout := physicalLocateCleanupRetryTimeout
	previousRetryDelay := physicalLocateCleanupRetryDelay
	physicalLocateControllerTimeout = 150 * time.Millisecond
	physicalLocateCleanupRetryTimeout = 2 * time.Second
	physicalLocateCleanupRetryDelay = time.Second
	defer func() {
		physicalLocateControllerTimeout = previousControllerTimeout
		physicalLocateCleanupRetryTimeout = previousRetryTimeout
		physicalLocateCleanupRetryDelay = previousRetryDelay
	}()

	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "healthy automatic retry target")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil || session.State != "active" {
		t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
	}
	f.client.httpClient.Timeout = 2 * time.Second
	f.mock.mu.Lock()
	f.mock.stallMethod = http.MethodGet
	f.mock.stallPath = "/json/info"
	f.mock.stallFor = time.Second
	f.mock.mu.Unlock()

	active := f.service.getSession(session.ControllerID)
	f.service.scheduleCleanupRetry(active)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(secondary.posts()) == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(secondary.posts()); got != 2 {
		t.Fatalf("healthy target was not cleaned during automatic retry: posts=%d want=2", got)
	}
	if got := len(f.mock.posts()); got != 1 {
		t.Fatalf("stalled target received cleanup without verification: posts=%d want=1", got)
	}
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 1 || targets[f.controller].LastError == "" {
		t.Errorf("stalled controller ownership/error not retained: %+v", targets[f.controller])
	}
	if targets[secondaryID].MayHaveReceived != 0 || targets[secondaryID].LastError != "" {
		t.Errorf("healthy controller was not durably resolved: %+v", targets[secondaryID])
	}

	physicalLocateCoordinator.Lock()
	f.service.retryMu.Lock()
	if current := f.service.getSession(session.ControllerID); current != nil && current.timer != nil {
		current.timer.Stop()
		current.timer = nil
	}
	f.service.retryMu.Unlock()
	physicalLocateCoordinator.Unlock()
	f.mock.mu.Lock()
	f.mock.stallMethod = ""
	f.mock.stallPath = ""
	f.mock.mu.Unlock()
	restarted := NewPhysicalLocateService(f.store, NewClient())
	if err := restarted.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("restart cleanup for unresolved automatic-retry target: %v", err)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 2 {
		t.Fatalf("restart did not target only unresolved controller: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
}

func TestPhysicalLocateThreeControllerPartialDeliveryAndRepeatedRecovery(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "three-controller middle target")
	tertiaryID, tertiary := addTertiaryPhysicalTarget(t, f, "three-controller final target")
	drawer, bin := makeThreeControllerDrawer(t, f)
	for _, controllerID := range []int64{secondaryID, tertiaryID} {
		if err := f.service.SaveControllerMappingVerification(ctx, controllerID, true); err != nil {
			t.Fatalf("verify controller #%d mapping: %v", controllerID, err)
		}
	}
	initial := [][][3]int{f.mock.pixels(), secondary.pixels(), tertiary.pixels()}
	secondary.dropResponsePostNumber = 1
	secondary.failPostNumber = 2

	session, locateErr := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if locateErr == nil || session.State != "partial" {
		t.Fatalf("three-controller partial delivery session=%+v err=%v, want unresolved partial state", session, locateErr)
	}
	if !strings.Contains(strings.ToLower(locateErr.Error()), "cleanup") && !strings.Contains(strings.ToLower(session.LastError), "cleanup") {
		t.Fatalf("partial-delivery error omitted unresolved cleanup detail: session=%+v err=%v", session, locateErr)
	}

	targets := durablePhysicalTargets(t, f, session.ID)
	if len(targets) != 3 || targets[f.controller].MayHaveReceived != 0 || targets[secondaryID].MayHaveReceived != 1 || targets[tertiaryID].MayHaveReceived != 0 {
		t.Fatalf("durable ownership after A-success/B-lost-response/C-not-reached and B cleanup failure = %+v", targets)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 2 || len(tertiary.posts()) != 0 {
		t.Fatalf("three-controller request counts A=%d B=%d C=%d; want A activation+cleanup, B activation+failed cleanup, C untouched", len(f.mock.posts()), len(secondary.posts()), len(tertiary.posts()))
	}
	for controllerIndex, posts := range [][]map[string]any{f.mock.posts(), secondary.posts(), tertiary.posts()} {
		for writeIndex, post := range posts {
			if _, exists := post["on"]; exists {
				t.Fatalf("controller %d write %d used controller-wide state: %#v", controllerIndex+1, writeIndex, post)
			}
		}
	}

	// Repeated Stop retries the original durable B ranges, not new topology-derived
	// ranges. It must not resend to A (already confirmed) or contact C (never tried).
	if err := f.service.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("repeated Stop did not settle the unresolved controller: %v", err)
	}
	if got := durablePhysicalTargets(t, f, session.ID); len(got) != 3 || got[secondaryID].MayHaveReceived != 0 || got[f.controller].MayHaveReceived != 0 || got[tertiaryID].MayHaveReceived != 0 {
		t.Fatalf("repeated Stop did not resolve exact persisted ownership: %+v", got)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 4 || len(tertiary.posts()) != 0 {
		t.Fatalf("repeated recovery expanded targets: A=%d B=%d C=%d", len(f.mock.posts()), len(secondary.posts()), len(tertiary.posts()))
	}

	finalFrames := [][][3]int{f.mock.pixels(), secondary.pixels(), tertiary.pixels()}
	for controllerIndex := range finalFrames {
		for pixel := range finalFrames[controllerIndex] {
			owned := false
			switch controllerIndex {
			case 0:
				owned = pixel >= 194 && pixel < 200
			case 1:
				owned = true
			case 2:
				owned = false // controller C was not reached after B's uncertain response
			}
			if owned {
				if finalFrames[controllerIndex][pixel] != [3]int{0, 0, 0} {
					t.Errorf("owned pixel controller %d:%d = %v, want black", controllerIndex+1, pixel, finalFrames[controllerIndex][pixel])
				}
			} else if finalFrames[controllerIndex][pixel] != initial[controllerIndex][pixel] {
				t.Errorf("unowned pixel changed at controller %d:%d: before=%v after=%v", controllerIndex+1, pixel, initial[controllerIndex][pixel], finalFrames[controllerIndex][pixel])
			}
		}
	}
}

func TestPhysicalLocateThreeControllerThirdFailsBeforeApplication(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "three-controller acknowledged middle target")
	tertiaryID, tertiary := addTertiaryPhysicalTarget(t, f, "three-controller pre-apply failure target")
	drawer, bin := makeThreeControllerDrawer(t, f)
	for _, controllerID := range []int64{secondaryID, tertiaryID} {
		if err := f.service.SaveControllerMappingVerification(ctx, controllerID, true); err != nil {
			t.Fatalf("verify controller #%d mapping: %v", controllerID, err)
		}
	}
	initial := [][][3]int{f.mock.pixels(), secondary.pixels(), tertiary.pixels()}
	tertiary.failBeforeApplyPostNumber = 1

	if _, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7); err == nil {
		t.Fatal("Locate succeeded although controller C failed before applying its activation")
	}
	if applied := tertiary.appliedPosts(); len(applied) != 2 || applied[0] || !applied[1] {
		t.Fatalf("controller C delivery simulation = %v, want failed activation then applied cleanup", applied)
	}

	sessions, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("three-controller session rows = %+v err=%v", sessions, err)
	}
	targets := durablePhysicalTargets(t, f, sessions[0].SessionID)
	if len(targets) != 3 || targets[f.controller].MayHaveReceived != 0 || targets[secondaryID].MayHaveReceived != 0 || targets[tertiaryID].MayHaveReceived != 0 {
		t.Fatalf("cleanup did not resolve each persisted delivery target: %+v", targets)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 4 || len(tertiary.posts()) != 2 {
		t.Fatalf("three-controller request counts A=%d B=%d C=%d; expected targeted activation/cleanup for A, B and C", len(f.mock.posts()), len(secondary.posts()), len(tertiary.posts()))
	}
	frames := [][][3]int{f.mock.pixels(), secondary.pixels(), tertiary.pixels()}
	for controllerIndex := range frames {
		for pixel := range frames[controllerIndex] {
			owned := controllerIndex == 1 || (controllerIndex == 0 && pixel >= 194) || (controllerIndex == 2 && pixel < 4)
			if owned {
				if frames[controllerIndex][pixel] != [3]int{0, 0, 0} {
					t.Errorf("owned pixel controller %d:%d = %v, want black", controllerIndex+1, pixel, frames[controllerIndex][pixel])
				}
			} else if frames[controllerIndex][pixel] != initial[controllerIndex][pixel] {
				t.Errorf("unrelated pixel controller %d:%d changed: before=%v after=%v", controllerIndex+1, pixel, initial[controllerIndex][pixel], frames[controllerIndex][pixel])
			}
		}
	}
	for controllerIndex, posts := range [][]map[string]any{f.mock.posts(), secondary.posts(), tertiary.posts()} {
		for writeIndex, post := range posts {
			if _, exists := post["on"]; exists {
				t.Fatalf("controller %d write %d used controller-wide state: %#v", controllerIndex+1, writeIndex, post)
			}
		}
	}
}

func TestPhysicalLocateCrossesControllersInOneDurableSession(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "secondary cross-controller")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	primaryBefore, secondaryBefore := len(f.mock.posts()), len(secondary.posts())

	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil {
		t.Fatalf("multi-controller Locate: %v", err)
	}
	if session.State != "active" || session.ControllerID != f.controller || !reflect.DeepEqual(session.AffectedControllerIDs, []int64{f.controller, secondaryID}) {
		t.Fatalf("logical session did not capture ordered controller targets: %+v", session)
	}
	primaryPosts, secondaryPosts := f.mock.posts()[primaryBefore:], secondary.posts()[secondaryBefore:]
	if len(primaryPosts) != 1 || len(secondaryPosts) != 1 {
		t.Fatalf("activation requests primary=%d secondary=%d, want one each", len(primaryPosts), len(secondaryPosts))
	}
	assertPixelCommand(t, primaryPosts[0], 20, 94, 100, []any{float64(128), float64(0), float64(0)})
	assertPixelCommand(t, secondaryPosts[0], 10, 0, 6, []any{float64(128), float64(0), float64(0)})
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, session.ID)
	if err != nil || len(targets) != 2 {
		t.Fatalf("durable target records=%+v err=%v, want two", targets, err)
	}
	for _, target := range targets {
		if target.MayHaveReceived != 1 || target.Fingerprint == "" || target.RangesJson == "[]" || target.CommandsJson == "[]" {
			t.Errorf("controller target lacks durable activation/cleanup state: %+v", target)
		}
	}
	restarted := NewPhysicalLocateService(f.store, NewClient())
	recovered, found, err := restarted.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !found || recovered.State != "active" || !reflect.DeepEqual(recovered.AffectedControllerIDs, []int64{f.controller, secondaryID}) {
		t.Fatalf("restart did not recover the full logical multi-controller session: %+v found=%v err=%v", recovered, found, err)
	}
	if err := restarted.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("multi-controller Stop after restart: %v", err)
	}
	if len(f.mock.posts()) != primaryBefore+2 || len(secondary.posts()) != secondaryBefore+2 {
		t.Fatalf("Stop did not target both controllers: primary=%d secondary=%d", len(f.mock.posts())-primaryBefore, len(secondary.posts())-secondaryBefore)
	}
	assertPixelCommand(t, f.mock.posts()[primaryBefore+1], 20, 94, 100, []any{float64(0), float64(0), float64(0)})
	assertPixelCommand(t, secondary.posts()[secondaryBefore+1], 10, 0, 6, []any{float64(0), float64(0), float64(0)})
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "stopped" {
		t.Fatalf("multi-controller session status=%+v found=%v err=%v", status, ok, err)
	}
}

func TestPhysicalLocatePartialActivationAttemptsCleanupOnEveryTarget(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "secondary partial failure")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	// Controller 1's second write is its failed cleanup attempt; controller 2's
	// first write is a failed activation, but its cleanup must still be attempted.
	f.mock.failPostNumber = 2
	secondary.failPostNumber = 1
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err == nil || session.State != "partial" {
		t.Fatalf("partial activation result session=%+v err=%v, want durable partial failure", session, err)
	}
	if !strings.Contains(session.LastError, "cleanup not confirmed") && !strings.Contains(session.LastError, "controller #") {
		t.Errorf("partial failure omitted actionable cleanup state: %+v", session)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 2 {
		t.Fatalf("cleanup did not attempt both target controllers: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	if err := f.service.WithoutActiveSession(ctx, func() error { return nil }); !errors.Is(err, ErrLocateSessionActive) {
		t.Fatalf("configuration write during partial multi-controller session = %v, want busy refusal", err)
	}

	f.mock.failPostNumber = 0
	secondary.failPostNumber = 0
	if err := f.service.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("retry cleanup for every attempted controller: %v", err)
	}
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "stopped" {
		t.Fatalf("retried session status=%+v found=%v err=%v", status, ok, err)
	}
	if len(f.mock.posts()) != 3 || len(secondary.posts()) != 2 {
		t.Fatalf("retry cleanup did not target only the unresolved controller: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
}

func TestPhysicalLocateCleansHealthySecondControllerWhenFirstIsUnreachable(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "healthy second controller")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil || session.State != "active" {
		t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
	}

	f.server.Close()
	if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
		t.Fatal("Stop should retain an error for the unreachable first controller")
	}
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "partial" {
		t.Fatalf("unreachable-first cleanup status=%+v found=%v err=%v", status, ok, err)
	}
	if len(f.mock.posts()) != 1 || len(secondary.posts()) != 2 {
		t.Fatalf("unreachable first controller blocked or received unsafe cleanup: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	if !strings.Contains(status.LastError, fmt.Sprintf("controller #%d", f.controller)) {
		t.Fatalf("session error does not identify unreachable controller: %q", status.LastError)
	}
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 1 || !strings.Contains(targets[f.controller].LastError, "cannot verify") {
		t.Errorf("unreachable cleanup target was not durably retained: %+v", targets[f.controller])
	}
	if targets[secondaryID].MayHaveReceived != 0 || targets[secondaryID].LastError != "" {
		t.Errorf("healthy controller cleanup was not durably resolved: %+v", targets[secondaryID])
	}

	if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
		t.Fatal("retry should continue reporting the unresolved first controller")
	}
	if len(secondary.posts()) != 2 {
		t.Fatalf("retry resent cleanup to the already-resolved second controller: %d writes", len(secondary.posts()))
	}
}

func TestPhysicalLocateCleanupTimeoutIsIsolatedPerController(t *testing.T) {
	previousTimeout := physicalLocateControllerTimeout
	physicalLocateControllerTimeout = 200 * time.Millisecond
	defer func() { physicalLocateControllerTimeout = previousTimeout }()

	for _, test := range []struct {
		name           string
		stallPath      string
		stallMethod    string
		reverseTargets bool
	}{
		{name: "verification-first", stallPath: "/json/info", stallMethod: http.MethodGet},
		{name: "verification-reversed", stallPath: "/json/info", stallMethod: http.MethodGet, reverseTargets: true},
		{name: "cleanup-first", stallPath: "/json/state", stallMethod: http.MethodPost},
		{name: "cleanup-reversed", stallPath: "/json/state", stallMethod: http.MethodPost, reverseTargets: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newPhysicalFixture(t)
			ctx := context.Background()
			secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "timeout isolation secondary")
			drawer, bin := makeCrossControllerDrawer(t, f)
			if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
				t.Fatal(err)
			}
			session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
			if err != nil || session.State != "active" {
				t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
			}

			active := f.service.getSession(session.ControllerID)
			if test.reverseTargets {
				active.targets[0], active.targets[1] = active.targets[1], active.targets[0]
				if err := f.service.persistSession(ctx, active); err != nil {
					t.Fatalf("persist reversed test target order: %v", err)
				}
			}
			stalledID := active.targets[0].ControllerID
			stalledMock := f.mock
			healthyMock := secondary
			if stalledID == secondaryID {
				stalledMock, healthyMock = secondary, f.mock
			}
			stalledMock.mu.Lock()
			stalledMock.stallMethod = test.stallMethod
			stalledMock.stallPath = test.stallPath
			stalledMock.stallFor = time.Second
			stalledMock.mu.Unlock()
			f.client.httpClient.Timeout = 2 * time.Second

			healthyID := f.controller
			if stalledID == f.controller {
				healthyID = secondaryID
			}
			var healthyCommand physicalPixelCommand
			for _, target := range active.targets {
				if target.ControllerID == healthyID {
					healthyCommand = target.Commands[0]
					break
				}
			}
			beforeHealthyPosts := len(healthyMock.posts())
			if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
				t.Fatal("Stop should retain the stalled controller as unresolved")
			}
			status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
			if err != nil || !ok || status.State != "partial" {
				t.Fatalf("stalled-controller cleanup status=%+v found=%v err=%v", status, ok, err)
			}
			if got := len(healthyMock.posts()); got != beforeHealthyPosts+1 {
				t.Fatalf("healthy controller did not receive targeted cleanup after the stalled target: posts=%d want=%d", got, beforeHealthyPosts+1)
			}
			assertPixelCommand(t, healthyMock.posts()[beforeHealthyPosts], healthyCommand.SegmentID, int(healthyCommand.Start), int(healthyCommand.Stop), []any{float64(0), float64(0), float64(0)})
			targets := durablePhysicalTargets(t, f, session.ID)
			if targets[stalledID].MayHaveReceived != 1 || targets[stalledID].LastError == "" {
				t.Errorf("stalled target ownership/error was not retained: %+v", targets[stalledID])
			}
			if targets[healthyID].MayHaveReceived != 0 || targets[healthyID].LastError != "" {
				t.Errorf("healthy target was not durably resolved: %+v", targets[healthyID])
			}

			stalledMock.mu.Lock()
			stalledMock.stallMethod = ""
			stalledMock.stallPath = ""
			stalledMock.mu.Unlock()
			restarted := NewPhysicalLocateService(f.store, NewClient())
			if err := restarted.StopDrawer(ctx, drawer, 7); err != nil {
				t.Fatalf("restart cleanup for unresolved controller: %v", err)
			}
			if got := len(healthyMock.posts()); got != beforeHealthyPosts+1 {
				t.Errorf("restart retried an already-resolved healthy target: posts=%d", got)
			}
			if got := len(stalledMock.posts()); got != 2 {
				t.Errorf("restart did not retry only the stalled target: posts=%d want=2", got)
			}
		})
	}
}

func TestPhysicalLocateCleanupCancellationPreservesOwnership(t *testing.T) {
	previousTimeout := physicalLocateControllerTimeout
	physicalLocateControllerTimeout = time.Second
	defer func() { physicalLocateControllerTimeout = previousTimeout }()

	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "cancel cleanup secondary")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil || session.State != "active" {
		t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
	}
	f.mock.mu.Lock()
	f.mock.stallMethod = http.MethodGet
	f.mock.stallPath = "/json/info"
	f.mock.stallFor = time.Second
	f.mock.mu.Unlock()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	startRequest := f.mock.requestCount()
	stopDone := make(chan error, 1)
	go func() { stopDone <- f.service.StopDrawer(cancelCtx, drawer, 7) }()
	deadline := time.Now().Add(time.Second)
	reachedStall := false
	for time.Now().Before(deadline) {
		if containsString(f.mock.requestsSince(startRequest), "GET /json/info") {
			cancel()
			reachedStall = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !reachedStall {
		cancel()
		t.Fatal("cleanup verification did not reach the stalled controller")
	}
	if err := <-stopDone; err == nil {
		t.Fatal("canceled Stop should return an error")
	}
	if len(f.mock.posts()) != 1 || len(secondary.posts()) != 1 {
		t.Fatalf("cancellation sent a range-off command: primary posts=%d secondary posts=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 1 || targets[secondaryID].MayHaveReceived != 1 {
		t.Fatalf("canceled cleanup lost durable ownership: primary=%+v secondary=%+v", targets[f.controller], targets[secondaryID])
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestPhysicalLocateCleansFirstControllerWhenSecondIsUnverifiable(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "unverifiable second controller")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil || session.State != "active" {
		t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
	}
	secondary.mu.Lock()
	secondary.offline = true
	secondary.mu.Unlock()

	if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
		t.Fatal("Stop should retain an error for the unverifiable second controller")
	}
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "partial" {
		t.Fatalf("unverifiable-second cleanup status=%+v found=%v err=%v", status, ok, err)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 1 {
		t.Fatalf("second-controller verification failure did not isolate targeted cleanup: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	if !strings.Contains(status.LastError, fmt.Sprintf("controller #%d", secondaryID)) {
		t.Fatalf("session error does not identify unverifiable controller: %q", status.LastError)
	}
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 0 || targets[f.controller].LastError != "" {
		t.Errorf("healthy first controller cleanup was not durably resolved: %+v", targets[f.controller])
	}
	if targets[secondaryID].MayHaveReceived != 1 || !strings.Contains(targets[secondaryID].LastError, "cannot verify") {
		t.Errorf("unverifiable second target was not durably retained: %+v", targets[secondaryID])
	}
}

func TestPhysicalLocateRetriesOnlyControllerWithPartialCleanupFailure(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "partial cleanup failure")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil || session.State != "active" {
		t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
	}
	secondary.failPostNumber = 2 // fail only the second controller's cleanup write

	if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
		t.Fatal("Stop should report the failed targeted cleanup")
	}
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "partial" {
		t.Fatalf("partial-cleanup status=%+v found=%v err=%v", status, ok, err)
	}
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 0 || targets[f.controller].LastError != "" {
		t.Errorf("successful first controller cleanup was not durably resolved: %+v", targets[f.controller])
	}
	if targets[secondaryID].MayHaveReceived != 1 || !strings.Contains(targets[secondaryID].LastError, "targeted cleanup failed") {
		t.Errorf("failed second controller cleanup was not retained with its error: %+v", targets[secondaryID])
	}
	if !strings.Contains(status.LastError, fmt.Sprintf("controller #%d", secondaryID)) {
		t.Errorf("session error does not identify the failed controller: %q", status.LastError)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 2 {
		t.Fatalf("initial partial cleanup writes primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}

	secondary.failPostNumber = 0
	if err := f.service.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("retry unresolved second-controller cleanup: %v", err)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 3 {
		t.Fatalf("retry did not target only unresolved controller: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	status, ok, err = f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "stopped" {
		t.Fatalf("retried cleanup status=%+v found=%v err=%v", status, ok, err)
	}
}
