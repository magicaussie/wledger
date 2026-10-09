package hardware

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// Migration005FlagKey is the system_flags key recording that the legacy LED
// index conversion (container-relative -> segment-absolute) has been applied.
// It is also set when a known segment-relative backup is restored, so the
// conversion can never run a second time against already-absolute indices.
const Migration005FlagKey = "migration_005_applied"

// MigrateLegacyLedIndices converts bins' relative led_index (per container)
// to absolute segment indices (across containers on same segment).
// This reflects legacy logic but persists it to the database.
func MigrateLegacyLedIndices(ctx context.Context, store db.Store, logger *slog.Logger) error {
	// Never convert bin indices whose coordinate system is unresolved: the
	// restored data may already be segment-relative, and converting again would
	// shift it a second time.
	if unresolved, err := ledspace.IsUnresolved(ctx, store); err != nil {
		return fmt.Errorf("failed to check LED coordinate space: %w", err)
	} else if unresolved {
		logger.Warn("skipping legacy LED index migration: restored LED coordinate space is unresolved")
		return nil
	}

	// Check if migration has already been applied
	flag, err := store.GetFlag(ctx, Migration005FlagKey)
	if err == nil && flag == "true" {
		return nil
	}

	logger.Info("starting legacy LED index migration (Track: Cross-Container Mapping)")

	err = store.ExecTx(ctx, func(q db.Querier) error {
		controllers, err := q.GetControllers(ctx)
		if err != nil {
			return err
		}

		for _, controller := range controllers {
			containers, err := q.GetContainersByController(ctx, controller.ID)
			if err != nil {
				return err
			}

			// Group by segment
			segments := make(map[int64][]db.Container)
			for _, c := range containers {
				segments[c.SegmentID] = append(segments[c.SegmentID], c)
			}

			for _, segContainers := range segments {
				// Sort containers by ID to ensure stable ordering matching legacy logic
				sort.Slice(segContainers, func(i, j int) bool {
					return segContainers[i].ID < segContainers[j].ID
				})

				var offset int64 = 0
				for _, container := range segContainers {
					if offset > 0 {
						bins, err := q.GetBinsByContainer(ctx, container.ID)
						if err != nil {
							return err
						}

						// Update bins with offset
						// To avoid UNIQUE(container_id, led_index) conflicts during sequential updates,
						// update in reverse order of current led_index (since offset is positive).
						for i := len(bins) - 1; i >= 0; i-- {
							bin := bins[i]
							if bin.LedIndex.Valid {
								newIndex := bin.LedIndex.Int64 + offset
								err := q.UpdateBinLedIndex(ctx, db.UpdateBinLedIndexParams{
									LedIndex: sql.NullInt64{Int64: newIndex, Valid: true},
									ID:       bin.ID,
								})
								if err != nil {
									return fmt.Errorf("failed to update bin %d index: %w", bin.ID, err)
								}
							}
						}
					}

					// Update offset for next container in this segment
					length, err := mapper.GetContainerLength(container)
					if err != nil {
						return err
					}
					offset += length
				}
			}
		}

		// Mark migration as applied
		return q.SetFlag(ctx, db.SetFlagParams{
			Key:   Migration005FlagKey,
			Value: "true",
		})
	})

	if err != nil {
		return fmt.Errorf("migration 005 failed: %w", err)
	}

	logger.Info("legacy LED index migration completed successfully")
	return nil
}
