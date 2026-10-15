package wled

import (
	"context"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// locateSessionForController returns the durable session row for a controller,
// or nil when none exists.
func locateSessionForController(t *testing.T, store db.Store, controllerID int64) *db.PhysicalLocateSession {
	t.Helper()
	rows, err := store.ListPhysicalLocateSessions(context.Background())
	if err != nil {
		t.Fatalf("list durable sessions: %v", err)
	}
	for i := range rows {
		if rows[i].ControllerID == controllerID {
			return &rows[i]
		}
	}
	return nil
}

// snapshotSessionForStaleWriter deep-copies an in-memory session into the exact
// shape a paused activation holds immediately before it persists the attempt
// marker: state "activating" with target ownership pending.
func snapshotSessionForStaleWriter(session *physicalSessionState, state string, mayHaveReceived bool) *physicalSessionState {
	copied := &physicalSessionState{
		PhysicalLocateSession: session.PhysicalLocateSession,
		fingerprint:           session.fingerprint,
	}
	copied.Ranges = append([]ControllerRange(nil), session.Ranges...)
	copied.commands = append([]physicalPixelCommand(nil), session.commands...)
	copied.targets = make([]physicalControllerTarget, len(session.targets))
	for i, target := range session.targets {
		target.Ranges = append([]ControllerRange(nil), target.Ranges...)
		target.Commands = append([]physicalPixelCommand(nil), target.Commands...)
		target.MayHaveReceived = mayHaveReceived
		copied.targets[i] = target
	}
	copied.State = state
	return copied
}

// TestStaleActivatingSnapshotCannotResurrectSupersededSession reproduces the
// supersession (H6) defect at the durable layer.
//
// A writer that was paused immediately before persisting its activation-attempt
// marker holds an in-memory snapshot in state "activating". By the time it
// resumes, an independent process may have stopped its session and completed a
// replacement session on the same controller. The stale writer must not be able
// to re-create its own durable session row (resurrect ownership) and must not be
// able to send an obsolete activation command.
func TestStaleActivatingSnapshotCannotResurrectSupersededSession(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()

	// Session S1 is reserved and activated by the first service instance.
	if _, err := f.service.LocateInventoryBin(ctx, f.partID, f.bin1, 7); err != nil {
		t.Fatalf("activate S1: %v", err)
	}
	live := f.service.getSession(f.controller)
	if live == nil {
		t.Fatal("no in-memory session after Locate")
	}
	supersededID := live.ID
	stale := snapshotSessionForStaleWriter(live, "activating", true)

	// An independent process stops S1 and completes a replacement session S2 on
	// the same controller, leaving the durable row terminal and owned by S2.
	replacer := NewPhysicalLocateService(f.store, f.client)
	if err := replacer.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("stop S1: %v", err)
	}
	if _, err := replacer.LocateInventoryBin(ctx, f.partID, f.bin2, 7); err != nil {
		t.Fatalf("activate S2: %v", err)
	}
	if err := replacer.StopDrawer(ctx, f.drawer2, 7); err != nil {
		t.Fatalf("stop S2: %v", err)
	}

	durable := locateSessionForController(t, f.store, f.controller)
	if durable == nil {
		t.Fatal("replacement left no durable session row")
	}
	if durable.SessionID == supersededID {
		t.Fatalf("replacement did not supersede S1 (%s)", supersededID)
	}
	if durable.State != "stopped" && durable.State != "failed" {
		t.Fatalf("durable replacement state = %q, want terminal", durable.State)
	}

	postsBefore := len(f.mock.posts())

	// The stale activating writer must be refused.
	if err := f.service.persistSession(ctx, stale); err == nil {
		t.Fatal("stale activating snapshot resurrected a superseded Locate session")
	}

	// Durable pixel ownership must not have been recreated.
	owned, err := db.HasPhysicalLocateOwnership(ctx, f.store)
	if err != nil {
		t.Fatalf("read durable ownership: %v", err)
	}
	if owned {
		t.Fatal("stale writer re-established durable pixel ownership")
	}
	after := locateSessionForController(t, f.store, f.controller)
	if after != nil && after.SessionID == supersededID {
		t.Fatalf("durable session was resurrected to superseded id %s", supersededID)
	}

	// Even a direct command claim for the superseded session id must send no
	// WLED write.
	controller, err := f.store.GetController(ctx, f.controller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.withPhysicalCommandClaim(ctx, supersededID, &stale.targets[0], []string{"activating"}, controller, []int{255, 0, 0}, false); err == nil {
		t.Fatal("command claim authorized a superseded session")
	}
	if got := len(f.mock.posts()); got != postsBefore {
		t.Fatalf("stale writer sent %d WLED writes, want 0", got-postsBefore)
	}

	if session := f.service.getSession(f.controller); session != nil && session.timer != nil {
		session.timer.Stop()
	}
}
