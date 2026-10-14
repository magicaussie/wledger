package wled

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

const locateClaimProcessHelperEnv = "WLEDGER_LOCATE_CLAIM_PROCESS_HELPER"
const locateClaimProcessDBEnv = "WLEDGER_LOCATE_CLAIM_PROCESS_DB"

func TestPhysicalLocateClaimProcessHelper(t *testing.T) {
	if os.Getenv(locateClaimProcessHelperEnv) != "hold" {
		return
	}
	path := os.Getenv(locateClaimProcessDBEnv)
	conn, err := db.Open("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	store := db.NewStore(conn)
	if err := db.WithOperationClaim(context.Background(), store, func(db.Querier) error {
		_, _ = fmt.Fprintln(os.Stdout, "LOCATE_WRITER_CLAIM_HELD")
		select {}
	}); err == nil {
		t.Fatal("claim holder exited without an error")
	}
}

func TestPhysicalLocateAndStopFailClosedUnderCrossProcessWriterContention(t *testing.T) {
	ctx := context.Background()
	f := newPhysicalFixture(t)
	f.database.SetMaxOpenConns(1)
	if _, err := f.database.ExecContext(ctx, "PRAGMA busy_timeout=300"); err != nil {
		t.Fatal(err)
	}
	var busyTimeout int
	if err := f.database.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout != 300 {
		t.Fatalf("test connection busy_timeout=%d ms, want 300 ms", busyTimeout)
	}

	// C01/C05: an independent process holds BEGIN IMMEDIATE while this service
	// repeatedly attempts Locate. No callback, preflight WLED read, reservation or
	// activation may get past the failed claim.
	beforePosts := len(f.mock.posts())
	holder := startLocateClaimProcess(t, f.database)
	for attempt := 1; attempt <= 2; attempt++ {
		started := time.Now()
		if _, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err == nil {
			t.Fatalf("contended Locate attempt %d succeeded without a writer claim", attempt)
		}
		elapsed := time.Since(started)
		t.Logf("C01/C05 Locate contention attempt %d: busy_timeout=%dms elapsed=%s", attempt, busyTimeout, elapsed)
		if elapsed > 2*time.Second {
			t.Fatalf("contended Locate attempt %d exceeded the 2 second bound: %v", attempt, elapsed)
		}
		if got := len(f.mock.posts()); got != beforePosts {
			t.Fatalf("contended Locate attempt %d issued state POSTs: before=%d after=%d", attempt, beforePosts, got)
		}
		sessions, err := f.store.ListPhysicalLocateSessions(ctx)
		if err != nil || len(sessions) != 0 {
			t.Fatalf("contended Locate attempt %d left durable ownership: %+v err=%v", attempt, sessions, err)
		}
	}
	killLocateClaimProcess(t, holder)

	// C08: the exact operation can proceed after the other process dies and
	// SQLite releases its writer reservation.
	if _, err := f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 7); err != nil {
		t.Fatalf("Locate after claim-holder death: %v", err)
	}
	if got := len(f.mock.posts()); got <= beforePosts {
		t.Fatalf("Locate after claim release sent no activation: before=%d after=%d", beforePosts, got)
	}

	// C02/C06: while active ownership exists, another process's SQLite writer
	// reservation prevents Stop's state transition from reaching WLED. This
	// service-level caller then retries safely once the child holding the lock is
	// killed; successful cleanup clears only the saved ranges.
	holder = startLocateClaimProcess(t, f.database)
	beforeStopPosts := len(f.mock.posts())
	for attempt := 1; attempt <= 2; attempt++ {
		started := time.Now()
		if err := f.service.StopDrawer(ctx, f.drawer1, 7); err == nil {
			t.Fatalf("contended Stop attempt %d succeeded without a writer claim", attempt)
		}
		elapsed := time.Since(started)
		t.Logf("C02/C06 Stop contention attempt %d: busy_timeout=%dms elapsed=%s", attempt, busyTimeout, elapsed)
		if elapsed > 2*time.Second {
			t.Fatalf("contended Stop attempt %d exceeded the 2 second bound: %v", attempt, elapsed)
		}
		if got := len(f.mock.posts()); got != beforeStopPosts {
			t.Fatalf("contended Stop attempt %d issued cleanup before claim acquisition: before=%d after=%d", attempt, beforeStopPosts, got)
		}
		sessions, err := f.store.ListPhysicalLocateSessions(ctx)
		if err != nil || len(sessions) != 1 || (sessions[0].State != "active" && sessions[0].State != "partial") {
			t.Fatalf("contended Stop attempt %d released or corrupted ownership: %+v err=%v", attempt, sessions, err)
		}
		targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, sessions[0].SessionID)
		if err != nil || len(targets) != 1 || targets[0].MayHaveReceived != 1 {
			t.Fatalf("contended Stop attempt %d lost the attempt marker: %+v err=%v", attempt, targets, err)
		}
	}
	killLocateClaimProcess(t, holder)
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("Stop after claim-holder death: %v", err)
	}
	if err := f.service.StopDrawer(ctx, f.drawer1, 7); err != nil {
		t.Fatalf("repeated Stop after confirmed cleanup: %v", err)
	}
	for _, post := range f.mock.posts()[beforeStopPosts:] {
		if _, global := post["on"]; global {
			t.Fatalf("post-contention recovery used controller-wide state: %#v", post)
		}
	}
}

func startLocateClaimProcess(t *testing.T, database *sql.DB) *exec.Cmd {
	t.Helper()
	var seq int
	var name, path string
	if err := database.QueryRowContext(context.Background(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestPhysicalLocateClaimProcessHelper$")
	command.Env = append(os.Environ(), locateClaimProcessHelperEnv+"=hold", locateClaimProcessDBEnv+"="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "LOCATE_WRITER_CLAIM_HELD" {
				ready <- scanner.Text()
				return
			}
		}
		ready <- ""
	}()
	select {
	case marker := <-ready:
		if marker == "" {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal("claim-holder process exited before acquiring BEGIN IMMEDIATE")
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal("timed out waiting for the independent claim-holder process")
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	return command
}

func killLocateClaimProcess(t *testing.T, command *exec.Cmd) {
	t.Helper()
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill claim-holder process: %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("claim-holder process exited normally, want SIGKILL")
	}
	if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGKILL {
		t.Fatalf("claim-holder termination=%v, want SIGKILL", command.ProcessState)
	}
}
