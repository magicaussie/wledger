package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestExecTx(t *testing.T) {
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer conn.Close()

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	store := db.NewStore(conn)

	ctx := context.Background()

	t.Run("successful transaction", func(t *testing.T) {
		err := store.ExecTx(ctx, func(q db.Querier) error {
			_, err := q.CreateUser(ctx, db.CreateUserParams{
				Email:        "tx_success@test.com",
				PasswordHash: "hash",
				Role:         "admin",
			})
			return err
		})

		if err != nil {
			t.Errorf("ExecTx failed: %v", err)
		}

		user, err := store.GetUserByEmail(ctx, "tx_success@test.com")
		if err != nil {
			t.Errorf("failed to find user after success tx: %v", err)
		}
		if user.Email != "tx_success@test.com" {
			t.Errorf("expected email tx_success@test.com, got %s", user.Email)
		}
	})

	t.Run("failed transaction with rollback", func(t *testing.T) {
		err := store.ExecTx(ctx, func(q db.Querier) error {
			_, err := q.CreateUser(ctx, db.CreateUserParams{
				Email:        "tx_rollback@test.com",
				PasswordHash: "hash",
				Role:         "admin",
			})
			if err != nil {
				return err
			}

			return fmt.Errorf("triggered rollback")
		})

		if err == nil {
			t.Error("expected error from ExecTx, got nil")
		}

		_, err = store.GetUserByEmail(ctx, "tx_rollback@test.com")
		if err == nil {
			t.Error("expected user not to exist after rollback")
		}
	})
}

// TestExecImmediateTx verifies that the immediate transaction helper commits on
// success, rolls back on failure, and enforces its write reservation across
// separate connections to the same database.
func TestExecImmediateTx(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + dir + "/immediate.db"
	conn, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	store := db.NewStore(conn)
	ctx := context.Background()

	t.Run("commit", func(t *testing.T) {
		if err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
			_, err := q.CreateUser(ctx, db.CreateUserParams{Email: "imm_ok@test.com", PasswordHash: "h", Role: "admin"})
			return err
		}); err != nil {
			t.Fatalf("ExecImmediateTx: %v", err)
		}
		if _, err := store.GetUserByEmail(ctx, "imm_ok@test.com"); err != nil {
			t.Errorf("user missing after commit: %v", err)
		}
	})

	t.Run("rollback", func(t *testing.T) {
		err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
			if _, err := q.CreateUser(ctx, db.CreateUserParams{Email: "imm_rb@test.com", PasswordHash: "h", Role: "admin"}); err != nil {
				return err
			}
			return fmt.Errorf("triggered rollback")
		})
		if err == nil {
			t.Fatal("expected an error")
		}
		if _, err := store.GetUserByEmail(ctx, "imm_rb@test.com"); err == nil {
			t.Error("user should not exist after rollback")
		}
	})

	t.Run("cross-connection reservation", func(t *testing.T) {
		conn2, err := db.Open(dsn)
		if err != nil {
			t.Fatalf("open second handle: %v", err)
		}
		defer conn2.Close()
		store2 := db.NewStore(conn2)

		locked := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- store.ExecImmediateTx(ctx, func(q db.Querier) error {
				close(locked)
				<-release
				return nil
			})
		}()

		select {
		case <-locked:
		case <-time.After(2 * time.Second):
			t.Fatal("first transaction did not acquire the reservation")
		}

		wrote := make(chan struct{})
		go func() {
			_ = store2.ExecImmediateTx(ctx, func(q db.Querier) error {
				close(wrote)
				return nil
			})
		}()

		select {
		case <-wrote:
			t.Fatal("second connection acquired the write reservation while the first held it")
		case <-time.After(200 * time.Millisecond):
			// Expected: the second connection is blocked on the reservation.
		}

		close(release)
		if err := <-done; err != nil {
			t.Fatalf("first transaction: %v", err)
		}
		select {
		case <-wrote:
		case <-time.After(2 * time.Second):
			t.Fatal("second connection did not proceed after the first released")
		}
	})
}

// TestExecImmediateTx_ConnectionSafety verifies that a failed BEGIN and a
// panicking callback both leave the pooled connection usable, so no transaction
// can leak onto a pooled connection.
func TestExecImmediateTx_ConnectionSafety(t *testing.T) {
	dir := t.TempDir()
	dsn := "file:" + dir + "/safety.db"
	conn, err := db.Open(dsn)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer conn.Close()
	// Pin the pool to a single connection so reuse of the same connection is
	// deterministic.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	// Shorten the busy timeout so the BEGIN-failure case does not wait the full
	// default five seconds.
	if _, err := conn.ExecContext(context.Background(), "PRAGMA busy_timeout = 200"); err != nil {
		t.Fatalf("set busy timeout: %v", err)
	}
	store := db.NewStore(conn)
	ctx := context.Background()

	t.Run("callback panic releases the reservation", func(t *testing.T) {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected the panic to propagate")
				}
			}()
			_ = store.ExecImmediateTx(ctx, func(q db.Querier) error {
				panic("boom")
			})
		}()
		// The single pooled connection must be reusable and unlocked.
		if err := store.ExecImmediateTx(ctx, func(q db.Querier) error { return nil }); err != nil {
			t.Fatalf("reservation leaked after a panicking callback: %v", err)
		}
	})

	t.Run("begin failure leaves the connection reusable", func(t *testing.T) {
		conn2, err := db.Open(dsn)
		if err != nil {
			t.Fatalf("open second handle: %v", err)
		}
		defer conn2.Close()
		store2 := db.NewStore(conn2)

		locked := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- store2.ExecImmediateTx(context.Background(), func(q db.Querier) error {
				close(locked)
				<-release
				return nil
			})
		}()
		select {
		case <-locked:
		case <-time.After(2 * time.Second):
			t.Fatal("holder did not acquire the reservation")
		}

		ctxShort, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
		defer cancel()
		ran := false
		err = store.ExecImmediateTx(ctxShort, func(q db.Querier) error {
			ran = true
			return nil
		})
		if err == nil {
			t.Fatal("expected BEGIN IMMEDIATE to fail while the reservation is held")
		}
		if ran {
			t.Fatal("callback ran despite BEGIN failure")
		}

		close(release)
		if err := <-done; err != nil {
			t.Fatalf("holder transaction: %v", err)
		}

		// The single pooled connection must be reusable.
		if err := store.ExecImmediateTx(ctx, func(q db.Querier) error { return nil }); err != nil {
			t.Fatalf("connection unusable after a failed BEGIN: %v", err)
		}
	})
}
