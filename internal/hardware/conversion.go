package hardware

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// ErrConversionUnresolved indicates that the LED coordinate space is unresolved,
// so a conversion cannot be performed. It is distinct from a refusal: an
// unresolved space has no known coordinate system to convert from.
var ErrConversionUnresolved = errors.New("cannot convert bin coordinates: LED coordinate space is unresolved")

// ConversionOutcome describes the result of a conversion attempt.
type ConversionOutcome string

const (
	// ConversionConverted: the conversion ran and committed.
	ConversionConverted ConversionOutcome = "converted"
	// ConversionAlreadyDrawer: the database was already drawer-relative; no
	// changes were made.
	ConversionAlreadyDrawer ConversionOutcome = "already_drawer"
	// ConversionRefused: validation failed; no changes were made.
	ConversionRefused ConversionOutcome = "refused"
	// ConversionStale: the database state no longer matches the reviewed
	// preflight, so the confirmation is rejected and no changes are made. The
	// administrator must review a fresh preview.
	ConversionStale ConversionOutcome = "stale"
)

// ConversionClass classifies one drawer's convertibility.
type ConversionClass string

const (
	// ConversionConvertible: the drawer's mapped bins all fall within its
	// allocation and can be converted.
	ConversionConvertible ConversionClass = "convertible"
	// ConversionBlocked: the drawer cannot be converted without guessing.
	ConversionBlocked ConversionClass = "blocked"
)

// ConversionBinChange is one proposed (or applied) bin index change.
type ConversionBinChange struct {
	BinID       int64
	ContainerID int64
	FromIndex   int64 // segment-relative
	ToIndex     int64 // drawer-relative
	Width       int64 // LED span, preserved across conversion
}

// ConversionFinding is one drawer's preflight result.
type ConversionFinding struct {
	ContainerID   int64
	ContainerName string
	SegmentID     int64
	LedStart      int64
	LedCount      int64
	Class         ConversionClass
	Reason        string
	Bins          []ConversionBinChange
}

// ConversionReport summarises a preflight run.
type ConversionReport struct {
	// Space is the coordinate space the report was produced against.
	Space string
	// TotalDrawers is the number of drawers considered (including empty and
	// blocked drawers).
	TotalDrawers int
	Findings     []ConversionFinding
	Convertible  int
	Blocked      int
	AffectedBins int
}

func (r *ConversionReport) add(f ConversionFinding) {
	r.Findings = append(r.Findings, f)
	r.TotalDrawers++
	switch f.Class {
	case ConversionConvertible:
		r.Convertible++
	case ConversionBlocked:
		r.Blocked++
	}
	r.AffectedBins += len(f.Bins)
}

