package db

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrOperationClaimRequired = errors.New("SQLite operation claim is required for this WLED command")
	ErrPhysicalLocateActive   = errors.New("a durable physical Locate session may still own pixels")
)

// WithOperationClaim reserves SQLite's writer lock before invoking fn. The
// transaction is the cross-process WLEDger operation claim; callers must keep
// synchronous, bounded side effects inside fn and must never detach work from it.
func WithOperationClaim(ctx context.Context, store Store, fn func(Querier) error) error {
	if store == nil || fn == nil {
		return ErrOperationClaimRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return store.ExecImmediateTx(ctx, func(q Querier) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(q); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("operation canceled before claim release: %w", err)
		}
		return nil
	})
}

// HasPhysicalLocateOwnership reports durable pixel ownership. Active session
// states are ownership even when a target's attempt marker is clear; a target
// marker also remains ownership if a legacy/stale session state is encountered.
func HasPhysicalLocateOwnership(ctx context.Context, q Querier) (bool, error) {
	sessions, err := q.ListPhysicalLocateSessions(ctx)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		switch session.State {
		case "activating", "active", "stopping", "partial":
			return true, nil
		}
		targets, err := q.ListPhysicalLocateSessionTargets(ctx, session.SessionID)
		if err != nil {
			return false, err
		}
		for _, target := range targets {
			if target.MayHaveReceived != 0 {
				return true, nil
			}
		}
	}
	return false, nil
}
