package wled

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

const locateCrashHelperEnv = "WLEDGER_LOCATE_CRASH_HELPER"

type locateCrashStore struct {
	db.Store
	point string
	calls int
}

func (s *locateCrashStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	s.calls++
	call := s.calls
	if s.point == "before_reservation" && call == 1 {
		killLocateTestProcess(81)
	}
	err := s.Store.ExecImmediateTx(ctx, fn)
	if err == nil && s.point == "after_reservation" && call == 1 {
		killLocateTestProcess(82)
	}
	if err == nil && s.point == "after_attempt_marker" && call == 2 {
		killLocateTestProcess(83)
	}
	return err
}

func killLocateTestProcess(fallback int) {
	if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
		os.Exit(fallback)
	}
	select {}
}

func TestPhysicalLocateCrashHelper(t *testing.T) {
	point := os.Getenv(locateCrashHelperEnv)
	if point == "" {
		return
	}
	path := os.Getenv("WLEDGER_LOCATE_CRASH_DB")

	drawerID, _ := strconv.ParseInt(os.Getenv("WLEDGER_LOCATE_CRASH_DRAWER"), 10, 64)
	binID, _ := strconv.ParseInt(os.Getenv("WLEDGER_LOCATE_CRASH_BIN"), 10, 64)
	ownerID, _ := strconv.ParseInt(os.Getenv("WLEDGER_LOCATE_CRASH_OWNER"), 10, 64)
	database, err := db.Open("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	store := &locateCrashStore{Store: db.NewStore(database), point: point}
	service := NewPhysicalLocateService(store, NewClient())
	if _, err := service.LocateDrawerBin(context.Background(), drawerID, binID, ownerID); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("Locate helper returned before its crash boundary")
}

func TestPhysicalLocateSIGKILLCrashMatrix(t *testing.T) {
	cases := []struct {
		name               string
		point              string
		wantSession        bool
		wantMarker         bool
		waitForHTTP        bool
		waitForApplication bool
	}{
		{name: "L01 before reservation", point: "before_reservation"},
		{name: "L02-L03 atomic reservation and target snapshot", point: "after_reservation", wantSession: true},
		{name: "L04 marker durable before HTTP send", point: "after_attempt_marker", wantSession: true, wantMarker: true},
		{name: "L05 request begins but mock does not read body", point: "request_unread", wantSession: true, wantMarker: true, waitForHTTP: true},
		{name: "L06 command applied and response withheld", point: "applied_no_response", wantSession: true, wantMarker: true, waitForHTTP: true, waitForApplication: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPhysicalFixture(t)
			initialPixels := f.mock.pixels()
			var seq int
			var name, databasePath string
			if err := f.database.QueryRowContext(context.Background(), "PRAGMA database_list").Scan(&seq, &name, &databasePath); err != nil {
				t.Fatalf("resolve fixture database file: %v", err)
			}

			requestLogStart := f.mock.requestCount()
			var requestEntered, requestRelease, deliveryApplied, deliveryRelease chan struct{}
			if tc.point == "request_unread" {
				requestEntered = make(chan struct{})
				requestRelease = make(chan struct{})
				f.mock.mu.Lock()
				f.mock.postEntered = requestEntered
				f.mock.postRelease = requestRelease
				f.mock.mu.Unlock()
			}
			if tc.point == "applied_no_response" {
				deliveryApplied = make(chan struct{})
				deliveryRelease = make(chan struct{})
				f.mock.mu.Lock()
				f.mock.postAccepted = deliveryApplied
				f.mock.postResponseRelease = deliveryRelease
				f.mock.mu.Unlock()
			}

			command := exec.Command(os.Args[0], "-test.run=^TestPhysicalLocateCrashHelper$")
			command.Env = append(os.Environ(),
				locateCrashHelperEnv+"="+tc.point,
				"WLEDGER_LOCATE_CRASH_DB="+databasePath,

				fmt.Sprintf("WLEDGER_LOCATE_CRASH_DRAWER=%d", f.drawer1),
				fmt.Sprintf("WLEDGER_LOCATE_CRASH_BIN=%d", f.bin1),
				"WLEDGER_LOCATE_CRASH_OWNER=7",
			)
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}

			if tc.waitForHTTP {
				event := requestEntered
				if tc.waitForApplication {
					event = deliveryApplied
				}
				select {
				case <-event:
				case <-time.After(5 * time.Second):
					_ = command.Process.Kill()
					_ = command.Wait()
					t.Fatalf("helper did not reach %s; output=%s", tc.point, output.String())
				}
				assertCrashState(t, f.store, tc)
				if tc.point == "request_unread" {
					if len(f.mock.posts()) != 0 || !strings.Contains(strings.Join(f.mock.requestsSince(requestLogStart), ","), "POST /json/state") {
						t.Fatalf("L05 mock did not record a begun-but-unread request: requests=%v bodies=%v", f.mock.requestsSince(requestLogStart), f.mock.posts())
					}
				}
				if tc.waitForApplication {
					if len(f.mock.posts()) != 1 {
						t.Fatalf("delivered-before-ack scenario recorded %d POSTs, want 1", len(f.mock.posts()))
					}
					if reflect.DeepEqual(f.mock.pixels(), initialPixels) {
						t.Fatal("controller did not apply the activation before withholding its response")
					}
					if len(f.mock.posts()[0]) == 0 || f.mock.posts()[0]["on"] != nil {
						t.Fatalf("activation was not a targeted pixel command: %#v", f.mock.posts()[0])
					}
					if f.mock.pixels()[96] == initialPixels[96] {
						t.Fatal("owned pixel did not change before the activation response was withheld")
					}
				}
				if err := command.Process.Kill(); err != nil {
					t.Fatalf("SIGKILL helper at %s: %v", tc.point, err)
				}
			}
			if err := command.Wait(); err == nil {
				t.Fatalf("helper exited normally instead of being killed at %s; output=%s", tc.point, output.String())
			}
			if state, ok := command.ProcessState.Sys().(syscall.WaitStatus); !ok || state.Signal() != syscall.SIGKILL {
				t.Fatalf("helper termination = %v, want SIGKILL; output=%s", command.ProcessState, output.String())
			}
			if requestRelease != nil {
				close(requestRelease)
			}
			if deliveryRelease != nil {
				close(deliveryRelease)
			}
			assertCrashState(t, f.store, tc)

			targets := []db.PhysicalLocateSessionTarget(nil)
			sessions, err := f.store.ListPhysicalLocateSessions(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantSession {
				if len(sessions) != 1 || sessions[0].State != "activating" {
					t.Fatalf("durable pre-crash session = %+v, want activating", sessions)
				}
				targets, err = f.store.ListPhysicalLocateSessionTargets(context.Background(), sessions[0].SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if len(targets) != 1 || (targets[0].MayHaveReceived != 0) != tc.wantMarker || targets[0].RangesJson == "[]" || targets[0].CommandsJson == "[]" {
					t.Fatalf("durable pre-crash target = %+v; want marker=%v and saved ranges/commands", targets, tc.wantMarker)
				}
			} else if len(sessions) != 0 {
				t.Fatalf("pre-reservation crash left a session: %+v", sessions)
			}

			beforeRecoveryPosts := len(f.mock.posts())
			if !tc.waitForHTTP && beforeRecoveryPosts != 0 {
				t.Fatalf("%s sent %d activation requests before its crash boundary", tc.point, beforeRecoveryPosts)
			}
			restarted := NewPhysicalLocateService(f.store, NewClient())
			if tc.wantSession {
				if err := restarted.StopDrawer(context.Background(), f.drawer1, 7); err != nil {
					t.Fatalf("restart recovery Stop: %v", err)
				}
			}
			if err := restarted.StopDrawer(context.Background(), f.drawer1, 7); err != nil {
				t.Fatalf("repeated Stop after recovery: %v", err)
			}
			afterRecoveryPosts := len(f.mock.posts())
			if tc.wantMarker {
				if afterRecoveryPosts <= beforeRecoveryPosts {
					t.Fatal("possibly delivered activation did not cause saved targeted recovery writes")
				}
			} else if afterRecoveryPosts != beforeRecoveryPosts {
				t.Fatalf("definitely pre-attempt boundary caused %d unexpected recovery writes", afterRecoveryPosts-beforeRecoveryPosts)
			}
			for _, post := range f.mock.posts() {
				if _, global := post["on"]; global {
					t.Fatalf("crash recovery sent a controller-wide command: %#v", post)
				}
			}
			finalPixels := f.mock.pixels()
			if tc.wantMarker {
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
							if finalPixels[pixel] != [3]int{0, 0, 0} {
								t.Errorf("owned pixel %d was not black after recovery: %v", pixel, finalPixels[pixel])
							}
						}
					}
				}
			}
			owned := make(map[int]struct{})
			for _, target := range targets {
				if !tc.wantMarker {
					continue
				}
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
						owned[pixel] = struct{}{}
					}
				}
			}
			for pixel := range finalPixels {
				if _, ok := owned[pixel]; !ok && finalPixels[pixel] != initialPixels[pixel] {
					t.Errorf("unowned pixel %d changed across recovery: before=%v after=%v", pixel, initialPixels[pixel], finalPixels[pixel])
				}
			}
		})
	}
}

func assertCrashState(t *testing.T, store db.Store, tc struct {
	name               string
	point              string
	wantSession        bool
	wantMarker         bool
	waitForHTTP        bool
	waitForApplication bool
}) {
	t.Helper()
	if !tc.wantSession {
		sessions, err := store.ListPhysicalLocateSessions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 0 {
			t.Fatalf("%s created durable session before reservation: %+v", tc.name, sessions)
		}
		return
	}
	sessions, err := store.ListPhysicalLocateSessions(context.Background())
	if err != nil || len(sessions) != 1 || sessions[0].State != "activating" {
		t.Fatalf("%s durable session = %+v err=%v, want activating", tc.name, sessions, err)
	}
	targets, err := store.ListPhysicalLocateSessionTargets(context.Background(), sessions[0].SessionID)
	if err != nil || len(targets) != 1 {
		t.Fatalf("%s durable targets = %+v err=%v, want one saved target", tc.name, targets, err)
	}
	if (targets[0].MayHaveReceived == 1) != tc.wantMarker || targets[0].RangesJson == "[]" || targets[0].CommandsJson == "[]" {
		t.Fatalf("%s target = %+v, want marker=%v with saved cleanup data", tc.name, targets[0], tc.wantMarker)
	}
}