// Fingerprint returns a deterministic digest of the report's conversion-relevant
// state. It is stable across runs for the same database state (findings and bins
// are canonicalised by id before hashing) and changes whenever any drawer
// allocation, bin mapping, contradiction or blocking reason changes. It is used
// to bind a confirmation to the exact preflight the administrator reviewed.
func (r ConversionReport) Fingerprint() string {
	h := sha256.New()
	fmt.Fprintf(h, "space=%s\n", r.Space)

	findings := make([]ConversionFinding, len(r.Findings))
	copy(findings, r.Findings)
	sort.Slice(findings, func(i, j int) bool { return findings[i].ContainerID < findings[j].ContainerID })

	for _, f := range findings {
		fmt.Fprintf(h, "drawer=%d;segment=%d;start=%d;count=%d;class=%s;reason=%s\n",
			f.ContainerID, f.SegmentID, f.LedStart, f.LedCount, f.Class, f.Reason)
		bins := make([]ConversionBinChange, len(f.Bins))
		copy(bins, f.Bins)
		sort.Slice(bins, func(i, j int) bool { return bins[i].BinID < bins[j].BinID })
		for _, b := range bins {
			fmt.Fprintf(h, "bin=%d;container=%d;from=%d;to=%d;width=%d\n", b.BinID, b.ContainerID, b.FromIndex, b.ToIndex, b.Width)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ConversionResult is the outcome of a conversion attempt.
type ConversionResult struct {
	Outcome       ConversionOutcome
	ConvertedBins int
	Report        ConversionReport
}

// PreflightConversion produces a read-only report of what a conversion would do.
// It never modifies data. When the coordinate space is not segment-relative the
// report is returned with no findings, because there is nothing to convert.
func PreflightConversion(ctx context.Context, store db.Store) (ConversionReport, error) {
	var report ConversionReport
	err := store.ExecTx(ctx, func(q db.Querier) error {
		space, err := ledspace.Current(ctx, q)
		if err != nil {
			return err
		}
		report.Space = space
		if space != ledspace.Segment {
			return nil
		}
		r, err := buildConversionReport(ctx, q)
		if err != nil {
			return err
		}
		report = r
		return nil
	})
	if err != nil {
		return ConversionReport{}, err
	}
	return report, nil
}

// ConvertToDrawerRelative converts every mapped bin's led_index from
// segment-relative to drawer-relative (drawer_index = segment_index -
// container.led_start) and sets led_coordinate_space to "drawer", atomically.
//
// The conversion is global and all-or-nothing. If any drawer's allocation is
// invalid or overlapping, or any mapped bin falls outside its drawer's
// allocation, the whole conversion is refused and no data is modified. Unmapped
// bins (NULL led_index) are left NULL. If the database is already drawer-relative
// the call is a no-op that reports ConversionAlreadyDrawer; an unresolved or
// unknown coordinate space is rejected with an error.
//
// The transaction acquires a write reservation (BEGIN IMMEDIATE) before reading
// the conversion snapshot, so the snapshot cannot be invalidated by a concurrent
// writer and no other writer can commit until the conversion finishes.
func ConvertToDrawerRelative(ctx context.Context, store db.Store, logger *slog.Logger) (ConversionResult, error) {
	return convertToDrawerRelative(ctx, store, "", logger)
}

// ConvertToDrawerRelativeConfirmed is ConvertToDrawerRelative with a stale-state
// guard: expectedFingerprint is the fingerprint of the preflight the
// administrator reviewed. The entire preflight is recalculated inside the same
// write transaction that would perform the conversion; if the freshly computed
// fingerprint differs, the confirmation is rejected with ConversionStale and no
// data is modified. An empty expectedFingerprint disables the check (used by the
// unconfirmed entry point and by tests).
func ConvertToDrawerRelativeConfirmed(ctx context.Context, store db.Store, expectedFingerprint string, logger *slog.Logger) (ConversionResult, error) {
	return convertToDrawerRelative(ctx, store, expectedFingerprint, logger)
}

func convertToDrawerRelative(ctx context.Context, store db.Store, expectedFingerprint string, logger *slog.Logger) (ConversionResult, error) {
	var result ConversionResult
	err := store.ExecImmediateTx(ctx, func(q db.Querier) error {
		space, err := ledspace.Current(ctx, q)
		if err != nil {
			return fmt.Errorf("failed to read LED coordinate space: %w", err)
		}
		switch space {
		case ledspace.Drawer:
			result.Outcome = ConversionAlreadyDrawer
			return nil
		case ledspace.Unresolved:
			return ErrConversionUnresolved
		case ledspace.Segment:
			// proceed
		default:
			return fmt.Errorf("unknown LED coordinate space %q", space)
		}

		report, err := buildConversionReport(ctx, q)
		if err != nil {
			return err
		}
		result.Report = report

		// Bind the conversion to the reviewed preflight. Revalidated against the
		// freshly built report inside the write transaction, so a database change
		// committed since the preview cannot be converted against a stale snapshot.
		if expectedFingerprint != "" && report.Fingerprint() != expectedFingerprint {
			result.Outcome = ConversionStale
			return nil
		}

		if report.Blocked > 0 {
			result.Outcome = ConversionRefused
			return nil
		}

		for _, f := range report.Findings {
			if len(f.Bins) == 0 {
				continue
			}
			// Clear the drawer's indices first so the (smaller) drawer-relative
			// indices can never transiently collide with a not-yet-updated
			// segment index under UNIQUE(container_id, led_index).
			if err := q.ClearContainerBinLedIndices(ctx, f.ContainerID); err != nil {
				return fmt.Errorf("failed to clear bin indices for container %d: %w", f.ContainerID, err)
			}
			for _, b := range f.Bins {
				if err := q.UpdateBinLedIndex(ctx, db.UpdateBinLedIndexParams{
					LedIndex: sql.NullInt64{Int64: b.ToIndex, Valid: true},
					ID:       b.BinID,
				}); err != nil {
					return fmt.Errorf("failed to convert bin %d: %w", b.BinID, err)
				}
			}
		}

		if err := ledspace.Set(ctx, q, ledspace.Drawer); err != nil {
			return fmt.Errorf("failed to set LED coordinate space: %w", err)
		}
		// The audit entry is written in the same transaction as the conversion, and a
		// failure to write it aborts the whole conversion, so the operation and its
		// audit trail can never diverge (no false success, no partial conversion).
		if err := audit.LogTx(ctx, q, "UPDATE", "HARDWARE", 0, "Converted bin LED indices to drawer-relative", nil, map[string]any{
			"drawers": report.Convertible,
			"bins":    report.AffectedBins,
			"space":   ledspace.Drawer,
		}); err != nil {
			return fmt.Errorf("failed to write conversion audit entry: %w", err)
		}
		result.Outcome = ConversionConverted
		result.ConvertedBins = report.AffectedBins
		return nil
	})
	if err != nil {
		return ConversionResult{}, err
	}

	if logger != nil {
		switch result.Outcome {
		case ConversionConverted:
			logger.Info("converted bin LED indices to drawer-relative",
				"bins", result.ConvertedBins, "drawers", result.Report.Convertible)
		case ConversionAlreadyDrawer:
			logger.Info("bin LED indices already drawer-relative; conversion skipped")
		case ConversionRefused:
			logger.Warn("bin LED index conversion refused", "blocked_drawers", result.Report.Blocked)
		case ConversionStale:
			logger.Warn("bin LED index conversion rejected: reviewed preview is stale")
		}
	}
	return result, nil
}

// buildConversionReport classifies every drawer and proposes the index change
// for each mapped bin. It performs no writes.
func buildConversionReport(ctx context.Context, q db.Querier) (ConversionReport, error) {
	report := ConversionReport{Space: ledspace.Segment}

	controllers, err := q.GetControllers(ctx)
	if err != nil {
		return report, err
	}

	type drawer struct {
		container db.Container
		bins      []db.Bin
	}
	var drawers []drawer
	for _, ctrl := range controllers {
		containers, err := q.GetContainersByController(ctx, ctrl.ID)
		if err != nil {
			return report, err
		}
		for _, c := range containers {
			bins, err := q.GetBinsByContainer(ctx, c.ID)
			if err != nil {
				return report, err
			}
			drawers = append(drawers, drawer{container: c, bins: bins})
		}
	}

	// Detect overlapping allocations within each segment.
	overlap := map[int64]bool{}
	bySegment := map[int64][]db.Container{}
	for _, d := range drawers {
		if d.container.LedCount > 0 {
			bySegment[d.container.SegmentID] = append(bySegment[d.container.SegmentID], d.container)
		}
	}
	for _, cs := range bySegment {
		sort.Slice(cs, func(i, j int) bool { return cs[i].LedStart < cs[j].LedStart })
		for i := 1; i < len(cs); i++ {
			if cs[i].LedStart < cs[i-1].LedStart+cs[i-1].LedCount {
				overlap[cs[i].ID] = true
				overlap[cs[i-1].ID] = true
			}
		}
	}

	for _, d := range drawers {
		f := ConversionFinding{
			ContainerID:   d.container.ID,
			ContainerName: d.container.Name,
			SegmentID:     d.container.SegmentID,
			LedStart:      d.container.LedStart,
			LedCount:      d.container.LedCount,
		}

		if d.container.LedCount > 0 {
			if err := ValidateAllocation(d.container.LedStart, d.container.LedCount); err != nil {
				f.Class = ConversionBlocked
				f.Reason = err.Error()
				report.add(f)
				continue
			}
		}
		if overlap[d.container.ID] {
			f.Class = ConversionBlocked
			f.Reason = "overlapping LED allocation in segment"
			report.add(f)
			continue
		}

		var changes []ConversionBinChange
		blocked := false
		for _, b := range d.bins {
			if !b.LedIndex.Valid {
				continue // unmapped bin stays NULL
			}
			if d.container.LedCount <= 0 {
				f.Class = ConversionBlocked
				f.Reason = fmt.Sprintf("bin %d is mapped but the drawer has no allocation", b.ID)
				blocked = true
				break
			}
			width := b.Width.Int64
			if !b.Width.Valid || width < 1 {
				width = 1
			}
			if err := ValidateBinMappingInSpace(ledspace.Segment, b.LedIndex.Int64, width, d.container.LedStart, d.container.LedCount); err != nil {
				f.Class = ConversionBlocked
				f.Reason = fmt.Sprintf("bin %d: %v", b.ID, err)
				blocked = true
				break
			}
			changes = append(changes, ConversionBinChange{
				BinID:       b.ID,
				ContainerID: d.container.ID,
				FromIndex:   b.LedIndex.Int64,
				ToIndex:     b.LedIndex.Int64 - d.container.LedStart,
				Width:       width,
			})
		}
		if blocked {
			report.add(f)
			continue
		}
		f.Class = ConversionConvertible
		f.Bins = changes
		report.add(f)
	}
	return report, nil
}
