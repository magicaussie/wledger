package hardware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// ErrInvalidAllocation marks a validation failure in submitted drawer
// allocations or bin mappings. It is a client error (HTTP 400), distinct from a
// genuine internal failure (HTTP 500), and can be matched with errors.Is.
var ErrInvalidAllocation = errors.New("invalid LED allocation")

// ValidateAllocation validates a single drawer LED allocation. Allocations are
// segment-relative: led_start is the first LED of the drawer within its WLED
// segment and led_count is the number of LEDs allocated to it.
func ValidateAllocation(ledStart, ledCount int64) error {
	if ledStart < 0 {
		return fmt.Errorf("led_start must be non-negative, got %d", ledStart)
	}
	if ledCount <= 0 {
		return fmt.Errorf("led_count must be positive, got %d", ledCount)
	}
	if ledStart > math.MaxInt64-ledCount {
		return fmt.Errorf("led allocation overflows: start %d count %d", ledStart, ledCount)
	}
	return nil
}

// ValidateBinMapping validates a single mapped bin against its drawer's
// allocation. ledIndex is the bin's segment-relative start LED and width is its
// LED span, following the existing bin-width semantics (a width below 1 is
// treated as 1). Negative indices, arithmetic overflow and any bin that is not
// fully contained within [start, start+count) are rejected.
func ValidateBinMapping(ledIndex, width, start, count int64) error {
	if ledIndex < 0 {
		return fmt.Errorf("bin led_index must be non-negative, got %d", ledIndex)
	}
	if width < 1 {
		width = 1
	}
	if ledIndex > math.MaxInt64-width {
		return fmt.Errorf("bin LED range overflows: index %d width %d", ledIndex, width)
	}
	if ledIndex < start {
		return fmt.Errorf("bin led_index %d is before allocation start %d", ledIndex, start)
	}
	if ledIndex+width > start+count {
		return fmt.Errorf("bin range [%d,%d) exceeds allocation [%d,%d)", ledIndex, ledIndex+width, start, start+count)
	}
	return nil
}

// ValidateBinMappingInSpace validates a mapped bin against its drawer's
// allocation in the given coordinate space. In segment space the bin index is
// segment-absolute and must lie within [start, start+count); in drawer space it
// is drawer-relative and must lie within [0, count).
func ValidateBinMappingInSpace(space string, ledIndex, width, start, count int64) error {
	switch space {
	case ledspace.Segment:
		return ValidateBinMapping(ledIndex, width, start, count)
	case ledspace.Drawer:
		if ledIndex < 0 {
			return fmt.Errorf("bin led_index must be non-negative, got %d", ledIndex)
		}
		if width < 1 {
			width = 1
		}
		if ledIndex > math.MaxInt64-width {
			return fmt.Errorf("bin LED range overflows: index %d width %d", ledIndex, width)
		}
		if ledIndex+width > count {
			return fmt.Errorf("bin range [%d,%d) exceeds drawer allocation [0,%d)", ledIndex, ledIndex+width, count)
		}
		return nil
	default:
		return fmt.Errorf("unknown LED coordinate space %q", space)
	}
}

// binMappingRow is the containment-relevant view of a submitted or imported
// bin. A nil LedIndex denotes an unmapped bin, which is exempt from containment
// validation and is preserved as-is.
type binMappingRow struct {
	ContainerIndex int
	Name           string
	LedIndex       *int
	Width          int
}

// validateBinMappingsInSpace validates every mapped submitted bin against its
// parent drawer's proposed allocation in the given coordinate space. It rejects
// out-of-range container references and any mapped bin that is not contained
// within its drawer's allocation. Unmapped bins (nil LED index) are allowed and
// are not given a coordinate.
func validateBinMappingsInSpace(space string, bins []binMappingRow, containers []db.Container) error {
	for _, b := range bins {
		if b.ContainerIndex < 0 || b.ContainerIndex >= len(containers) {
			return fmt.Errorf("bin %q references invalid container index %d", b.Name, b.ContainerIndex)
		}
		if b.LedIndex == nil {
			continue // unmapped bin
		}
		c := containers[b.ContainerIndex]
		if err := ValidateBinMappingInSpace(space, int64(*b.LedIndex), int64(clampWidth(b.Width)), c.LedStart, c.LedCount); err != nil {
			return fmt.Errorf("bin %q in drawer %q: %w", b.Name, c.Name, err)
		}
	}
	return nil
}

