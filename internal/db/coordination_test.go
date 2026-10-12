package db_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

const operationClaimHelperEnv = "WLEDGER_OPERATION_CLAIM_HELPER"
const operationClaimPathEnv = "WLEDGER_OPERATION_CLAIM_PATH"

// This test doubles as a subprocess helper. A killed process must release its
// BEGIN IMMEDIATE claim while leaving unrelated committed state untouched.
func TestOperationClaimReleasedOnProcessTermination(t *testing.T) {
	if os.Getenv(operationClaimHelperEnv) == "1" {
		path := os.Getenv(operationClaimPathEnv)
		conn, err := db.Open("file:" + path)
		if err != nil {
			t.Fatalf("open helper database: %v", err)
		}
		defer conn.Close()
		store := db.NewStore(conn)
		err = db.WithOperationClaim(context.Background(), store, func(db.Querier) error {
			_, _ = fmt.Fprintln(os.Stdout, "OPERATION_CLAIM_HELD")
			select {}
		})
		t.Fatalf("helper claim returned unexpectedly: %v", err)
	}

	path := filepath.Join(t.TempDir(), "operation-claim.sqlite")
	conn, err := db.Open("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	store := db.NewStore(conn)

	command := exec.Command(os.Args[0], "-test.run=^TestOperationClaimReleasedOnProcessTermination$")
	command.Env = append(os.Environ(), operationClaimHelperEnv+"=1", operationClaimPathEnv+"="+path)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if strings.TrimSpace(scanner.Text()) == "OPERATION_CLAIM_HELD" {
				line <- scanner.Text()
				return
			}
		}
		line <- ""
	}()
	select {
	case marker := <-line:
		if marker == "" {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatal("claim helper exited before acquiring its SQLite operation claim")
		}
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal("timed out waiting for subprocess operation claim")
	}

	acquired := make(chan error, 1)
	go func() {
		acquired <- db.WithOperationClaim(context.Background(), store, func(db.Querier) error { return nil })
	}()
	select {
	case err := <-acquired:
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("second process acquired the claim before owner termination: %v", err)
	case <-time.After(75 * time.Millisecond):
	}

	if err := command.Process.Kill(); err != nil {
		t.Fatalf("terminate claim owner subprocess: %v", err)
	}
	_ = command.Wait()
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("claim was not released after process termination: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SQLite operation claim remained held after owner process termination")
	}
}
