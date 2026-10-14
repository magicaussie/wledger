package wled

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestPhysicalLocateDelayedActivationCanRelightPixelsAfterAcknowledgedRecovery(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	initial := f.mock.pixels()
	var seq int
	var databaseName, databasePath string
	if err := f.database.QueryRowContext(ctx, "PRAGMA database_list").Scan(&seq, &databaseName, &databasePath); err != nil {
		t.Fatalf("resolve fixture database path: %v", err)
	}

	activationReceived := make(chan struct{})
	applyDelayedActivation := make(chan struct{})
	f.mock.mu.Lock()
	f.mock.delayApplyPostNumber = 1
	f.mock.applyPostEntered = activationReceived
	f.mock.applyPostRelease = applyDelayedActivation
	f.mock.mu.Unlock()

	command := exec.Command(os.Args[0], "-test.run=^TestPhysicalLocateCrashHelper$")
	command.Env = append(os.Environ(),
		locateCrashHelperEnv+"=delayed_activation",
		"WLEDGER_LOCATE_CRASH_DB="+databasePath,
		fmt.Sprintf("WLEDGER_LOCATE_CRASH_DRAWER=%d", f.drawer1),
		fmt.Sprintf("WLEDGER_LOCATE_CRASH_BIN=%d", f.bin1),
		"WLEDGER_LOCATE_CRASH_OWNER=7",
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
		select {
		case <-applyDelayedActivation:
		default:
			close(applyDelayedActivation)
		}
	}()

	select {
	case <-activationReceived:
	case <-time.After(5 * time.Second):
		t.Fatal("activation request did not reach the mock's delayed-application barrier")
	}
	if !equalPhysicalPixels(f.mock.pixels(), initial) {
		t.Fatal("mock applied activation before the deterministic delayed-delivery barrier was released")
	}
	sessions, err := f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].State != "activating" {
		t.Fatalf("durable state at delayed activation barrier = %+v err=%v, want activating", sessions, err)
	}
	activationRGB := scaleRGB(colorRGB(sessions[0].Color), locateBrightness)
	activationColor := [3]int{activationRGB[0], activationRGB[1], activationRGB[2]}
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID)
	if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("target at delayed activation barrier = %+v err=%v, want durable possible-delivery ownership", targets, err)
	}

	if err := command.Process.Kill(); err != nil {
		t.Fatalf("SIGKILL activation process: %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("activation child exited normally; expected SIGKILL")
	}
	if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGKILL {
		t.Fatalf("activation child termination=%v, want SIGKILL", command.ProcessState)
	}

	// A new service instance can acquire SQLite's released claim, send the exact
	// persisted targeted cleanup and receive acknowledgements while the mock still
	// holds the earlier activation request before simulated application.
	restarted := NewPhysicalLocateService(f.store, NewClient())
	if err := restarted.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("recovery while activation remained delayed: %v", err)
	}
	sessions, err = f.store.ListPhysicalLocateSessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].State != "stopped" {
		t.Fatalf("session after acknowledged cleanup = %+v err=%v, want stopped", sessions, err)
	}
	targets, err = f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID)
	if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 0 {
		t.Fatalf("target after acknowledged cleanup = %+v err=%v, want resolved", targets, err)
	}
	if got := f.mock.posts(); len(got) < 3 {
		t.Fatalf("mock observed %d POST bodies, want delayed activation plus both targeted cleanup writes", len(got))
	} else if _, hasGlobalState := got[0]["on"]; hasGlobalState {
		t.Fatalf("delayed activation was not a targeted pixel command: %#v", got[0])
	}
	var delayedCommands []physicalPixelCommand
	if err := json.Unmarshal([]byte(targets[0].CommandsJson), &delayedCommands); err != nil {
		t.Fatal(err)
	}
	if !physicalPixelsAreCleaned(f.mock.pixels(), initial, delayedCommands) {
		t.Fatal("acknowledged targeted cleanup did not black only the saved ranges while preserving unrelated pixels")
	}
	for _, post := range f.mock.posts()[1:] {
		if _, global := post["on"]; global {
			t.Fatalf("recovery used a controller-wide command: %#v", post)
		}
	}

	// Simulate the controller applying the previously received activation after
	// cleanup has already been acknowledged and durable ownership released.
	close(applyDelayedActivation)
	deadline := time.Now().Add(2 * time.Second)
	activationPayloads := f.mock.posts()
	for time.Now().Before(deadline) {
		pixels := f.mock.pixels()
		if len(activationPayloads) > 0 && physicalPayloadIsColor(pixels, activationPayloads[0], activationColor) {
			finalSessions, err := f.store.ListPhysicalLocateSessions(ctx)
			if err != nil || len(finalSessions) != 1 || finalSessions[0].State != "stopped" {
				t.Fatalf("delayed activation changed durable session state: %+v err=%v", finalSessions, err)
			}
			t.Logf("reproduced safety limitation: session=%s state=stopped marker=0; delayed activation=%#v arrived after cleanup payloads=%#v were acknowledged; the saved activation ranges were relit", finalSessions[0].SessionID, activationPayloads[0], f.mock.posts()[1:])
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("mock did not apply the activation after release; delayed-delivery scenario was not exercised")
}

func equalPhysicalPixels(a, b [][3]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func physicalPixelsAreCleaned(pixels, initial [][3]int, commands []physicalPixelCommand) bool {
	if len(pixels) != len(initial) {
		return false
	}
	owned := make(map[int]struct{})
	for _, command := range commands {
		offset := 0
		if command.SegmentID == 20 {
			offset = 100
		}
		for pixel := int(command.Start) + offset; pixel < int(command.Stop)+offset; pixel++ {
			if pixel < 0 || pixel >= len(pixels) || pixels[pixel] != [3]int{0, 0, 0} {
				return false
			}
			owned[pixel] = struct{}{}
		}
	}
	for pixel := range pixels {
		if _, ok := owned[pixel]; !ok && pixels[pixel] != initial[pixel] {
			return false
		}
	}
	return len(owned) != 0
}

func physicalPayloadIsColor(pixels [][3]int, payload map[string]any, expected [3]int) bool {
	segments, ok := payload["seg"].([]any)
	if !ok || len(segments) == 0 {
		return false
	}
	applied := false
	for _, rawSegment := range segments {
		segment, ok := rawSegment.(map[string]any)
		if !ok {
			return false
		}
		id, ok := segment["id"].(float64)
		if !ok {
			return false
		}
		values, ok := segment["i"].([]any)
		if !ok || len(values) != 3 {
			return false
		}
		start, startOK := values[0].(float64)
		stop, stopOK := values[1].(float64)
		color, colorOK := values[2].([]any)
		if !startOK || !stopOK || !colorOK || len(color) != 3 {
			return false
		}
		offset := 0
		if int(id) == 20 {
			offset = 100
		}
		for channel := 0; channel < 3; channel++ {
			value, ok := color[channel].(float64)
			if !ok || int(value) != expected[channel] {
				return false
			}
		}
		for pixel := int(start) + offset; pixel < int(stop)+offset; pixel++ {
			if pixel < 0 || pixel >= len(pixels) || pixels[pixel] != expected {
				return false
			}
			applied = true
		}
	}
	return applied
}