// ValidateRestoredAllocationRanges validates the drawer allocations present in a
// restore manifest: each explicit allocation must be well-formed and must not
// overlap another allocation in the same segment. It does not validate bin
// containment, which requires a known bin coordinate space. Allocations are
// always segment-relative, so this check is space-independent.
func ValidateRestoredAllocationRanges(containers []db.Container) error {
	allocated := make([]db.Container, 0, len(containers))
	for _, c := range containers {
		if c.LedCount > 0 {
			allocated = append(allocated, c)
		}
	}
	return ValidateAllocations(allocated)
}

// ValidateRestoredAllocations checks the drawer allocations and mapped-bin
// containment carried by a restore manifest. It reports whether the backup
// carries explicit allocations (modern). Legacy backups without allocations are
// not validated against a derived layout: they are left untouched so they
// remain eligible for a safe post-restore allocation backfill, and no guessed
// allocations are assigned here.
func ValidateRestoredAllocations(containers []db.Container, bins []db.Bin) (bool, error) {
	allocated := make([]db.Container, 0, len(containers))
	for _, c := range containers {
		if c.LedCount > 0 {
			allocated = append(allocated, c)
		}
	}
	if len(allocated) == 0 {
		return false, nil // legacy backup: no explicit allocations to validate
	}
	if err := ValidateAllocations(allocated); err != nil {
		return true, err
	}

	byID := make(map[int64]db.Container, len(containers))
	for _, c := range containers {
		byID[c.ID] = c
	}
	for _, b := range bins {
		if !b.LedIndex.Valid {
			continue // unmapped bin
		}
		c, ok := byID[b.ContainerID]
		if !ok {
			return true, fmt.Errorf("bin %d references missing container %d", b.ID, b.ContainerID)
		}
		if c.LedCount <= 0 {
			return true, fmt.Errorf("bin %d in drawer %q is mapped but the drawer has no allocation", b.ID, c.Name)
		}
		width := b.Width.Int64
		if !b.Width.Valid || width < 1 {
			width = 1
		}
		if err := ValidateBinMapping(b.LedIndex.Int64, width, c.LedStart, c.LedCount); err != nil {
			return true, fmt.Errorf("bin %d in drawer %q: %w", b.ID, c.Name, err)
		}
	}
	return true, nil
}

// ValidateRestoredDrawerAllocations checks a drawer-relative restore manifest:
// every drawer allocation must be well-formed and non-overlapping, and every
// mapped bin's drawer-relative index must lie within its drawer's allocation. A
// mapped bin whose drawer has no allocation is rejected, because a
// drawer-relative index has no meaning without an allocation to be relative to.
func ValidateRestoredDrawerAllocations(containers []db.Container, bins []db.Bin) error {
	allocated := make([]db.Container, 0, len(containers))
	for _, c := range containers {
		if c.LedCount > 0 {
			allocated = append(allocated, c)
		}
	}
	if err := ValidateAllocations(allocated); err != nil {
		return err
	}

	byID := make(map[int64]db.Container, len(containers))
	for _, c := range containers {
		byID[c.ID] = c
	}
	for _, b := range bins {
		if !b.LedIndex.Valid {
			continue // unmapped bin
		}
		c, ok := byID[b.ContainerID]
		if !ok {
			return fmt.Errorf("bin %d references missing container %d", b.ID, b.ContainerID)
		}
		if c.LedCount <= 0 {
			return fmt.Errorf("bin %d in drawer %q is drawer-relative but the drawer has no allocation", b.ID, c.Name)
		}
		width := b.Width.Int64
		if !b.Width.Valid || width < 1 {
			width = 1
		}
		if err := ValidateBinMappingInSpace(ledspace.Drawer, b.LedIndex.Int64, width, c.LedStart, c.LedCount); err != nil {
			return fmt.Errorf("bin %d in drawer %q: %w", b.ID, c.Name, err)
		}
	}
	return nil
}

// ValidateAllocations validates a set of drawer allocations, ensuring each is
// well-formed and that allocations do not overlap within a segment.
func ValidateAllocations(containers []db.Container) error {
	type span struct {
		name       string
		start, end int64
	}
	bySegment := map[int64][]span{}
	for _, c := range containers {
		if err := ValidateAllocation(c.LedStart, c.LedCount); err != nil {
			return fmt.Errorf("container %q: %w", c.Name, err)
		}
		bySegment[c.SegmentID] = append(bySegment[c.SegmentID], span{c.Name, c.LedStart, c.LedStart + c.LedCount})
	}
	for seg, spans := range bySegment {
		sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
		for i := 1; i < len(spans); i++ {
			if spans[i].start < spans[i-1].end {
				return fmt.Errorf("overlapping LED allocations in segment %d: %q [%d,%d) and %q [%d,%d)",
					seg, spans[i-1].name, spans[i-1].start, spans[i-1].end, spans[i].name, spans[i].start, spans[i].end)
			}
		}
	}
	return nil
}

