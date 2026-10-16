package db

import (
	"context"
	"errors"
)

// LEDQuerier extends sqlc's generated contract with hand-written physical LED
// helpers. Keep this interface outside generated files so regeneration is safe.
type LEDQuerier interface {
	Querier
	PhysicalBinPixelQuerier
	SavePhysicalOperationMetadata(context.Context, PhysicalOperationMetadata) error
	GetPhysicalOperationMetadata(context.Context, string) (PhysicalOperationMetadata, error)
	SavePhysicalOwnershipRange(context.Context, PhysicalOwnershipRange) error
	ListPhysicalOwnershipRanges(context.Context) ([]PhysicalOwnershipRange, error)
	PhysicalEmergencyPending(context.Context) (bool, error)
	FencePhysicalOperations(context.Context) error
	ResolvePhysicalControllerOff(context.Context, int64, string, int64) error
	RecordPhysicalControllerOffFailure(context.Context, int64, string) error
	FinishPhysicalEmergencyOff(context.Context) error
	PhysicalEmergencyID(context.Context) (string, error)
	SavePhysicalEmergencyOutcome(context.Context, PhysicalEmergencyOutcome) error
	ListPhysicalEmergencyOutcomes(context.Context) ([]PhysicalEmergencyOutcome, error)
}

var _ LEDQuerier = (*Queries)(nil)

var ErrLEDQuerierRequired = errors.New("physical LED query support is required")

func RequireLEDQuerier(q Querier) (LEDQuerier, error) {
	led, ok := q.(LEDQuerier)
	if !ok || led == nil {
		return nil, ErrLEDQuerierRequired
	}
	return led, nil
}

// WithLEDOperationClaim preserves the writer claim and cancellation checks of
// WithOperationClaim, and rejects transaction implementations lacking LED helpers.
func WithLEDOperationClaim(ctx context.Context, store Store, fn func(LEDQuerier) error) error {
	if fn == nil {
		return ErrOperationClaimRequired
	}
	return WithOperationClaim(ctx, store, func(q Querier) error {
		led, ok := q.(LEDQuerier)
		if !ok || led == nil {
			return ErrLEDQuerierRequired
		}
		return fn(led)
	})
}

// ExecLEDRead runs LED reads on the transaction-bound query implementation.
func ExecLEDRead(ctx context.Context, store Store, fn func(LEDQuerier) error) error {
	if store == nil || fn == nil {
		return ErrLEDQuerierRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return store.ExecTx(ctx, func(q Querier) error {
		led, ok := q.(LEDQuerier)
		if !ok || led == nil {
			return ErrLEDQuerierRequired
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return fn(led)
	})
}
