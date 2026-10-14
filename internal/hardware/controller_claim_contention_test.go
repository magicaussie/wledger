package hardware

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

const controllerClaimHelperEnv = "WLEDGER_CONTROLLER_CLAIM_HELPER"
const controllerClaimDatabaseEnv = "WLEDGER_CONTROLLER_CLAIM_DATABASE"

func TestControllerClaimHelper(t *testing.T) {
	if os.Getenv(controllerClaimHelperEnv) != "hold" {
		return
	}
	conn, err := db.Open("file:" + os.Getenv(controllerClaimDatabaseEnv))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.WithOperationClaim(context.Background(), db.NewStore(conn), func(db.Querier) error {
		_, _ = fmt.Fprintln(os.Stdout, "CONTROLLER_WRITER_CLAIM_HELD")
		select {}
	}); err == nil {
		t.Fatal("claim helper exited without an error")
	}
}

func TestControllerEditFailsClosedWhileIndependentProcessHoldsWriterClaim(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller-claim.db")
	conn, err := db.Open("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(conn)
	controller, err := store.CreateController(ctx, controllerParams("Original", "127.0.0.1", 18080))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SavePhysicalLocateSession(ctx, db.SavePhysicalLocateSessionParams{
		ControllerID: controller.ID, ControllerAddress: "127.0.0.1", ControllerPort: 18080,
		SessionID: "controller-contention", DrawerID: 1, BinID: 1, OwnerID: 3, State: "partial",
		RangesJson: `[{"start":25,"count":8}]`, CommandsJson: `[{"segment_id":10,"start":24,"stop":32}]`,
		Fingerprint: "saved-fingerprint", Color: "#ff0000", Brightness: 128,
		LastOperation: "cleanup pending", LastOperationAt: time.Now(), LastError: "offline",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SavePhysicalLocateSessionTarget(ctx, db.SavePhysicalLocateSessionTargetParams{
		SessionID: "controller-contention", ControllerID: controller.ID, ControllerAddress: "127.0.0.1", ControllerPort: 18080,
		RangesJson: `[{"start":25,"count":8}]`, CommandsJson: `[{"segment_id":10,"start":24,"stop":32}]`,
		Fingerprint: "saved-fingerprint", MayHaveReceived: 1, LastError: "offline",
	}); err != nil {
		t.Fatal(err)
	}
	var configuredTimeout int
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&configuredTimeout); err != nil {
		t.Fatal(err)
	}
	if configuredTimeout != 5000 {
		t.Fatalf("db.Open busy_timeout=%d ms, want configured default 5000 ms", configuredTimeout)
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=300"); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, wled.NewClient(), nil)

	command := exec.Command(os.Args[0], "-test.run=^TestControllerClaimHelper$")
	command.Env = append(os.Environ(), controllerClaimHelperEnv+"=hold", controllerClaimDatabaseEnv+"="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "CONTROLLER_WRITER_CLAIM_HELD" {
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
			t.Fatal("other process exited before taking its writer claim")
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal("timed out waiting for other process to hold the writer claim")
	}

	started := time.Now()
	contendedErr := service.UpdateController(ctx, controller.ID, controllerParams("Changed", "192.0.2.99", 8080))
	elapsed := time.Since(started)
	t.Logf("C03 controller mutation contention: busy_timeout=300ms elapsed=%s error=%v", elapsed, contendedErr)
	if contendedErr == nil {
		t.Fatal("controller edit succeeded without obtaining the SQLite writer claim")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("controller edit exceeded the 2 second bound with busy_timeout=300ms: %v", elapsed)
	}
	stored, err := store.GetController(ctx, controller.ID)
	if err != nil || stored.Name != "Original" || stored.IpAddress != "127.0.0.1" || stored.Port.Int64 != 18080 {
		t.Fatalf("contended edit partially changed endpoint: %+v err=%v", stored, err)
	}
	targets, err := store.ListPhysicalLocateSessionTargets(ctx, "controller-contention")
	if err != nil || len(targets) != 1 || targets[0].ControllerAddress != "127.0.0.1" || targets[0].ControllerPort != 18080 || targets[0].MayHaveReceived != 1 {
		t.Fatalf("contended edit changed saved recovery identity: %+v err=%v", targets, err)
	}

	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill writer-claim process: %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("writer-claim process exited normally, want SIGKILL")
	}
	if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); !ok || status.Signal() != syscall.SIGKILL {
		t.Fatalf("writer-claim process termination=%v, want SIGKILL", command.ProcessState)
	}
	if err := service.UpdateController(ctx, controller.ID, controllerParams("Changed", "192.0.2.99", 8080)); !errors.Is(err, db.ErrPhysicalLocateActive) {
		t.Fatalf("controller edit after lock release=%v, want durable Locate ownership refusal", err)
	}
	stored, err = store.GetController(ctx, controller.ID)
	if err != nil || stored.Name != "Original" || stored.IpAddress != "127.0.0.1" || stored.Port.Int64 != 18080 {
		t.Fatalf("post-release edit changed endpoint despite durable ownership: %+v err=%v", stored, err)
	}
}
