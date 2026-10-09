package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
)

// Store defines all functions to execute db queries and transactions
type Store interface {
	Querier
	ExecTx(ctx context.Context, fn func(Querier) error) error
	// ExecImmediateTx runs fn inside a transaction that acquires a write
	// reservation (BEGIN IMMEDIATE) before fn runs.
	ExecImmediateTx(ctx context.Context, fn func(Querier) error) error
}

// SQLStore provides all functions to execute SQL queries and transactions
type SQLStore struct {
	db *sql.DB
	*Queries
}

// NewStore creates a new store
func NewStore(dbConn *sql.DB) Store {
	return &SQLStore{
		db:      dbConn,
		Queries: New(dbConn),
	}
}

// ExecTx executes a function within a database transaction
func (s *SQLStore) ExecTx(ctx context.Context, fn func(Querier) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	q := New(tx)
	err = fn(q)
	if err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx err: %v, rb err: %v", err, rbErr)
		}
		return err
	}

	return tx.Commit()
}

// ExecImmediateTx runs fn inside a transaction that acquires a write reservation
// (BEGIN IMMEDIATE) before fn runs, on a single connection checked out from the
// pool for the whole transaction.
//
// Unlike ExecTx, which issues a deferred BEGIN and only upgrades to a write lock
// on the first write, ExecImmediateTx takes the write reservation up front. This
// guarantees that a snapshot read inside fn is the latest committed state and
// cannot be invalidated by a concurrent writer, and that no other writer can
// commit until fn finishes. It is intended for operations that must read a
// consistent snapshot and then write based on it (for example the LED
// coordinate-space conversion).
//
// The reservation is enforced by SQLite's own locking (with the connection's
// busy timeout), not by any in-process mutex, so it is safe across multiple
// pooled connections and across processes.
func (s *SQLStore) ExecImmediateTx(ctx context.Context, fn func(Querier) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}

	// If fn panics or otherwise returns without finalizing, release the write
	// reservation before the connection is returned to the pool.
	finalized := false
	defer func() {
		if !finalized {
			_ = rollbackImmediate(ctx, conn, fmt.Errorf("immediate transaction not finalized"))
		}
	}()

	q := New(conn)
	if err := fn(q); err != nil {
		finalized = true
		return rollbackImmediate(ctx, conn, err)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		finalized = true
		return rollbackImmediate(ctx, conn, err)
	}
	finalized = true
	return nil
}

// rollbackImmediate rolls back an immediate transaction. If the rollback itself
// fails the connection is discarded rather than returned to the pool, so a
// half-open transaction can never leak to another caller.
func rollbackImmediate(ctx context.Context, conn *sql.Conn, cause error) error {
	if _, rbErr := conn.ExecContext(ctx, "ROLLBACK"); rbErr != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		return fmt.Errorf("tx err: %v, rb err: %v", cause, rbErr)
	}
	return cause
}