// resolveAllocations fills in missing allocations (backward compatible with
// clients that do not send led_start/led_count) using the submitted order, then
// validates the resulting set. A container with led_count <= 0 is treated as
// unallocated and derives its allocation from its layout capacity. It returns
// the resolved allocations so callers can validate bin containment against the
// proposed (not previously persisted) allocations.
func resolveAllocations(containers []containerInJSON) ([]db.Container, error) {
	segEnd := map[int64]int64{}
	resolved := make([]db.Container, 0, len(containers))
	for i := range containers {
		c := &containers[i]
		if c.LedCount <= 0 {
			c.LedCount = c.Config.Length()
			c.LedStart = segEnd[c.SegmentID]
		}
		resolved = append(resolved, db.Container{
			Name:      c.Name,
			SegmentID: c.SegmentID,
			LedStart:  c.LedStart,
			LedCount:  c.LedCount,
		})
		if end := c.LedStart + c.LedCount; end > segEnd[c.SegmentID] {
			segEnd[c.SegmentID] = end
		}
	}
	return resolved, ValidateAllocations(resolved)
}

// AllocationClass classifies a drawer's mapped bins against its proposed
// allocation.
type AllocationClass string

const (
	// AllocationClean: the drawer has mapped bins, all within the allocation.
	AllocationClean AllocationClass = "clean"
	// AllocationEmpty: the drawer has no mapped bins (allocation still valid).
	AllocationEmpty AllocationClass = "empty"
	// AllocationInconsistent: the mapping cannot be reconciled with a proposed
	// allocation without guessing.
	AllocationInconsistent AllocationClass = "inconsistent"
)

// AllocationFinding is one drawer's preflight result.
type AllocationFinding struct {
	ContainerID   int64
	ContainerName string
	SegmentID     int64
	ProposedStart int64
	ProposedCount int64
	Class         AllocationClass
	Reason        string
}

// AllocationReport summarises a preflight run.
type AllocationReport struct {
	Findings     []AllocationFinding
	Clean        int
	Empty        int
	Inconsistent int
}

func (r *AllocationReport) add(f AllocationFinding) {
	r.Findings = append(r.Findings, f)
	switch f.Class {
	case AllocationClean:
		r.Clean++
	case AllocationEmpty:
		r.Empty++
	case AllocationInconsistent:
		r.Inconsistent++
	}
}

// PreflightDrawerAllocations derives proposed drawer allocations using the
// current UI ordering (position_index, id) and validates every mapped bin
// against them. It is strictly read-only and never alters stored bin indices.
// Partially populated drawers are allowed: a drawer is clean as long as every
// mapped bin lies within its proposed allocation.
func PreflightDrawerAllocations(ctx context.Context, store db.Store) (AllocationReport, error) {
	var report AllocationReport
	controllers, err := store.GetControllers(ctx)
	if err != nil {
		return report, err
	}
	for _, ctrl := range controllers {
		containers, err := store.GetContainersByController(ctx, ctrl.ID)
		if err != nil {
			return report, err
		}
		segOffset := map[int64]int64{}
		segBroken := map[int64]bool{}
		for _, c := range containers {
			if segBroken[c.SegmentID] {
				report.add(AllocationFinding{
					ContainerID: c.ID, ContainerName: c.Name, SegmentID: c.SegmentID,
					Class:  AllocationInconsistent,
					Reason: "preceding container in segment has indeterminate length",
				})
				continue
			}
			length, err := mapper.GetContainerLength(c)
			if err != nil {
				report.add(AllocationFinding{
					ContainerID: c.ID, ContainerName: c.Name, SegmentID: c.SegmentID,
					Class: AllocationInconsistent, Reason: "malformed config: " + err.Error(),
				})
				segBroken[c.SegmentID] = true
				continue
			}
			if length <= 0 {
				report.add(AllocationFinding{
					ContainerID: c.ID, ContainerName: c.Name, SegmentID: c.SegmentID,
					Class: AllocationInconsistent, Reason: "layout has no LEDs (indeterminate length)",
				})
				segBroken[c.SegmentID] = true
				continue
			}
			start := segOffset[c.SegmentID]
			segOffset[c.SegmentID] = start + length

			bins, err := store.GetBinsByContainer(ctx, c.ID)
			if err != nil {
				return report, err
			}
			class, reason := classifyBins(bins, start, length)
			report.add(AllocationFinding{
				ContainerID: c.ID, ContainerName: c.Name, SegmentID: c.SegmentID,
				ProposedStart: start, ProposedCount: length, Class: class, Reason: reason,
			})
		}
	}
	return report, nil
}

