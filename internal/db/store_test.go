package db_test

import (
	"context"
	"fmt"
	"testing"

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
