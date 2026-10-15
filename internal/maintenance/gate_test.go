package maintenance

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGateExclusiveLockWaitsForInFlightRead(t *testing.T) {
	uploads := filepath.Join(t.TempDir(), "uploads")
	if err := os.Mkdir(uploads, 0755); err != nil {
		t.Fatal(err)
	}
	reader := New(uploads)
	writer := New(uploads)
	unlockRead, err := reader.ReadLock()
	if err != nil {
		t.Fatal(err)
	}

	type lockResult struct {
		unlock func()
		err    error
	}
	acquired := make(chan lockResult, 1)
	go func() {
		unlock, err := writer.WriteLock()
		acquired <- lockResult{unlock: unlock, err: err}
	}()
	select {
	case result := <-acquired:
		if result.err != nil {
			t.Fatalf("acquire exclusive lock: %v", result.err)
		}
		result.unlock()
		t.Fatal("exclusive lock passed an in-flight request")
	case <-time.After(50 * time.Millisecond):
	}
	unlockRead()
	select {
	case result := <-acquired:
		if result.err != nil {
			t.Fatalf("acquire exclusive lock after request: %v", result.err)
		}
		result.unlock()
	case <-time.After(5 * time.Second):
		t.Fatal("exclusive lock did not proceed after the request completed")
	}
}

func TestGateMiddlewareFailsClosedForPendingJournal(t *testing.T) {
	uploads := filepath.Join(t.TempDir(), "uploads")
	if err := os.Mkdir(uploads, 0755); err != nil {
		t.Fatal(err)
	}
	gate := New(uploads)
	control := ControlDir(uploads)
	if err := os.MkdirAll(control, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, JournalName), []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}

	called := false
	h := gate.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("pending restore response = %d, handler called = %v; want 503 without serving", rec.Code, called)
	}
}

// TestLockPathIsSharedInsideTheMount verifies that when the uploads path is a
// mount point the maintenance lock lives inside the persistent mount, so every
// container sharing that bind mount contends on the same lock file. For an
// ordinary uploads directory it stays in the sibling control directory.
func TestLockPathIsSharedInsideTheMount(t *testing.T) {
	uploads := filepath.Join(t.TempDir(), "uploads")
	if err := os.Mkdir(uploads, 0755); err != nil {
		t.Fatal(err)
	}

	original := mountPointDetector
	t.Cleanup(func() { mountPointDetector = original })

	mountPointDetector = func(string) bool { return false }
	if got, want := LockPath(uploads), filepath.Join(ControlDir(uploads), maintenanceLockName); got != want {
		t.Errorf("non-mount lock path = %q, want %q", got, want)
	}

	mountPointDetector = func(string) bool { return true }
	if got, want := LockPath(uploads), filepath.Join(uploads, maintenanceLockName); got != want {
		t.Errorf("mount lock path = %q, want %q", got, want)
	}
}

// TestGateMiddlewareFailsClosedForInMountJournal verifies the fail-closed check
// also covers the durable in-mount journal, which lives inside the persistent
// uploads mount rather than the container filesystem.
func TestGateMiddlewareFailsClosedForInMountJournal(t *testing.T) {
	uploads := filepath.Join(t.TempDir(), "uploads")
	if err := os.Mkdir(uploads, 0755); err != nil {
		t.Fatal(err)
	}
	gate := New(uploads)
	inMount := InMountControlDir(uploads)
	if err := os.MkdirAll(inMount, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inMount, JournalName), []byte("pending"), 0600); err != nil {
		t.Fatal(err)
	}

	called := false
	h := gate.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusServiceUnavailable || called {
		t.Fatalf("in-mount pending restore response = %d, handler called = %v; want 503 without serving", rec.Code, called)
	}
}