// classifyBins reports whether a drawer's mapped bins fit within [start,start+count).
func classifyBins(bins []db.Bin, start, count int64) (AllocationClass, string) {
	mapped := 0
	for _, b := range bins {
		if !b.LedIndex.Valid {
			continue
		}
		mapped++
		idx := b.LedIndex.Int64
		w := b.Width.Int64
		if w < 1 {
			w = 1
		}
		if idx < start {
			return AllocationInconsistent, fmt.Sprintf("bin %d index %d is before allocation start %d", b.ID, idx, start)
		}
		if idx+w > start+count {
			return AllocationInconsistent, fmt.Sprintf("bin %d range [%d,%d) exceeds allocation [%d,%d)", b.ID, idx, idx+w, start, start+count)
		}
	}
	if mapped == 0 {
		return AllocationEmpty, ""
	}
	return AllocationClean, ""
}

// BackfillDrawerAllocations derives and persists drawer LED allocations for
// containers that do not yet have one, using the current UI ordering. It is
// transactional and idempotent (guarded by a system flag). Containers whose
// mappings are inconsistent are left unallocated and reported; their stored bin
// indices are never altered.
func BackfillDrawerAllocations(ctx context.Context, store db.Store, logger *slog.Logger) error {
	// Allocations are derived from segment-relative bin indices using the UI
	// ordering. Drawer-relative indices are already relative to an allocation, and
	// unresolved indices have no known coordinate system, so neither can be used
	// to derive a meaningful allocation range.
	space, err := ledspace.Current(ctx, store)
	if err != nil {
		return fmt.Errorf("failed to read LED coordinate space: %w", err)
	}
	if space != ledspace.Segment {
		logger.Warn("skipping drawer allocation backfill: bin LED indices are not segment-relative", "space", space)
		return nil
	}

	const flagKey = "drawer_allocation_backfilled"
	if flag, err := store.GetFlag(ctx, flagKey); err == nil && flag == "true" {
		// Already attempted. Re-run only if unallocated drawers exist (e.g. after
		// restoring an older backup that predates drawer allocations).
		if n, err := store.CountUnallocatedContainers(ctx); err == nil && n == 0 {
			return nil
		}
	}

	report, err := PreflightDrawerAllocations(ctx, store)
	if err != nil {
		return fmt.Errorf("allocation preflight failed: %w", err)
	}

	err = store.ExecTx(ctx, func(q db.Querier) error {
		for _, f := range report.Findings {
			if f.Class == AllocationInconsistent {
				continue
			}
			c, err := q.GetContainer(ctx, f.ContainerID)
			if err != nil {
				return fmt.Errorf("failed to load container %d: %w", f.ContainerID, err)
			}
			if c.LedCount > 0 {
				continue // already allocated; never overwrite
			}
			if err := q.UpdateContainerAllocation(ctx, db.UpdateContainerAllocationParams{
				LedStart: f.ProposedStart,
				LedCount: f.ProposedCount,
				ID:       f.ContainerID,
			}); err != nil {
				return fmt.Errorf("failed to set allocation for container %d: %w", f.ContainerID, err)
			}
		}
		return q.SetFlag(ctx, db.SetFlagParams{Key: flagKey, Value: "true"})
	})
	if err != nil {
		return fmt.Errorf("allocation backfill failed: %w", err)
	}

	logger.Info("drawer LED allocation backfill complete",
		"clean", report.Clean, "empty", report.Empty, "inconsistent", report.Inconsistent)
	for _, f := range report.Findings {
		if f.Class == AllocationInconsistent {
			logger.Warn("drawer allocation not derived (inconsistent mapping)",
				"container_id", f.ContainerID, "container", f.ContainerName,
				"segment_id", f.SegmentID, "reason", f.Reason)
		}
	}
	return nil
}
