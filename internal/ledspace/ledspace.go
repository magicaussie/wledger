// Package ledspace tracks the coordinate system of stored bin LED indices.
//
// The state is persisted in the system_flags table so it survives application
// restarts. It exists because a restored backup may not carry enough metadata
// to establish whether its bins.led_index values are segment-relative, and
// silently interpreting them would address the wrong physical LEDs.
package ledspace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
)

const (
	// FlagKey is the system_flags key holding the coordinate-space state.
	FlagKey = "led_coordinate_space"
	// Segment means bin LED indices are segment-relative (absolute within their
	// WLED segment). This is the historical and default coordinate system.
	Segment = "segment"
	// Drawer means bin LED indices are relative to their owning drawer's
	// segment-relative allocation (containers.led_start/led_count).
	Drawer = "drawer"
	// Unresolved means the coordinate system could not be established (for
	// example a restored backup without an explicit coordinate-space marker).
	Unresolved = "unresolved"
)

// Current returns the persisted coordinate space of stored bin LED indices.
//
// An absent flag means Segment, which is the state of all data that has never
// been restored from an unmarked backup. A persisted value that is not one of
// the known spaces is rejected rather than silently treated as segment, so an
// unrecognised state can never be mistaken for a safe, resolved one.
func Current(ctx context.Context, q db.Querier) (string, error) {
	v, err := q.GetFlag(ctx, FlagKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Segment, nil
		}
		return "", err
	}
	switch v {
	case Segment, Drawer, Unresolved:
		return v, nil
	default:
		return "", fmt.Errorf("unknown LED coordinate space %q", v)
	}
}

// IsUnresolved reports whether stored bin LED indices are in an unresolved
// coordinate space. An unknown persisted value is reported as an error rather
// than being treated as resolved.
func IsUnresolved(ctx context.Context, q db.Querier) (bool, error) {
	space, err := Current(ctx, q)
	if err != nil {
		return false, err
	}
	return space == Unresolved, nil
}

// Set persists the coordinate-space state. Only known spaces are accepted so an
// unrecognised value can never be written and later mistaken for a resolved
// state.
func Set(ctx context.Context, q db.Querier, space string) error {
	switch space {
	case Segment, Drawer, Unresolved:
	default:
		return fmt.Errorf("unknown LED coordinate space %q", space)
	}
	return q.SetFlag(ctx, db.SetFlagParams{Key: FlagKey, Value: space})
}
