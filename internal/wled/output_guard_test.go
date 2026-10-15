package wled

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// All integration fixtures in this package use loopback httptest controllers;
// enable output for those mock-only tests unless a test explicitly clears it.
func TestMain(m *testing.M) {
	_ = os.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	os.Exit(m.Run())
}

func TestInventoryOnlyRejectsAllNewWLEDWritesWithoutCreatingLocateOwnership(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "")
	fixture := newPhysicalFixture(t)
	ctx := context.Background()
	legacy := NewService(fixture.store, NewClient(), nil)
	writesBefore := outputGuardPostCount(fixture.mock)

	checks := []struct {
		name string
		run  func() error
	}{
		{"physical inventory Locate", func() error {
			_, err := fixture.service.LocateInventoryBin(ctx, fixture.partID, fixture.bin1, 7)
			return err
		}},
		{"physical drawer Locate", func() error {
			_, err := fixture.service.LocateDrawerBin(ctx, fixture.drawer1, fixture.bin1, 7)
			return err
		}},
		{"legacy part Locate", func() error { return legacy.LocatePart(ctx, fixture.partID) }},
		{"legacy bin Locate", func() error { return legacy.LocateBin(ctx, fixture.controller, fixture.bin1) }},
		{"legacy drawer Locate", func() error { return legacy.LocateDrawer(ctx, fixture.controller, fixture.drawer1) }},
		{"legacy flash", func() error { return legacy.FlashError(ctx, fixture.controller, fixture.bin1) }},
		{"GlobalOff", func() error { return legacy.GlobalOff(ctx) }},
		{"direct client state write", func() error {
			return fixture.client.SetState(ctx, fixture.server.Listener.Addr().String(), map[string]any{"on": true})
		}},
		{"direct client flash write", func() error {
			return fixture.client.Apply(ctx, fixture.server.Listener.Addr().String(), 10, 0, 1, State{Color: "#00FF00", Mode: ModeFlash})
		}},
		{"direct client clear", func() error { return fixture.client.Clear(ctx, fixture.server.Listener.Addr().String()) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); !errors.Is(err, ErrPhysicalOutputDisabled) {
				t.Fatalf("error = %v, want ErrPhysicalOutputDisabled", err)
			}
		})
	}
	if got := outputGuardPostCount(fixture.mock); got != writesBefore {
		t.Fatalf("blocked calls produced %d state writes; before=%d", got, writesBefore)
	}
	sessions, err := fixture.store.ListPhysicalLocateSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("blocked activation created durable Locate ownership: %+v", sessions)
	}
	if owners := currentPhysicalReservationOwnerIDs(); len(owners) != 0 {
		t.Fatalf("blocked activation left process-local ownership: %v", owners)
	}
}

