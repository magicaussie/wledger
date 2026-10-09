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

	"github.com/tuxedocurly/wledger/internal/db"
)

const (
	// FlagKey is the system_flags key holding the coordinate-space state.
	FlagKey = "led_coordinate_space"
	// Segment means bin LED indices are segment-relative.
	Segment = "segment"
	// Unresolved means the coordinate system could not be established (for
	// example a restored backup without an explicit coordinate-space marker).
	Unresolved = "unresolved"
)

// IsUnresolved reports whether stored bin LED indices are in an unresolved
// coordinate space. An absent flag means resolved (segment-relative), which is
// the state for all data that has not been restored from an unmarked backup.
func IsUnresolved(ctx context.Context, q db.Querier) (bool, error) {
	v, err := q.GetFlag(ctx, FlagKey)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return v == Unresolved, nil
}

// Set persists the coordinate-space state.
func Set(ctx context.Context, q db.Querier, space string) error {
	return q.SetFlag(ctx, db.SetFlagParams{Key: FlagKey, Value: space})
}
