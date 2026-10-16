package wled

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestPhysicalLocateBackgroundRecoveryDoesNotTransferAuthenticatedOwnership(t *testing.T) {
	oldLocateTimeout := locateSessionTimeout
	oldRetryDelay := physicalLocateCleanupRetryDelay
	oldControllerTimeout := physicalLocateControllerTimeout
	locateSessionTimeout = time.Hour
	physicalLocateCleanupRetryDelay = 25 * time.Millisecond
	physicalLocateControllerTimeout = 200 * time.Millisecond
	defer func() {
		locateSessionTimeout = oldLocateTimeout
		physicalLocateCleanupRetryDelay = oldRetryDelay
		physicalLocateControllerTimeout = oldControllerTimeout
	}()

	f := newPhysicalFixture(t)
	ctx := context.Background()
	if _, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err != nil {
		t.Fatal(err)
	}
	original := f.service.getSession(f.controller)
	if original == nil || original.timer == nil {
		t.Fatal("successful Locate did not schedule service recovery")
	}
	original.timer.Stop()
	locateSessionTimeout = 10 * time.Millisecond
	if _, err := f.database.ExecContext(ctx, `UPDATE physical_operation_metadata SET expires_at=? WHERE session_id=?`, time.Now().Add(10*time.Millisecond), original.ID); err != nil {
		t.Fatal(err)
	}

	f.mock.mu.Lock()
	f.mock.offline = true
	f.mock.mu.Unlock()
	beforePosts := len(f.mock.posts())
	restarted := NewPhysicalLocateService(f.store, NewClient())

	// A different authenticated user is not granted session ownership merely
	// because the original user is unavailable.
	if err := restarted.StopDrawer(ctx, f.drawer1, 8); !errors.Is(err, ErrLocateSessionOwned) {
		t.Fatalf("foreign-user Stop=%v, want ErrLocateSessionOwned", err)
	}
	if got := len(f.mock.posts()); got != beforePosts {
		t.Fatalf("foreign-user Stop sent %d unexpected WLED writes", got-beforePosts)
	}

	// Wait for startup-scheduled background recovery to observe the offline
	// controller and retain the durable unresolved target.
	deadline := time.Now().Add(2 * time.Second)
	var session PhysicalLocateSession
	for time.Now().Before(deadline) {
		rows, err := f.store.ListPhysicalLocateSessions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].State == "partial" && rows[0].LastError != "" {
			var targets []db.PhysicalLocateSessionTarget
			targets, err = f.store.ListPhysicalLocateSessionTargets(ctx, rows[0].SessionID)
			if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 1 {
				t.Fatalf("offline recovery released possible delivery: %+v err=%v", targets, err)
			}
			session = PhysicalLocateSession{ID: rows[0].SessionID, DrawerID: rows[0].DrawerID, State: rows[0].State}
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if session.ID == "" {
		t.Fatal("restarted service did not attempt background recovery while the controller was offline")
	}
	if got := len(f.mock.posts()); got != beforePosts {
		t.Fatalf("offline recovery sent state POSTs: before=%d after=%d", beforePosts, got)
	}

	// Reconnect and use a request barrier to prove the restarted service's
	// background worker, not a new authenticated owner's Stop request, performs
	// saved-range cleanup.
	cleanupAccepted := make(chan struct{})
	cleanupRelease := make(chan struct{})
	f.mock.mu.Lock()
	f.mock.offline = false
	f.mock.postAccepted = cleanupAccepted
	f.mock.postResponseRelease = cleanupRelease
	f.mock.mu.Unlock()
	select {
	case <-cleanupAccepted:
	case <-time.After(2 * time.Second):
		f.mock.mu.Lock()
		f.mock.postResponseRelease = nil
		f.mock.mu.Unlock()
		t.Fatal("background recovery did not retry after controller reconnection")
	}
	close(cleanupRelease)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := f.store.ListPhysicalLocateSessions(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 && rows[0].State == "stopped" {
			targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, rows[0].SessionID)
			if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 0 {
				t.Fatalf("background recovery finalized before target resolution: %+v err=%v", targets, err)
			}
			for _, post := range f.mock.posts()[beforePosts:] {
				if _, global := post["on"]; global {
					t.Fatalf("background recovery used GlobalOff: %#v", post)
				}
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background recovery received cleanup response but did not durably stop the session")
}