func TestGlobalOffIsRejectedWhilePhysicalLocateOwnsPixels(t *testing.T) {
	fixture := newPhysicalFixture(t)
	ctx := context.Background()
	if _, err := fixture.service.LocateDrawerBin(ctx, fixture.drawer1, fixture.bin1, 7); err != nil {
		t.Fatalf("start simulated physical Locate: %v", err)
	}
	writesBefore := outputGuardPostCount(fixture.mock)
	service := NewService(fixture.store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := service.GlobalOff(ctx); !errors.Is(err, ErrHardwareBusy) {
		t.Fatalf("GlobalOff error = %v, want ErrHardwareBusy while Locate owns pixels", err)
	}
	if writesAfter := outputGuardPostCount(fixture.mock); writesAfter != writesBefore {
		t.Fatalf("GlobalOff sent %d state writes while Locate owns pixels; before=%d", writesAfter-writesBefore, writesBefore)
	}
	if err := fixture.service.StopDrawer(ctx, fixture.drawer1, 7); err != nil {
		t.Fatalf("stop simulated physical Locate: %v", err)
	}
}

func TestInventoryOnlyAllowsOnlyPersistedTargetedCleanup(t *testing.T) {
	fixture := newPhysicalFixture(t)
	ctx := context.Background()
	active, err := fixture.service.LocateDrawerBin(ctx, fixture.drawer1, fixture.bin1, 7)
	if err != nil {
		t.Fatalf("mock-only opt-in Locate: %v", err)
	}
	writesAfterActivation := outputGuardPostCount(fixture.mock)
	if writesAfterActivation == 0 {
		t.Fatal("enabled test capability did not send the mock Locate state write")
	}

	targetsBeforeStop, err := fixture.store.ListPhysicalLocateSessionTargets(ctx, active.ID)
	if err != nil || len(targetsBeforeStop) != 1 {
		t.Fatalf("load persisted cleanup target: targets=%+v err=%v", targetsBeforeStop, err)
	}
	var savedCommands []physicalPixelCommand
	if err := json.Unmarshal([]byte(targetsBeforeStop[0].CommandsJson), &savedCommands); err != nil {
		t.Fatalf("decode persisted targeted cleanup commands: %v", err)
	}
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "false")
	if err := fixture.service.StopDrawer(ctx, fixture.drawer1, 7); err != nil {
		t.Fatalf("persisted targeted cleanup while output disabled: %v", err)
	}
	if got := outputGuardPostCount(fixture.mock); got != writesAfterActivation+len(savedCommands) {
		t.Fatalf("cleanup state writes = %d, want %d saved targeted cleanup writes after %d activation writes", got, len(savedCommands), writesAfterActivation)
	}
	sessions, err := fixture.store.ListPhysicalLocateSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var stopped *db.PhysicalLocateSession
	for i := range sessions {
		if sessions[i].SessionID == active.ID {
			stopped = &sessions[i]
			break
		}
	}
	if stopped == nil || stopped.State != "stopped" {
		t.Fatalf("durable session not safely resolved after acknowledged targeted cleanup: %+v", sessions)
	}
	targets, err := fixture.store.ListPhysicalLocateSessionTargets(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].MayHaveReceived != 0 {
		t.Fatalf("target ownership marker not persisted as released after acknowledged cleanup: %+v", targets)
	}
	posts := fixture.mock.posts()
	for _, body := range posts[writesAfterActivation:] {
		if _, hasGlobalState := body["on"]; hasGlobalState {
			t.Fatalf("cleanup used a controller-wide state payload instead of saved pixel ranges: %+v", body)
		}
		if _, hasSegments := body["seg"]; !hasSegments {
			t.Fatalf("cleanup payload did not target saved pixel ranges: %+v", body)
		}
	}
}

func TestInventoryOnlyRetainsOwnershipWhenSavedCleanupCannotBeVerified(t *testing.T) {
	fixture := newPhysicalFixture(t)
	ctx := context.Background()
	active, err := fixture.service.LocateDrawerBin(ctx, fixture.drawer1, fixture.bin1, 7)
	if err != nil {
		t.Fatalf("mock-only opt-in Locate: %v", err)
	}
	writesBeforeStop := outputGuardPostCount(fixture.mock)
	fixture.mock.mu.Lock()
	fixture.mock.offline = true
	fixture.mock.mu.Unlock()
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "false")
	if err := fixture.service.StopDrawer(ctx, fixture.drawer1, 7); err == nil {
		t.Fatal("cleanup unexpectedly succeeded without verifying the saved controller target")
	}
	if got := outputGuardPostCount(fixture.mock); got != writesBeforeStop {
		t.Fatalf("unverified cleanup sent a WLED state write: before=%d after=%d", writesBeforeStop, got)
	}
	sessions, err := fixture.store.ListPhysicalLocateSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var retained *db.PhysicalLocateSession
	for i := range sessions {
		if sessions[i].SessionID == active.ID {
			retained = &sessions[i]
			break
		}
	}
	if retained == nil || (retained.State != "partial" && retained.State != "stopping") {
		t.Fatalf("unresolved cleanup ownership was not retained: %+v", sessions)
	}
	targets, err := fixture.store.ListPhysicalLocateSessionTargets(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("unverified cleanup incorrectly released durable target ownership: %+v", targets)
	}
}

func outputGuardPostCount(mock *physicalMock) int {
	mock.mu.Lock()
	defer mock.mu.Unlock()
	return mock.postCount
}
