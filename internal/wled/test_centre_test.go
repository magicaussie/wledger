package wled

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func centreRequest(f *physicalFixture) TestRequest {
	return TestRequest{ControllerID: f.controller, Start: 20, Count: 3, Function: "test_pixel", Mode: "solid", Style: FunctionStyle{Color: "#FF0000", Brightness: 255}, Duration: time.Minute}
}
func centrePixel(f *physicalFixture, n int) [3]int {
	f.mock.mu.Lock()
	defer f.mock.mu.Unlock()
	return f.mock.framebuffer[n]
}
func centreWait(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out waiting for test operation")
}

func TestCentreOutputBoundaries(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	for _, tc := range []struct {
		output       int
		start, count int64
		valid        bool
	}{
		{0, 0, 1, true}, {0, 99, 1, true}, {0, 99, 2, false}, {0, 100, 1, false}, {1, 100, 100, true}, {1, 99, 1, false}, {1, 199, 1, true}, {1, 199, 2, false}, {2, 0, 1, false}, {-1, 0, 1, false},
	} {
		t.Run(fmt.Sprintf("output%d_%d_%d", tc.output, tc.start, tc.count), func(t *testing.T) {
			f := newPhysicalFixture(t)
			in := centreRequest(f)
			in.OutputOrder = &tc.output
			in.Start = tc.start
			in.Count = tc.count
			before := len(f.mock.posts())
			session, err := f.service.StartTest(context.Background(), in, 1)
			if !tc.valid {
				if err == nil {
					_ = f.service.StopOperation(context.Background(), session.ID, 1)
					t.Fatal("accepted range outside real hardware output")
				}
				if len(f.mock.posts()) != before {
					t.Fatal("rejected output range sent a state command")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer f.service.StopOperation(context.Background(), session.ID, 1)
			if got := centrePixel(f, int(tc.start)); got != ([3]int{255, 0, 0}) {
				t.Fatalf("first pixel=%v", got)
			}
			if got := centrePixel(f, int(tc.start+tc.count-1)); got != ([3]int{255, 0, 0}) {
				t.Fatalf("last pixel=%v", got)
			}
		})
	}
}

func TestCentreDurableStartAndStopOwnership(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newPhysicalFixture(t)
	ctx := context.Background()
	session, err := f.service.StartTest(ctx, centreRequest(f), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, session.ID, 1)
	rows, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(rows) != 1 || rows[0].SessionID != session.ID || rows[0].State != "active" {
		t.Fatalf("durable sessions=%+v err=%v", rows, err)
	}
	metadata, err := f.store.GetPhysicalOperationMetadata(ctx, session.ID)
	if err != nil || !metadata.ExpiresAt.After(time.Now()) {
		t.Fatalf("metadata=%+v err=%v", metadata, err)
	}
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, session.ID)
	if err != nil || len(targets) != 1 || targets[0].MayHaveReceived == 0 {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	before := len(f.mock.posts())
	if err = f.service.StopOperation(ctx, session.ID, 2); !errors.Is(err, ErrLocateSessionOwned) {
		t.Fatalf("wrong owner stop=%v", err)
	}
	if len(f.mock.posts()) != before {
		t.Fatal("wrong owner sent cleanup")
	}
}

func TestCentreDisjointLocateStopIsolation(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newPhysicalFixture(t)
	ctx := context.Background()
	locate, err := f.service.LocateDrawerBin(ctx, f.drawer2, f.bin2, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, locate.ID, 1)
	original := centrePixel(f, 9)
	session, err := f.service.StartTest(ctx, centreRequest(f), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, session.ID, 2)
	if err = f.service.StopOperation(ctx, session.ID, 2); err != nil {
		t.Fatal(err)
	}
	if got := centrePixel(f, 9); got != original {
		t.Fatalf("test cleanup altered concurrent Locate: %v -> %v", original, got)
	}
	if got := centrePixel(f, 20); got != ([3]int{}) {
		t.Fatalf("test not cleared: %v", got)
	}
	session, err = f.service.StartTest(ctx, centreRequest(f), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, session.ID, 2)
	if err = f.service.StopOperation(ctx, locate.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := centrePixel(f, 20); got != ([3]int{255, 0, 0}) {
		t.Fatalf("Locate cleanup altered test: %v", got)
	}
}

func TestCentrePatternsTimeoutCancelNoRelight(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	for _, mode := range []string{"flash", "sequence"} {
		for _, stop := range []string{"cancel", "timeout"} {
			t.Run(mode+"/"+stop, func(t *testing.T) {
				f := newPhysicalFixture(t)
				ctx := context.Background()
				in := centreRequest(f)
				in.Mode = mode
				if stop == "timeout" {
					in.Duration = time.Second
				}
				session, err := f.service.StartTest(ctx, in, 1)
				if err != nil {
					t.Fatal(err)
				}
				defer f.service.StopOperation(ctx, session.ID, 1)
				centreWait(t, func() bool { return len(f.mock.posts()) >= 2 })
				if stop == "cancel" {
					if err = f.service.StopOperation(ctx, session.ID, 1); err != nil {
						t.Fatal(err)
					}
				}
				centreWait(t, func() bool {
					rows, e := f.store.ListPhysicalLocateSessions(ctx)
					if e != nil {
						return false
					}
					for _, row := range rows {
						if row.SessionID == session.ID {
							return row.State == "stopped"
						}
					}
					return false
				})
				before := len(f.mock.posts())
				time.Sleep(650 * time.Millisecond)
				if len(f.mock.posts()) != before {
					t.Fatal("stopped pattern sent another command")
				}
				for n := 20; n < 23; n++ {
					if got := centrePixel(f, n); got != ([3]int{}) {
						t.Fatalf("pixel %d relit: %v", n, got)
					}
				}
				// A new service loading durable state must not restart a saved pattern.
				restarted := NewPhysicalLocateService(f.store, f.client)
				if err := restarted.ensureLoaded(ctx); err != nil {
					t.Fatal(err)
				}
				defer stopOwnershipTimers(restarted)
				before = len(f.mock.posts())
				time.Sleep(550 * time.Millisecond)
				if len(f.mock.posts()) != before {
					t.Fatal("restart relaunched saved pattern")
				}
			})
		}
	}
}

func TestCentreDrawerWithoutSelectedController(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newPhysicalFixture(t)
	in := centreRequest(f)
	in.ControllerID = 0
	in.DrawerID = f.drawer2
	in.Function = "drawer_all"
	in.Count = 0
	session, err := f.service.StartTest(context.Background(), in, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(context.Background(), session.ID, 1)
	if got := centrePixel(f, 9); got != ([3]int{255, 0, 0}) {
		t.Fatalf("drawer first pixel=%v", got)
	}
	if got := centrePixel(f, 10); got != ([3]int{255, 0, 0}) {
		t.Fatalf("drawer last pixel=%v", got)
	}
}

func TestCentrePatternFramesAreBoundedAndOrdered(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newPhysicalFixture(t)
	ctx := context.Background()
	in := centreRequest(f)
	session, err := f.service.StartTest(ctx, in, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, session.ID, 1)
	outside := centrePixel(f, 23)
	for step := int64(0); step < 7; step++ {
		done, err := f.service.testFrame(ctx, session.ID, step, "sequence")
		if done || err != nil {
			t.Fatalf("frame %d done=%v err=%v", step, done, err)
		}
		for pixel := 0; pixel < 3; pixel++ {
			want := [3]int{}
			if int64(pixel) == step%3 {
				want = [3]int{255, 0, 0}
			}
			if got := centrePixel(f, 20+pixel); got != want {
				t.Fatalf("step %d pixel %d=%v want %v", step, pixel, got, want)
			}
		}
		if got := centrePixel(f, 23); got != outside {
			t.Fatalf("sequence wrote outside range: %v -> %v", outside, got)
		}
	}
	for step := int64(0); step < 4; step++ {
		done, err := f.service.testFrame(ctx, session.ID, step, "flash")
		if done || err != nil {
			t.Fatalf("flash done=%v err=%v", done, err)
		}
		want := [3]int{}
		if step%2 == 1 {
			want = [3]int{255, 0, 0}
		}
		for pixel := 20; pixel < 23; pixel++ {
			if got := centrePixel(f, pixel); got != want {
				t.Fatalf("flash %d pixel %d=%v want %v", step, pixel, got, want)
			}
		}
	}
	if err := f.service.StopOperation(ctx, session.ID, 1); err != nil {
		t.Fatal(err)
	}
	before := len(f.mock.posts())
	done, err := f.service.testFrame(ctx, session.ID, 0, "sequence")
	if !done || err != nil || len(f.mock.posts()) != before {
		t.Fatalf("stale frame done=%v err=%v sends=%d", done, err, len(f.mock.posts())-before)
	}
}

func TestCentreDrawerBinWithoutSelectedController(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newPhysicalFixture(t)
	in := centreRequest(f)
	in.ControllerID = 0
	in.DrawerID = f.drawer2
	in.BinID = f.bin2
	in.Function = "drawer_bin"
	in.Count = 0
	session, err := f.service.StartTest(context.Background(), in, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(context.Background(), session.ID, 1)
	if got := centrePixel(f, 9); got != ([3]int{255, 0, 0}) {
		t.Fatalf("drawer bin fallback pixel=%v", got)
	}
}

func TestCentreExplicitZeroBrightness(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	f := newPhysicalFixture(t)
	in := centreRequest(f)
	in.Style = FunctionStyle{Color: "#123456", Brightness: 0}
	session, err := f.service.StartTest(context.Background(), in, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(context.Background(), session.ID, 1)
	if got := centrePixel(f, 20); got != ([3]int{}) {
		t.Fatalf("zero brightness replaced by default: %v", got)
	}
}
