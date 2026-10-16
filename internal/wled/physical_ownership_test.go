package wled

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"reflect"
)

func startOwnership(t *testing.T, service *PhysicalLocateService, controller int64, ranges []ControllerRange) PhysicalLocateSession {
	t.Helper()
	session, err := service.StartRanges(context.Background(), controller, ranges, 7, "test-centre", "#00FF00", 100, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return session
}
func stopOwnershipTimers(service *PhysicalLocateService) {
	physicalLocateCoordinator.Lock()
	defer physicalLocateCoordinator.Unlock()
	service.retryMu.Lock()
	defer service.retryMu.Unlock()
	for _, s := range service.sessions {
		if s.timer != nil {
			s.timer.Stop()
			s.timer = nil
		}
	}
}

func TestPhysicalOwnershipDisjointStopRestartAndReservations(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	first := startOwnership(t, f.service, f.controller, []ControllerRange{{Start: 5, Count: 2}, {Start: 105, Count: 3}})
	second := startOwnership(t, f.service, f.controller, []ControllerRange{{Start: 20, Count: 4}})
	if first.DrawerID != 0 || first.BinID != 0 {
		t.Fatal("generic operation acquired inventory identity")
	}
	before := f.mock.pixels()
	stopOwnershipTimers(f.service)
	store2, _ := secondPhysicalStore(t, f)
	restarted := NewPhysicalLocateService(store2, NewClient())
	defer stopOwnershipTimers(restarted)
	operations, err := restarted.Operations(ctx)
	if err != nil || len(operations) != 2 {
		t.Fatalf("recovered operations=%+v err=%v", operations, err)
	}
	for _, op := range operations {
		if op.Function != "test-centre" || op.Duration != time.Hour || op.ExpiresAt.IsZero() {
			t.Fatalf("lost metadata: %+v", op)
		}
	}
	if err := restarted.StopOperation(ctx, first.ID, 8); !errors.Is(err, ErrLocateSessionOwned) {
		t.Fatalf("foreign stop=%v", err)
	}
	if err := restarted.StopOperation(ctx, first.ID, 7); err != nil {
		t.Fatal(err)
	}
	after := f.mock.pixels()
	for i := 19; i < 23; i++ {
		if after[i] != before[i] {
			t.Fatalf("stopping first changed second pixel %d", i)
		}
	}
	for _, i := range []int{4, 5, 104, 105, 106} {
		if after[i] != [3]int{} {
			t.Fatalf("owned pixel %d not off: %v", i, after[i])
		}
	}
	idGate := controllerIDGate(f.controller)
	idGate.mu.Lock()
	reserved := idGate.reserved
	idGate.mu.Unlock()
	endpoint := writeGate(second.ControllerAddress, second.ControllerPort)
	endpoint.mu.Lock()
	endpointReserved := endpoint.reserved
	endpoint.mu.Unlock()
	if !reserved || !endpointReserved {
		t.Fatal("stopping one operation unreserved another owner")
	}
	if owned, err := db.HasControllerPhysicalLocateOwnership(ctx, store2, f.controller); err != nil || !owned {
		t.Fatalf("configuration ownership=%v err=%v", owned, err)
	}
	if err := restarted.StopOperation(ctx, first.ID, 7); err != nil {
		t.Fatal(err)
	}
	if err := restarted.StopOperation(ctx, second.ID, 7); err != nil {
		t.Fatal(err)
	}
	operations, err = restarted.Operations(ctx)
	if err != nil || len(operations) != 0 {
		t.Fatalf("remaining=%+v err=%v", operations, err)
	}
	rows, err := store2.ListPhysicalLocateSessions(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("history lost: %+v err=%v", rows, err)
	}
	for _, row := range rows {
		targets, err := store2.ListPhysicalLocateSessionTargets(ctx, row.SessionID)
		if err != nil || len(targets) != 1 {
			t.Fatalf("lost targets for %s: %v", row.SessionID, err)
		}
	}
}

func TestPhysicalOwnershipPhysicalAliasAndLocateConflict(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	alias, err := f.store.CreateController(ctx, db.CreateControllerParams{Name: "physical alias", IpAddress: "localhost", Port: sql.NullInt64{Int64: controllerPortMust(t, f), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{ID: alias.ID, LedCount: 200}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.SaveControllerMappingVerification(ctx, alias.ID, true); err != nil {
		t.Fatal(err)
	}
	locate, err := f.service.LocateDrawerBin(ctx, f.drawer2, f.bin2, 7)
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.mock.posts())
	_, err = f.service.StartRanges(ctx, alias.ID, []ControllerRange{{Start: 10, Count: 1}}, 8, "test-centre", "#0000FF", 128, time.Minute)
	if !errors.Is(err, ErrLocateSessionActive) || !errors.Is(err, ErrPhysicalOwnershipConflict) {
		t.Fatalf("alias overlap=%v", err)
	}
	if len(f.mock.posts()) != before {
		t.Fatal("conflict silently overwrote Locate")
	}
	if active := f.service.sessionByID(locate.ID); active == nil || active.State != "active" {
		t.Fatal("Locate priority was lost")
	}
	disjoint := startOwnership(t, f.service, alias.ID, []ControllerRange{{Start: 30, Count: 1}})
	if err := f.service.StopOperation(ctx, disjoint.ID, 7); err != nil {
		t.Fatal(err)
	}
	if err := f.service.StopDrawer(ctx, f.drawer2, 7); err != nil {
		t.Fatal(err)
	}
}
func controllerPortMust(t *testing.T, f *physicalFixture) int64 {
	t.Helper()
	c, err := f.store.GetController(context.Background(), f.controller)
	if err != nil {
		t.Fatal(err)
	}
	return controllerPort(c)
}

func TestPhysicalOwnershipEmergencyOffIndependentFailures(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondary, mock2 := addSecondaryPhysicalTarget(t, f, "secondary")
	if err := f.service.SaveControllerMappingVerification(ctx, secondary, true); err != nil {
		t.Fatal(err)
	}
	first := startOwnership(t, f.service, f.controller, []ControllerRange{{Start: 5, Count: 1}})
	second := startOwnership(t, f.service, secondary, []ControllerRange{{Start: 25, Count: 1}})
	// The first controller rejects the off. The second must still be attempted.
	f.mock.mu.Lock()
	f.mock.failBeforeApplyPostNumber = f.mock.postCount + 1
	f.mock.failPostNumber = f.mock.postCount + 1
	f.mock.failPostStatus = 503
	f.mock.mu.Unlock()
	results, err := f.service.EmergencyOff(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("off results=%+v", results)
	}
	byID := map[int64]ControllerOffResult{}
	for _, r := range results {
		byID[r.ControllerID] = r
	}
	if byID[f.controller].Acknowledged || byID[f.controller].Error == "" || !byID[secondary].Acknowledged {
		t.Fatalf("independent results=%+v", results)
	}
	posts := mock2.posts()
	if len(posts) != 2 || posts[1]["on"] != false {
		t.Fatalf("secondary did not receive off: %+v", posts)
	}
	operations, err := f.service.Operations(ctx)
	if err != nil || len(operations) != 1 || operations[0].ID != first.ID {
		t.Fatalf("unresolved=%+v err=%v", operations, err)
	}
	targets := durablePhysicalTargets(t, f, first.ID)
	if targets[f.controller].MayHaveReceived != 1 {
		t.Fatal("failed off released ownership")
	}
	targets = durablePhysicalTargets(t, f, second.ID)
	if targets[secondary].MayHaveReceived != 0 {
		t.Fatal("successful off retained attempted ownership")
	}
	if _, err := f.service.StartRanges(ctx, f.controller, []ControllerRange{{Start: 5, Count: 1}}, 7, "test-centre", "#FFFFFF", 50, time.Minute); !errors.Is(err, ErrLocateSessionActive) {
		t.Fatalf("failed off overlap=%v", err)
	}
	// Even the healthy controller is globally blocked until emergency retry.
	if _, err := f.service.StartRanges(ctx, secondary, []ControllerRange{{Start: 50, Count: 1}}, 7, "test-centre", "#FFFFFF", 50, time.Minute); !errors.Is(err, ErrLocateSessionActive) {
		t.Fatalf("partial emergency allowed a disjoint healthy-controller start: %v", err)
	}
	if _, err := f.service.EmergencyOff(ctx); err != nil {
		t.Fatal(err)
	}
}

// A separate executable has no access to process-local mutexes/reservations.
func TestPhysicalOwnershipProcessHelper(t *testing.T) {
	path := os.Getenv("WLEDGER_OWNERSHIP_TEST_DB")
	if path == "" {
		return
	}
	database, err := db.Open("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	controller, _ := strconv.ParseInt(os.Getenv("WLEDGER_OWNERSHIP_TEST_CONTROLLER"), 10, 64)
	start, _ := strconv.ParseInt(os.Getenv("WLEDGER_OWNERSHIP_TEST_START"), 10, 64)
	service := NewPhysicalLocateService(db.NewStore(database), NewClient())
	defer stopOwnershipTimers(service)
	_, err = service.StartRanges(context.Background(), controller, []ControllerRange{{Start: start, Count: 3}}, 7, "process-test", "#0000FF", 90, time.Hour)
	if os.Getenv("WLEDGER_OWNERSHIP_EXPECT_CONFLICT") == "1" {
		if !errors.Is(err, ErrLocateSessionActive) {
			t.Fatalf("expected durable conflict, got %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
}
func TestPhysicalOwnershipCrossProcessClaimsAndRecovery(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	first := startOwnership(t, f.service, f.controller, []ControllerRange{{Start: 5, Count: 3}})
	var seq int
	var name, path string
	if err := f.database.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		start    int
		conflict bool
	}{{6, true}, {30, false}} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestPhysicalOwnershipProcessHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), "WLEDGER_OWNERSHIP_TEST_DB="+path, fmt.Sprintf("WLEDGER_OWNERSHIP_TEST_CONTROLLER=%d", f.controller), fmt.Sprintf("WLEDGER_OWNERSHIP_TEST_START=%d", tc.start), fmt.Sprintf("WLEDGER_OWNERSHIP_EXPECT_CONFLICT=%d", map[bool]int{true: 1, false: 0}[tc.conflict]))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("subprocess=%v\n%s", err, output)
		}
	}
	operations, err := f.service.Operations(ctx)
	if err != nil || len(operations) != 2 {
		t.Fatalf("process durable operations=%+v err=%v", operations, err)
	}
	if err := f.service.StopOperation(ctx, first.ID, 7); err != nil {
		t.Fatal(err)
	}
	operations, err = f.service.Operations(ctx)
	if err != nil || len(operations) != 1 || operations[0].Function != "process-test" {
		t.Fatalf("targeted stop crossed processes: %+v err=%v", operations, err)
	}
	if err := f.service.StopOperation(ctx, operations[0].ID, 7); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalOwnershipEmergencyFenceSurvivesInterruptedOutput(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	operation := startOwnership(t, f.service, f.controller, []ControllerRange{{Start: 5, Count: 2}})
	stale := snapshotSessionForStaleWriter(f.service.sessionByID(operation.ID), "activating", true)
	// Reproduce restart after committing cancellation but before off delivery.
	if err := db.WithLEDOperationClaim(ctx, f.store, func(q db.LEDQuerier) error { return q.FencePhysicalOperations(ctx) }); err != nil {
		t.Fatal(err)
	}
	before := len(f.mock.posts())
	controller, err := f.store.GetController(ctx, f.controller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.withPhysicalCommandClaim(ctx, operation.ID, &stale.targets[0], []string{"activating"}, controller, []int{1, 2, 3}, false); err == nil {
		t.Fatal("emergency fence allowed stale activation")
	}
	if err := f.service.persistSession(ctx, stale); err == nil {
		t.Fatal("emergency fence allowed stale reacquisition")
	}
	if _, err := f.service.StartRanges(ctx, f.controller, []ControllerRange{{Start: 30, Count: 1}}, 7, "test-centre", "#FFFFFF", 50, time.Minute); !errors.Is(err, ErrLocateSessionActive) || !strings.Contains(err.Error(), "emergency") {
		t.Fatalf("barrier allowed reservation: %v", err)
	}
	if len(f.mock.posts()) != before {
		t.Fatal("revoked operations wrote hardware")
	}
	restarted := NewPhysicalLocateService(f.store, NewClient())
	defer stopOwnershipTimers(restarted)
	if _, err := restarted.EmergencyOff(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.PhysicalEmergencyPending(ctx)
	if err != nil || pending {
		t.Fatalf("barrier not resolved: %v %v", pending, err)
	}
}

func TestPhysicalOwnershipMigrationPreservesLegacyRecoveryEvidence(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	if err := goose.DownTo(f.database, ".", 24); err != nil {
		t.Fatal(err)
	}
	ranges := `[{"start":5,"count":2},{"start":105,"count":3}]`
	commands := `[{"segment_id":10,"start":4,"stop":6},{"segment_id":20,"start":4,"stop":7}]`
	if _, err := f.store.SavePhysicalLocateSession(ctx, db.SavePhysicalLocateSessionParams{
		ControllerID: f.controller, ControllerAddress: "127.0.0.1", ControllerPort: controllerPortMust(t, f), SessionID: "legacy-recovery", DrawerID: f.drawer1, BinID: f.bin1, OwnerID: 7, State: "partial", RangesJson: ranges, CommandsJson: commands, Fingerprint: "legacy-fingerprint", Color: "#FFFFFF", Brightness: 128, LastOperation: "unresolved legacy cleanup", LastOperationAt: time.Now().UTC(), LastError: "controller unavailable",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SavePhysicalLocateSessionTarget(ctx, db.SavePhysicalLocateSessionTargetParams{SessionID: "legacy-recovery", ControllerID: f.controller, ControllerAddress: "127.0.0.1", ControllerPort: controllerPortMust(t, f), RangesJson: ranges, CommandsJson: commands, Fingerprint: "legacy-fingerprint", MayHaveReceived: 1, LastError: "controller unavailable"}); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetsBefore, err := f.store.ListPhysicalLocateSessionTargets(ctx, "legacy-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(f.database); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetsAfter, err := f.store.ListPhysicalLocateSessionTargets(ctx, "legacy-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(targetsBefore, targetsAfter) {
		t.Fatalf("migration changed legacy evidence: before=%+v after=%+v targets=%+v/%+v", before, after, targetsBefore, targetsAfter)
	}
	rows, err := f.database.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration left a broken foreign key")
	}
	if _, err := f.service.StartRanges(ctx, f.controller, []ControllerRange{{Start: 30, Count: 2}}, 7, "test-centre", "#FFFFFF", 90, time.Hour); !errors.Is(err, ErrLocateSessionActive) {
		t.Fatalf("unidentified legacy ownership was ignored: %v", err)
	}
	if len(f.mock.posts()) != 0 {
		t.Fatal("legacy migration check wrote hardware")
	}
}

func TestPhysicalOwnershipNewAPIsRespectDisabledOutput(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	operation := startOwnership(t, f.service, f.controller, []ControllerRange{{Start: 5, Count: 2}})
	before := len(f.mock.posts())
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "false")
	if _, err := f.service.StartRanges(ctx, f.controller, []ControllerRange{{Start: 30, Count: 2}}, 7, "test-centre", "#FFFFFF", 90, time.Hour); !errors.Is(err, ErrPhysicalOutputDisabled) {
		t.Fatalf("disabled start=%v", err)
	}
	if err := f.service.StopOperation(ctx, operation.ID, 7); err != nil {
		t.Fatalf("verified persisted cleanup must remain available: %v", err)
	}
	if _, err := f.service.EmergencyOff(ctx); !errors.Is(err, ErrPhysicalOutputDisabled) {
		t.Fatalf("disabled emergency=%v", err)
	}
	// Disabling new output must not strand acknowledged, saved targeted cleanup.
	// Neither the explicit Stop nor a repeated recovery may power the device on.
	if err := f.service.runScheduledCleanupRetry(f.controller, 7, operation.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.mock.posts()) != before+1 {
		t.Fatal("cleanup must send exactly the recorded targeted range-off")
	}
	for _, post := range f.mock.posts()[before:] {
		if _, exists := post["on"]; exists {
			t.Fatal("cleanup changed global power")
		}
	}
	targets := durablePhysicalTargets(t, f, operation.ID)
	if targets[f.controller].MayHaveReceived != 0 {
		t.Fatal("acknowledged cleanup did not release ownership")
	}
}

// The caller may disappear immediately after the emergency was accepted. Its
// cancellation and the first endpoint's timeout must not cancel the second one.
func TestPhysicalOwnershipEmergencyCancellationIndependentDeliveries(t *testing.T) {
	f := newPhysicalFixture(t)
	secondary, mock2 := addSecondaryPhysicalTarget(t, f, "healthy second")
	if _, err := f.database.Exec(`UPDATE controllers SET connection_timeout_ms=100 WHERE id=?`, f.controller); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	f.mock.mu.Lock()
	f.mock.postEntered = entered
	f.mock.postRelease = release
	f.mock.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type response struct {
		results []ControllerOffResult
		err     error
	}
	done := make(chan response, 1)
	go func() { r, err := f.service.EmergencyOff(ctx); done <- response{r, err} }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first off never entered")
	}
	cancel()
	var outcome response
	select {
	case outcome = <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("emergency did not continue after request cancellation")
	}
	close(release)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if len(outcome.results) != 2 || !outcome.results[0].Attempted || outcome.results[0].Acknowledged || !outcome.results[1].Acknowledged {
		t.Fatalf("outcomes=%+v", outcome.results)
	}
	if len(mock2.posts()) != 1 {
		t.Fatal("healthy second controller was not attempted")
	}
	store2, _ := secondPhysicalStore(t, f)
	restarted := NewPhysicalLocateService(store2, NewClient())
	defer stopOwnershipTimers(restarted)
	status, err := restarted.EmergencyStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 2 || status[0].Acknowledged || status[0].Error == "" || status[1].ControllerID != secondary || !status[1].Acknowledged {
		t.Fatalf("acknowledgement did not survive independently: %+v", status)
	}
	pending, err := store2.PhysicalEmergencyPending(context.Background())
	if err != nil || !pending {
		t.Fatalf("failed off did not retain global barrier: %v %v", pending, err)
	}
}

type emergencyOutcomeFailStore struct {
	db.Store
	controllerID int64
	failure      error
	attemptOnly  bool
}
type emergencyOutcomeFailQuerier struct {
	db.LEDQuerier
	controllerID int64
	failure      error
	attemptOnly  bool
}

func (s emergencyOutcomeFailStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	return s.Store.ExecImmediateTx(ctx, func(q db.Querier) error {
		led, err := db.RequireLEDQuerier(q)
		if err != nil {
			return err
		}
		return fn(emergencyOutcomeFailQuerier{led, s.controllerID, s.failure, s.attemptOnly})
	})
}
func (q emergencyOutcomeFailQuerier) SavePhysicalEmergencyOutcome(ctx context.Context, r db.PhysicalEmergencyOutcome) error {
	if r.ControllerID == q.controllerID && r.Attempted && (r.Acknowledged != q.attemptOnly) {
		return q.failure
	}
	return q.LEDQuerier.SavePhysicalEmergencyOutcome(ctx, r)
}
func TestPhysicalOwnershipEmergencyPersistenceErrorDoesNotAbortLaterDeliveries(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondary, mock2 := addSecondaryPhysicalTarget(t, f, "healthy second")
	injected := errors.New("simulated emergency outcome persistence failure")
	service := NewPhysicalLocateService(emergencyOutcomeFailStore{f.store, f.controller, injected, false}, NewClient())
	defer stopOwnershipTimers(service)
	results, err := service.EmergencyOff(ctx)
	if !errors.Is(err, injected) {
		t.Fatalf("infrastructure error not aggregated: %v", err)
	}
	if len(results) != 2 || results[0].Acknowledged || !strings.Contains(results[0].Error, "persistence") || !results[1].Acknowledged {
		t.Fatalf("results=%+v", results)
	}
	if len(f.mock.posts()) != 1 || len(mock2.posts()) != 1 {
		t.Fatal("persistence failure suppressed an off attempt")
	}
	status, err := f.service.EmergencyStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 2 || status[0].Acknowledged || !status[0].Attempted || status[1].ControllerID != secondary || !status[1].Acknowledged {
		t.Fatalf("independent durable status=%+v", status)
	}
	pending, err := f.store.PhysicalEmergencyPending(ctx)
	if err != nil || !pending {
		t.Fatalf("persistence failure cleared barrier: %v %v", pending, err)
	}
	if _, err := f.service.EmergencyOff(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err = f.store.PhysicalEmergencyPending(ctx)
	if err != nil || pending {
		t.Fatalf("retry did not resolve barrier: %v %v", pending, err)
	}
}

func TestPhysicalOwnershipEmergencyFailureWithoutPriorOperation(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	f.mock.mu.Lock()
	f.mock.failPostNumber = 1
	f.mock.failPostStatus = 503
	f.mock.mu.Unlock()
	results, err := f.service.EmergencyOff(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Attempted || results[0].Acknowledged || results[0].Address == "" || results[0].Port == 0 {
		t.Fatalf("outcomes=%+v", results)
	}
	sessions, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("test unexpectedly owns inventory session: %+v %v", sessions, err)
	}
	pending, err := f.store.PhysicalEmergencyPending(ctx)
	if err != nil || !pending {
		t.Fatalf("failure without session cleared barrier: %v %v", pending, err)
	}
	restarted := NewPhysicalLocateService(f.store, NewClient())
	defer stopOwnershipTimers(restarted)
	status, err := restarted.EmergencyStatus(ctx)
	if err != nil || !reflect.DeepEqual(results, status) {
		t.Fatalf("failed no-session off lost across restart: %+v %v", status, err)
	}
	if err := goose.DownTo(f.database, ".", 26); err == nil {
		t.Fatal("027 rollback discarded an emergency delivery failure")
	}
	statusAfterRollback, err := restarted.EmergencyStatus(ctx)
	if err != nil || !reflect.DeepEqual(status, statusAfterRollback) {
		t.Fatalf("failed 027 rollback changed outcomes: %+v %v", statusAfterRollback, err)
	}
	if _, err := restarted.StartRanges(ctx, f.controller, []ControllerRange{{Start: 50, Count: 1}}, 7, "test-centre", "#FFFFFF", 50, time.Minute); !errors.Is(err, ErrLocateSessionActive) {
		t.Fatalf("global barrier allowed new start: %v", err)
	}
	if _, err := restarted.EmergencyOff(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err = f.store.PhysicalEmergencyPending(ctx)
	if err != nil || pending {
		t.Fatalf("successful retry did not clear barrier: %v %v", pending, err)
	}
}

func TestPhysicalOwnershipEmergencyReportsHistoricalEndpointsWithoutSending(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%v", missing), func(t *testing.T) {
			f := newPhysicalFixture(t)
			ctx := context.Background()
			operation := startOwnership(t, f.service, f.controller, []ControllerRange{{Start: 5, Count: 1}})
			before := len(f.mock.posts())
			if missing {
				// Simulate an already-corrupt/restored configuration in this disposable DB.
				// Missing configuration must never erase independent cleanup evidence.
				conn, err := f.database.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
					t.Fatal(err)
				}
				_, deleteErr := conn.ExecContext(ctx, `DELETE FROM controllers WHERE id=?`, f.controller)
				_, restoreErr := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`)
				conn.Close()
				if deleteErr != nil || restoreErr != nil {
					t.Fatalf("simulate missing controller: %v %v", deleteErr, restoreErr)
				}
			} else {
				secondary, _ := addSecondaryPhysicalTarget(t, f, "new configured endpoint")
				current, err := f.store.GetController(ctx, secondary)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.database.ExecContext(ctx, `UPDATE controllers SET ip_address=?,port=? WHERE id=?`, current.IpAddress, controllerPort(current), f.controller); err != nil {
					t.Fatal(err)
				}
			}
			results, err := f.service.EmergencyOff(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var historical *ControllerOffResult
			for i := range results {
				r := &results[i]
				if r.ControllerID == f.controller && r.Address == operation.ControllerAddress && r.Port == operation.ControllerPort {
					historical = r
				}
			}
			if historical == nil || historical.Attempted || historical.Acknowledged || !strings.Contains(historical.Error, "manual identity") {
				t.Fatalf("missing unattempted historical endpoint: %+v", results)
			}
			if len(f.mock.posts()) != before {
				t.Fatal("sent emergency off to a historical endpoint")
			}
			targets := durablePhysicalTargets(t, f, operation.ID)
			if targets[f.controller].MayHaveReceived != 1 {
				t.Fatal("historical ownership evidence discarded")
			}
			status, err := f.service.EmergencyStatus(ctx)
			if err != nil || !reflect.DeepEqual(results, status) {
				t.Fatalf("historical endpoint not exposed durably: %+v %v", status, err)
			}
			// A retry cannot silently forget a stale endpoint or command it.
			if _, err := f.service.EmergencyOff(ctx); err != nil {
				t.Fatal(err)
			}
			if len(f.mock.posts()) != before {
				t.Fatal("retry sent off to historical endpoint")
			}
			pending, err := f.store.PhysicalEmergencyPending(ctx)
			if err != nil || !pending {
				t.Fatalf("unresolved historical endpoint cleared barrier: %v %v", pending, err)
			}
		})
	}
}

func TestPhysicalOwnershipMigrationRollbackRefusalPreservesEvidenceAndFKs(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	operation := startOwnership(t, f.service, f.controller, []ControllerRange{{Start: 5, Count: 1}, {Start: 105, Count: 2}})
	before, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetsBefore, err := f.store.ListPhysicalLocateSessionTargets(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Empty 027/026 can roll back; populated 025 must refuse, before changing FKs.
	if err := goose.DownTo(f.database, ".", 24); err == nil {
		t.Fatal("025 rollback discarded ownership")
	}
	after, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	targetsAfter, err := f.store.ListPhysicalLocateSessionTargets(ctx, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(targetsBefore, targetsAfter) {
		t.Fatal("failed rollback changed recovery evidence")
	}
	var foreignKeys int
	if err := f.database.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("failed rollback changed FK enforcement: %d %v", foreignKeys, err)
	}
	rows, err := f.database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("failed rollback left invalid FKs")
	}
	rows.Close()
	// Restore the additive tables for normal fixture teardown and later tests.
	if err := db.Migrate(f.database); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalOwnershipEmergencyAttemptMarkerFailureDoesNotSuppressOff(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	_, mock2 := addSecondaryPhysicalTarget(t, f, "healthy second")
	injected := errors.New("simulated attempt marker write failure")
	service := NewPhysicalLocateService(emergencyOutcomeFailStore{f.store, f.controller, injected, true}, NewClient())
	defer stopOwnershipTimers(service)
	results, err := service.EmergencyOff(ctx)
	if !errors.Is(err, injected) {
		t.Fatalf("attempt marker infrastructure error missing: %v", err)
	}
	if len(results) != 2 || !results[0].Acknowledged || !results[1].Acknowledged || len(f.mock.posts()) != 1 || len(mock2.posts()) != 1 {
		t.Fatalf("attempt marker failure suppressed an off: %+v", results)
	}
	status, err := service.EmergencyStatus(ctx)
	if err != nil || !reflect.DeepEqual(results, status) {
		t.Fatalf("final durable acknowledgements did not heal pending markers: %+v %v", status, err)
	}
}
