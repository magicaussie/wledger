package hardware

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// This file captures a read-only compatibility baseline of the LED data model.
// One consistent read snapshot of the mapping and inventory tables, the LED
// coordinate-space flags and the WLED target every bin would address is bound to
// versioned SHA-256 fingerprints, so a later migration can prove that a layout
// change preserved inventory identities, quantities and physical LED targets.
//
// It adds no writes, no new SQL and no runtime hooks.

// CompatibilityFingerprintVersion identifies the capture and fingerprint scheme.
// The version is part of every hashed payload, so a digest can never be compared
// against a digest produced by a different scheme.
const CompatibilityFingerprintVersion = "1"

// DrawerAllocationBackfilledFlagKey is the system_flags key recorded by
// BackfillDrawerAllocations in internal/hardware/allocation.go. It is repeated
// here because that function keeps the key private.
const DrawerAllocationBackfilledFlagKey = "drawer_allocation_backfilled"

var errCompatibilityInvalidUTF8 = errors.New("compatibility snapshot contains invalid UTF-8")

// CompatibilityCode is the stable identifier of a per-bin target refusal or of a
// diagnostic finding. Refusals and diagnostics share one vocabulary, so a
// consumer can select findings by code without caring which record carries them.
// A code is machine-consumed and never contains prose.
type CompatibilityCode string

const (
	// CodeUnmapped: the bin has no stored LED index (SQL NULL).
	CodeUnmapped CompatibilityCode = "unmapped"
	// CodeUnresolvedSpace: stored indices have no resolved coordinate system.
	CodeUnresolvedSpace CompatibilityCode = "unresolved_coordinate_space"
	// CodeUnknownSpace: the persisted coordinate-space value is not recognised.
	CodeUnknownSpace CompatibilityCode = "unknown_coordinate_space"
	// CodeContainerNotFound: a bin references a container that is not stored.
	CodeContainerNotFound CompatibilityCode = "container_not_found"
	// CodeContainerMissingController: a container references a missing controller.
	CodeContainerMissingController CompatibilityCode = "container_missing_controller"
	// CodeNegativeIndex: the stored LED index is negative.
	CodeNegativeIndex CompatibilityCode = "negative_index"
	// CodeNoDrawerAllocation: a drawer-relative index has no drawer allocation.
	CodeNoDrawerAllocation CompatibilityCode = "no_drawer_allocation"
	// CodeIndexOutsideAllocation: a drawer-relative index is outside its allocation.
	CodeIndexOutsideAllocation CompatibilityCode = "index_outside_allocation"
	// CodeIndexOverflow: adding a drawer's allocation start to its index overflows.
	CodeIndexOverflow CompatibilityCode = "index_overflow"
	// CodeUnclassifiedRefusal: the mapper refused for a condition this capture
	// does not recognise. It is reported rather than mislabelled, so a new mapper
	// refusal cannot pass unnoticed.
	CodeUnclassifiedRefusal CompatibilityCode = "unclassified_refusal"
	// CodeInvalidAllocation: a drawer allocation is not well-formed.
	CodeInvalidAllocation CompatibilityCode = "invalid_allocation"
	// CodeInvalidSegment: the stored WLED segment id is negative.
	CodeInvalidSegment CompatibilityCode = "invalid_segment_id"
	// CodeInvalidControllerCapacity: the controller LED count is negative.
	CodeInvalidControllerCapacity CompatibilityCode = "invalid_controller_capacity"
	// CodeRangeOverflow: a bin's full LED span overflows int64.
	CodeRangeOverflow CompatibilityCode = "range_overflow"
	// CodeRangeOutsideAllocation: a bin's full span is not contained in its
	// drawer's allocation.
	CodeRangeOutsideAllocation CompatibilityCode = "range_outside_allocation"
	// CodeRangeBeyondCapacity: a bin's full span exceeds the controller's LED count.
	CodeRangeBeyondCapacity CompatibilityCode = "range_beyond_controller_capacity"
	// CodeOverlappingBinSpan: two resolved bins address overlapping LED ranges in
	// the same controller segment, including bins in different containers.
	CodeOverlappingBinSpan CompatibilityCode = "overlapping_bin_span"
	// CodeOverlappingAllocation: two drawer allocations overlap in the same
	// controller segment.
	CodeOverlappingAllocation CompatibilityCode = "overlapping_allocation"
	// CodeControllerEndpointMissing: a controller has no stored endpoint address.
	CodeControllerEndpointMissing CompatibilityCode = "controller_endpoint_missing"
	// CodeControllerEndpointInvalid: a controller address cannot form the legacy
	// http://<IpAddress>/json/state endpoint.
	CodeControllerEndpointInvalid CompatibilityCode = "controller_endpoint_invalid"
	// CodeAssignmentMissingPart: a part assignment references a part that is not stored.
	CodeAssignmentMissingPart CompatibilityCode = "assignment_missing_part"
	// CodeAssignmentMissingBin: a part assignment references a bin that is not stored.
	CodeAssignmentMissingBin CompatibilityCode = "assignment_missing_bin"
)

// CompatibilitySeverity classifies a diagnostic. A warning marks a finding that
// can invalidate a compatibility conclusion; info records an intended state.
type CompatibilitySeverity string

const (
	// CompatibilitySeverityInfo records a valid, expected state.
	CompatibilitySeverityInfo CompatibilitySeverity = "info"
	// CompatibilitySeverityWarning records a finding that needs review.
	CompatibilitySeverityWarning CompatibilitySeverity = "warning"
)

// CompatibilityFlag is the exact state of one system_flags row. An absent flag
// (Present false) is distinct from every stored value, including "false" and
// "segment".
type CompatibilityFlag struct {
	Key     string `json:"key"`
	Present bool   `json:"present"`
	Value   string `json:"value"`
}

// CompatibilityFlags carries the flags the compatibility contract depends on.
type CompatibilityFlags struct {
	LedCoordinateSpace         CompatibilityFlag `json:"led_coordinate_space"`
	Migration005Applied        CompatibilityFlag `json:"migration_005_applied"`
	DrawerAllocationBackfilled CompatibilityFlag `json:"drawer_allocation_backfilled"`
}

// CompatibilityRaw preserves the complete generated model rows of one snapshot.
// Values are held exactly as read, including SQL NULL validity, ids, foreign keys
// and row metadata; the slices are canonicalised by id in copies, so the stored
// rows themselves are never mutated.
type CompatibilityRaw struct {
	Controllers     []db.Controller     `json:"controllers"`
	Containers      []db.Container      `json:"containers"`
	Bins            []db.Bin            `json:"bins"`
	Parts           []db.Part           `json:"parts"`
	PartAssignments []db.PartAssignment `json:"part_assignments"`
}

// CompatibilityBinTarget records the WLED target a bin would address, or the
// refusal that prevents it. Start and Count are the would-be range the existing
// mapper hands to wire targeting, including ranges the mapper does not bound;
// they are recorded rather than corrected, and any unsafe span is reported as a
// diagnostic.
type CompatibilityBinTarget struct {
	BinID          int64             `json:"bin_id"`
	ContainerID    int64             `json:"container_id"`
	ControllerID   int64             `json:"controller_id,omitempty"`
	Address        string            `json:"address,omitempty"`
	RawLedIndex    sql.NullInt64     `json:"raw_led_index"`
	RawWidth       sql.NullInt64     `json:"raw_width"`
	EffectiveWidth int64             `json:"effective_width"`
	Resolved       bool              `json:"resolved"`
	SegmentID      int64             `json:"segment_id,omitempty"`
	Start          int64             `json:"start,omitempty"`
	Count          int64             `json:"count,omitempty"`
	Refusal        CompatibilityCode `json:"refusal,omitempty"`
}

// CompatibilityDiagnostic is one structured finding. It carries the code and the
// raw values it was derived from; no prose error message is stored or hashed.
type CompatibilityDiagnostic struct {
	Code             CompatibilityCode     `json:"code"`
	Severity         CompatibilitySeverity `json:"severity"`
	BinID            int64                 `json:"bin_id,omitempty"`
	ContainerID      int64                 `json:"container_id,omitempty"`
	ControllerID     int64                 `json:"controller_id,omitempty"`
	SegmentID        int64                 `json:"segment_id,omitempty"`
	AssignmentID     int64                 `json:"assignment_id,omitempty"`
	PartID           int64                 `json:"part_id,omitempty"`
	Index            int64                 `json:"index,omitempty"`
	Width            int64                 `json:"width,omitempty"`
	Capacity         int64                 `json:"capacity,omitempty"`
	OtherBinID       int64                 `json:"other_bin_id,omitempty"`
	OtherContainerID int64                 `json:"other_container_id,omitempty"`
}

// CompatibilityFingerprint is a versioned SHA-256 digest of one captured domain.
type CompatibilityFingerprint struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

// String renders the fingerprint as "<version>:<digest>", or "" when no
// fingerprint was produced.
func (f CompatibilityFingerprint) String() string {
	if f.Digest == "" {
		return ""
	}
	return f.Version + ":" + f.Digest
}

// CompatibilityBaseline is one read-only snapshot of the LED data model.
type CompatibilityBaseline struct {
	// Version identifies the capture and fingerprint scheme.
	Version string `json:"version"`
	// Space is the coordinate space of stored bin indices: segment when the
	// led_coordinate_space flag is absent, otherwise the stored value.
	Space string `json:"space"`
	// SpaceKnown is false when Space is not a recognised coordinate space.
	SpaceKnown bool `json:"space_known"`
	// LocateBlocked records that bin/part locate and flash operations are refused for
	// this snapshot. The coordinate guard runs before per-bin mapping, so an
	// unknown or unresolved space blocks every bin, including unmapped ones.
	LocateBlocked bool `json:"locate_blocked"`
	// Flags holds the exact flag state captured for this snapshot.
	Flags CompatibilityFlags `json:"flags"`
	// Raw holds the complete model rows captured for this snapshot.
	Raw CompatibilityRaw `json:"raw"`
	// Targets holds one entry per bin, canonicalised by bin id.
	Targets []CompatibilityBinTarget `json:"targets"`
	// Diagnostics holds the structured findings in a deterministic order.
	Diagnostics []CompatibilityDiagnostic `json:"diagnostics"`
	// InventoryFingerprint covers parts, part assignments and bin identities.
	InventoryFingerprint CompatibilityFingerprint `json:"inventory_fingerprint"`
	// MappingFingerprint covers controllers, containers, bins and raw flags.
	MappingFingerprint CompatibilityFingerprint `json:"mapping_fingerprint"`
	// TargetsFingerprint covers the resolved per-bin targets and refusals.
	TargetsFingerprint CompatibilityFingerprint `json:"targets_fingerprint"`
}

// CaptureCompatibilityBaseline reads one consistent snapshot of the LED data
// model inside a single read transaction and binds it to versioned fingerprints.
// It performs no writes, no network calls and no hardware operations.
//
// On a query, transaction or serialization failure it returns the zero baseline
// and a wrapped error: a partial capture is never returned, so a caller cannot
// mistake missing data for compatible data.
func CaptureCompatibilityBaseline(ctx context.Context, store db.Store) (CompatibilityBaseline, error) {
	var baseline CompatibilityBaseline
	err := store.ExecTx(ctx, func(q db.Querier) error {
		raw, flags, err := readCompatibilitySnapshot(ctx, q)
		if err != nil {
			return err
		}
		captured, err := buildCompatibilityBaseline(raw, flags)
		if err != nil {
			return err
		}
		baseline = captured
		return nil
	})
	if err != nil {
		return CompatibilityBaseline{}, fmt.Errorf("capture compatibility baseline: %w", err)
	}
	return baseline, nil
}

// readCompatibilitySnapshot reads every captured row through the given querier,
// so a caller inside a transaction observes one consistent snapshot.
func readCompatibilitySnapshot(ctx context.Context, q db.Querier) (CompatibilityRaw, CompatibilityFlags, error) {
	controllers, err := q.GetControllers(ctx)
	if err != nil {
		return CompatibilityRaw{}, CompatibilityFlags{}, fmt.Errorf("read controllers: %w", err)
	}
	containers, err := q.GetAllContainers(ctx)
	if err != nil {
		return CompatibilityRaw{}, CompatibilityFlags{}, fmt.Errorf("read containers: %w", err)
	}
	bins, err := q.GetAllBins(ctx)
	if err != nil {
		return CompatibilityRaw{}, CompatibilityFlags{}, fmt.Errorf("read bins: %w", err)
	}
	parts, err := q.GetAllParts(ctx)
	if err != nil {
		return CompatibilityRaw{}, CompatibilityFlags{}, fmt.Errorf("read parts: %w", err)
	}
	assignments, err := q.GetAllPartAssignments(ctx)
	if err != nil {
		return CompatibilityRaw{}, CompatibilityFlags{}, fmt.Errorf("read part assignments: %w", err)
	}
	flags, err := readCompatibilityFlags(ctx, q)
	if err != nil {
		return CompatibilityRaw{}, CompatibilityFlags{}, err
	}
	raw := CompatibilityRaw{
		Controllers:     controllers,
		Containers:      containers,
		Bins:            bins,
		Parts:           parts,
		PartAssignments: assignments,
	}
	return raw, flags, nil
}

// readCompatibilityFlags reads the flags the compatibility contract depends on.
func readCompatibilityFlags(ctx context.Context, q db.Querier) (CompatibilityFlags, error) {
	var flags CompatibilityFlags
	for _, target := range []struct {
		key string
		dst *CompatibilityFlag
	}{
		{ledspace.FlagKey, &flags.LedCoordinateSpace},
		{Migration005FlagKey, &flags.Migration005Applied},
		{DrawerAllocationBackfilledFlagKey, &flags.DrawerAllocationBackfilled},
	} {
		flag, err := readCompatibilityFlag(ctx, q, target.key)
		if err != nil {
			return CompatibilityFlags{}, err
		}
		*target.dst = flag
	}
	return flags, nil
}

// readCompatibilityFlag reads one flag, preserving the difference between an
// absent row and a stored value. GetFlag reports an absent key with sql.ErrNoRows.
func readCompatibilityFlag(ctx context.Context, q db.Querier, key string) (CompatibilityFlag, error) {
	value, err := q.GetFlag(ctx, key)
	switch {
	case err == nil:
		return CompatibilityFlag{Key: key, Present: true, Value: value}, nil
	case errors.Is(err, sql.ErrNoRows):
		return CompatibilityFlag{Key: key, Present: false}, nil
	default:
		return CompatibilityFlag{}, fmt.Errorf("read flag %q: %w", key, err)
	}
}

// buildCompatibilityBaseline canonicalises the captured rows, resolves the
// per-bin targets and diagnostics, and binds each domain to a fingerprint. Any
// failure returns the zero baseline so partial data is never certified.
func buildCompatibilityBaseline(raw CompatibilityRaw, flags CompatibilityFlags) (CompatibilityBaseline, error) {
	raw = canonicalRawOrder(raw)
	space, spaceKnown := compatibilitySpace(flags.LedCoordinateSpace)

	containers := make(map[int64]db.Container, len(raw.Containers))
	for _, container := range raw.Containers {
		containers[container.ID] = container
	}
	controllers := make(map[int64]db.Controller, len(raw.Controllers))
	for _, controller := range raw.Controllers {
		controllers[controller.ID] = controller
	}
	view := compatibilityView{
		raw:         raw,
		space:       space,
		spaceKnown:  spaceKnown,
		containers:  containers,
		controllers: controllers,
	}

	inventory, err := FingerprintInventory(raw)
	if err != nil {
		return CompatibilityBaseline{}, err
	}
	mapping, err := FingerprintMapping(raw, flags)
	if err != nil {
		return CompatibilityBaseline{}, err
	}
	targets := view.resolveTargets()
	targetFingerprint, err := FingerprintTargets(targets)
	if err != nil {
		return CompatibilityBaseline{}, err
	}

	return CompatibilityBaseline{
		Version:              CompatibilityFingerprintVersion,
		Space:                space,
		SpaceKnown:           spaceKnown,
		LocateBlocked:        !spaceKnown || space == ledspace.Unresolved,
		Flags:                flags,
		Raw:                  raw,
		Targets:              targets,
		Diagnostics:          view.diagnostics(targets),
		InventoryFingerprint: inventory,
		MappingFingerprint:   mapping,
		TargetsFingerprint:   targetFingerprint,
	}, nil
}

// canonicalRawOrder returns copies of the captured slices sorted by id, so every
// consumer sees the same order regardless of the order the queries returned.
func canonicalRawOrder(raw CompatibilityRaw) CompatibilityRaw {
	controllers := cloneCompatibilityRows(raw.Controllers)
	containers := cloneCompatibilityRows(raw.Containers)
	bins := cloneCompatibilityRows(raw.Bins)
	parts := cloneCompatibilityRows(raw.Parts)
	assignments := cloneCompatibilityRows(raw.PartAssignments)
	sort.Slice(controllers, func(i, j int) bool { return controllers[i].ID < controllers[j].ID })
	sort.Slice(containers, func(i, j int) bool { return containers[i].ID < containers[j].ID })
	sort.Slice(bins, func(i, j int) bool { return bins[i].ID < bins[j].ID })
	sort.Slice(parts, func(i, j int) bool { return parts[i].ID < parts[j].ID })
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].ID < assignments[j].ID })
	return CompatibilityRaw{
		Controllers:     controllers,
		Containers:      containers,
		Bins:            bins,
		Parts:           parts,
		PartAssignments: assignments,
	}
}

// An empty query result is the same row set whether its slice is nil or empty.
func cloneCompatibilityRows[T any](rows []T) []T {
	return append([]T{}, rows...)
}

// compatibilitySpace resolves the coordinate space from the raw flag. The rules
// mirror ledspace.Current: an absent flag means segment, and a value outside the
// known spaces is reported as unknown instead of being treated as segment.
func compatibilitySpace(flag CompatibilityFlag) (string, bool) {
	if !flag.Present {
		return ledspace.Segment, true
	}
	switch flag.Value {
	case ledspace.Segment, ledspace.Drawer, ledspace.Unresolved:
		return flag.Value, true
	default:
		return flag.Value, false
	}
}

// compatibilityView is the canonicalised, read-only view of one snapshot that
// the target and diagnostic builders work from.
type compatibilityView struct {
	raw         CompatibilityRaw
	space       string
	spaceKnown  bool
	containers  map[int64]db.Container
	controllers map[int64]db.Controller
}

// resolveTargets records the WLED target each bin would address, or the refusal
// that prevents it. mapper.CalculateGlobalIndex is the authority for a resolved
// index and only ever reads the captured rows; the legacy width clamp is applied
// exactly as wled.triggerLocate applies it. Refusals are recorded as codes, and
// the mapper's prose is intentionally not retained because nothing consumes it.
func (v compatibilityView) resolveTargets() []CompatibilityBinTarget {
	targets := make([]CompatibilityBinTarget, 0, len(v.raw.Bins))
	for _, bin := range v.raw.Bins {
		target := CompatibilityBinTarget{
			BinID:          bin.ID,
			ContainerID:    bin.ContainerID,
			RawLedIndex:    bin.LedIndex,
			RawWidth:       bin.Width,
			EffectiveWidth: effectiveBinWidth(bin.Width),
		}
		container, hasContainer := v.containers[bin.ContainerID]
		var hasController bool
		if hasContainer {
			target.ControllerID = container.ControllerID
			// Wire targeting addresses a controller by IpAddress; the raw Port is
			// preserved in the mapping fingerprint and is not used here.
			if controller, ok := v.controllers[container.ControllerID]; ok {
				hasController = true
				target.Address = controller.IpAddress
			}
		}
		switch {
		case !v.spaceKnown:
			target.Refusal = CodeUnknownSpace
		case v.space == ledspace.Unresolved:
			target.Refusal = CodeUnresolvedSpace
		case !hasContainer:
			target.Refusal = CodeContainerNotFound
		case !hasController:
			target.Refusal = CodeContainerMissingController
		case !bin.LedIndex.Valid:
			target.Refusal = CodeUnmapped
		default:
			segmentID, globalIndex, err := mapper.CalculateGlobalIndex(v.space, v.raw.Containers, bin)
			if err != nil {
				target.Refusal = classifyRefusal(v.space, bin, container)
			} else {
				target.Resolved = true
				target.SegmentID = segmentID
				target.Start = globalIndex
				target.Count = target.EffectiveWidth
			}
		}
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].BinID < targets[j].BinID })
	return targets
}

// classifyRefusal maps a mapper.CalculateGlobalIndex refusal to a stable code.
// The mapper returns untyped errors, so the conditions it refuses on are
// re-derived here from the captured values in the same order. A refusal this
// function does not recognise is reported as unclassified rather than
// mislabelled, so a new mapper refusal cannot pass unnoticed.
func classifyRefusal(space string, bin db.Bin, container db.Container) CompatibilityCode {
	switch {
	case bin.LedIndex.Int64 < 0:
		return CodeNegativeIndex
	case space == ledspace.Drawer && container.LedCount <= 0:
		return CodeNoDrawerAllocation
	case space == ledspace.Drawer && bin.LedIndex.Int64 >= container.LedCount:
		return CodeIndexOutsideAllocation
	case space == ledspace.Drawer && container.LedStart > math.MaxInt64-bin.LedIndex.Int64:
		return CodeIndexOverflow
	default:
		return CodeUnclassifiedRefusal
	}
}

// effectiveBinWidth applies the legacy bin-width clamp: a stored width below 1,
// including a NULL width read as zero, addresses one LED, exactly as
// wled.triggerLocate clamps it. The stored value is preserved unchanged in
// CompatibilityBinTarget.RawWidth.
func effectiveBinWidth(width sql.NullInt64) int64 {
	if !width.Valid || width.Int64 < 1 {
		return 1
	}
	return width.Int64
}

// diagnostics returns every structured finding of the snapshot in a
// deterministic order.
func (v compatibilityView) diagnostics(targets []CompatibilityBinTarget) []CompatibilityDiagnostic {
	diagnostics := append(v.spaceDiagnostics(), v.containerDiagnostics()...)
	diagnostics = append(diagnostics, v.allocationOverlapDiagnostics()...)
	diagnostics = append(diagnostics, v.controllerEndpointDiagnostics()...)
	diagnostics = append(diagnostics, v.targetDiagnostics(targets)...)
	diagnostics = append(diagnostics, v.binOverlapDiagnostics(targets)...)
	diagnostics = append(diagnostics, v.assignmentDiagnostics()...)
	sort.SliceStable(diagnostics, func(i, j int) bool {
		left, right := diagnostics[i], diagnostics[j]
		if left.Code != right.Code {
			return left.Code < right.Code
		}
		if left.BinID != right.BinID {
			return left.BinID < right.BinID
		}
		if left.ContainerID != right.ContainerID {
			return left.ContainerID < right.ContainerID
		}
		if left.ControllerID != right.ControllerID {
			return left.ControllerID < right.ControllerID
		}
		if left.SegmentID != right.SegmentID {
			return left.SegmentID < right.SegmentID
		}
		if left.OtherBinID != right.OtherBinID {
			return left.OtherBinID < right.OtherBinID
		}
		if left.OtherContainerID != right.OtherContainerID {
			return left.OtherContainerID < right.OtherContainerID
		}
		if left.AssignmentID != right.AssignmentID {
			return left.AssignmentID < right.AssignmentID
		}
		if left.Index != right.Index {
			return left.Index < right.Index
		}
		if left.Width != right.Width {
			return left.Width < right.Width
		}
		return left.Capacity < right.Capacity
	})
	return diagnostics
}

// spaceDiagnostics reports the coordinate-space state that blocks locate and
// flash for the whole snapshot.
func (v compatibilityView) spaceDiagnostics() []CompatibilityDiagnostic {
	if !v.spaceKnown {
		return []CompatibilityDiagnostic{{Code: CodeUnknownSpace, Severity: CompatibilitySeverityWarning}}
	}
	if v.space == ledspace.Unresolved {
		return []CompatibilityDiagnostic{{Code: CodeUnresolvedSpace, Severity: CompatibilitySeverityWarning}}
	}
	return nil
}

// containerDiagnostics reports missing controller relationships and drawer
// allocations that are not well-formed. An unallocated drawer (led_count of zero)
// is a valid state and is not reported.
func (v compatibilityView) containerDiagnostics() []CompatibilityDiagnostic {
	var diagnostics []CompatibilityDiagnostic
	for _, controller := range v.raw.Controllers {
		if controller.LedCount < 0 {
			diagnostics = append(diagnostics, CompatibilityDiagnostic{
				Code:         CodeInvalidControllerCapacity,
				Severity:     CompatibilitySeverityWarning,
				ControllerID: controller.ID,
				Capacity:     controller.LedCount,
			})
		}
	}
	for _, container := range v.raw.Containers {
		if _, ok := v.controllers[container.ControllerID]; !ok {
			diagnostics = append(diagnostics, CompatibilityDiagnostic{
				Code:         CodeContainerMissingController,
				Severity:     CompatibilitySeverityWarning,
				ContainerID:  container.ID,
				ControllerID: container.ControllerID,
			})
		}
		if container.SegmentID < 0 {
			diagnostics = append(diagnostics, CompatibilityDiagnostic{
				Code:        CodeInvalidSegment,
				Severity:    CompatibilitySeverityWarning,
				ContainerID: container.ID,
				Index:       container.SegmentID,
			})
		}
		if container.LedCount != 0 || container.LedStart < 0 {
			if err := ValidateAllocation(container.LedStart, container.LedCount); err != nil {
				diagnostics = append(diagnostics, CompatibilityDiagnostic{
					Code:        CodeInvalidAllocation,
					Severity:    CompatibilitySeverityWarning,
					ContainerID: container.ID,
					Index:       container.LedStart,
					Width:       container.LedCount,
					Capacity:    container.LedCount,
				})
			}
		}
	}
	return diagnostics
}

// compatLedSpan is one half-open LED range [start,end).
type compatLedSpan struct {
	start, end int64
}

// overlappingSpanPairs returns every index pair (i, j), i < j, whose half-open
// spans overlap, over a slice already sorted by start. Every pair is compared,
// not only adjacent spans, so a nested span is detected. Spans that only touch
// (start == end) do not overlap.
func overlappingSpanPairs(spans []compatLedSpan) [][2]int {
	var pairs [][2]int
	for i := range spans {
		for j := i + 1; j < len(spans); j++ {
			if spans[j].start >= spans[i].end {
				break
			}
			pairs = append(pairs, [2]int{i, j})
		}
	}
	return pairs
}

// compatBinSpan is one resolved bin target's span with the ids it belongs to.
type compatBinSpan struct {
	binID       int64
	containerID int64
	span        compatLedSpan
}

// compatAllocationSpan is one container allocation's span with its container id.
type compatAllocationSpan struct {
	containerID int64
	span        compatLedSpan
}

// allocationOverlapDiagnostics reports every pair of positive, valid drawer
// allocations that overlap within the same controller segment. Malformed or
// unallocated containers are skipped because their range cannot be reliably
// compared; they are already reported by containerDiagnostics. Adjacent half-open
// ranges do not overlap, and allocations on different controllers or segments are
// independent.
func (v compatibilityView) allocationOverlapDiagnostics() []CompatibilityDiagnostic {
	groups := map[[2]int64][]compatAllocationSpan{}
	for _, container := range v.raw.Containers {
		if container.LedCount <= 0 {
			continue
		}
		if err := ValidateAllocation(container.LedStart, container.LedCount); err != nil {
			continue
		}
		key := [2]int64{container.ControllerID, container.SegmentID}
		groups[key] = append(groups[key], compatAllocationSpan{
			containerID: container.ID,
			span:        compatLedSpan{start: container.LedStart, end: container.LedStart + container.LedCount},
		})
	}
	var diagnostics []CompatibilityDiagnostic
	for key, entries := range groups {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].span.start != entries[j].span.start {
				return entries[i].span.start < entries[j].span.start
			}
			if entries[i].span.end != entries[j].span.end {
				return entries[i].span.end < entries[j].span.end
			}
			return entries[i].containerID < entries[j].containerID
		})
		spans := make([]compatLedSpan, len(entries))
		for i, entry := range entries {
			spans[i] = entry.span
		}
		for _, pair := range overlappingSpanPairs(spans) {
			first, second := entries[pair[0]], entries[pair[1]]
			diagnostics = append(diagnostics, CompatibilityDiagnostic{
				Code:             CodeOverlappingAllocation,
				Severity:         CompatibilitySeverityWarning,
				ContainerID:      first.containerID,
				OtherContainerID: second.containerID,
				ControllerID:     key[0],
				SegmentID:        key[1],
				Index:            first.span.start,
				Width:            first.span.end - first.span.start,
			})
		}
	}
	return diagnostics
}

// controllerEndpointDiagnostics reports controllers whose stored address cannot
// form the legacy http://<IpAddress>/json/state endpoint. It performs no network
// access, never trims or rewrites the stored address and ignores the legacy Port
// field exactly as wire targeting does.
func (v compatibilityView) controllerEndpointDiagnostics() []CompatibilityDiagnostic {
	var diagnostics []CompatibilityDiagnostic
	for _, controller := range v.raw.Controllers {
		if diagnostic, ok := controllerEndpointDiagnostic(controller); ok {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	return diagnostics
}

// controllerEndpointDiagnostic classifies one controller address against the
// legacy construction http://<IpAddress>/json/state. An empty or blank address is
// reported as missing; an address the construction cannot turn into a real
// nonempty hostname with no credentials, the exact path /json/state and no query
// or fragment is reported as invalid. Valid IPv4, hostname, host:port and
// bracketed IPv6 authorities are preserved.
func controllerEndpointDiagnostic(controller db.Controller) (CompatibilityDiagnostic, bool) {
	diagnostic := CompatibilityDiagnostic{Severity: CompatibilitySeverityWarning, ControllerID: controller.ID}
	if strings.TrimSpace(controller.IpAddress) == "" {
		diagnostic.Code = CodeControllerEndpointMissing
		return diagnostic, true
	}
	endpoint, err := url.Parse("http://" + controller.IpAddress + "/json/state")
	if err != nil || endpoint.User != nil || endpoint.Host == "" || endpoint.Hostname() == "" ||
		endpoint.Path != "/json/state" || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		diagnostic.Code = CodeControllerEndpointInvalid
		return diagnostic, true
	}
	host := endpoint.Hostname()
	if strings.Contains(host, ":") && !strings.HasPrefix(endpoint.Host, "[") {
		diagnostic.Code = CodeControllerEndpointInvalid
		return diagnostic, true
	}
	if strings.HasPrefix(endpoint.Host, "[") {
		literal, _, _ := strings.Cut(host, "%")
		if net.ParseIP(literal) == nil || !strings.Contains(literal, ":") {
			diagnostic.Code = CodeControllerEndpointInvalid
			return diagnostic, true
		}
	}
	if port := endpoint.Port(); port != "" {
		if _, err := strconv.ParseUint(port, 10, 16); err != nil {
			diagnostic.Code = CodeControllerEndpointInvalid
			return diagnostic, true
		}
	}
	return CompatibilityDiagnostic{}, false
}

// binOverlapDiagnostics reports every pair of resolved bins whose legacy addresses
// overlap in the same controller segment, including bins in different containers
// that share an effective target space. Bins the mapper refused, did not resolve,
// or whose span overflows int64 are skipped because their range cannot be reliably
// compared. Adjacent half-open ranges do not overlap, and different controllers or
// segments are independent. The raw and would-be target values are preserved in
// CompatibilityRaw and CompatibilityBinTarget; nothing here derives or repairs them.
func (v compatibilityView) binOverlapDiagnostics(targets []CompatibilityBinTarget) []CompatibilityDiagnostic {
	groups := map[[2]int64][]compatBinSpan{}
	for _, target := range targets {
		if !target.Resolved || target.Count < 1 || target.Start > math.MaxInt64-target.Count {
			continue
		}
		key := [2]int64{target.ControllerID, target.SegmentID}
		groups[key] = append(groups[key], compatBinSpan{
			binID:       target.BinID,
			containerID: target.ContainerID,
			span:        compatLedSpan{start: target.Start, end: target.Start + target.Count},
		})
	}
	var diagnostics []CompatibilityDiagnostic
	for key, entries := range groups {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].span.start != entries[j].span.start {
				return entries[i].span.start < entries[j].span.start
			}
			if entries[i].span.end != entries[j].span.end {
				return entries[i].span.end < entries[j].span.end
			}
			return entries[i].binID < entries[j].binID
		})
		spans := make([]compatLedSpan, len(entries))
		for i, entry := range entries {
			spans[i] = entry.span
		}
		for _, pair := range overlappingSpanPairs(spans) {
			first, second := entries[pair[0]], entries[pair[1]]
			diagnostics = append(diagnostics, CompatibilityDiagnostic{
				Code:             CodeOverlappingBinSpan,
				Severity:         CompatibilitySeverityWarning,
				BinID:            first.binID,
				OtherBinID:       second.binID,
				ContainerID:      first.containerID,
				OtherContainerID: second.containerID,
				ControllerID:     key[0],
				SegmentID:        key[1],
				Index:            first.span.start,
				Width:            first.span.end - first.span.start,
			})
		}
	}
	return diagnostics
}

// assignmentDiagnostics reports part assignments that reference a part or a bin
// the snapshot does not store. A NULL bin_id is legitimate unassigned stock and is
// never reported. Every assignment row and quantity is preserved, and the check
// reads the captured rows directly rather than joining, so a corrupted assignment
// is never hidden by a missing parent row.
func (v compatibilityView) assignmentDiagnostics() []CompatibilityDiagnostic {
	partIDs := make(map[int64]struct{}, len(v.raw.Parts))
	for _, part := range v.raw.Parts {
		partIDs[part.ID] = struct{}{}
	}
	binIDs := make(map[int64]struct{}, len(v.raw.Bins))
	for _, bin := range v.raw.Bins {
		binIDs[bin.ID] = struct{}{}
	}
	var diagnostics []CompatibilityDiagnostic
	for _, assignment := range v.raw.PartAssignments {
		base := CompatibilityDiagnostic{
			Severity:     CompatibilitySeverityWarning,
			AssignmentID: assignment.ID,
			PartID:       assignment.PartID,
		}
		if _, ok := partIDs[assignment.PartID]; !ok {
			missingPart := base
			missingPart.Code = CodeAssignmentMissingPart
			diagnostics = append(diagnostics, missingPart)
		}
		if assignment.BinID.Valid {
			if _, ok := binIDs[assignment.BinID.Int64]; !ok {
				missingBin := base
				missingBin.Code = CodeAssignmentMissingBin
				missingBin.BinID = assignment.BinID.Int64
				diagnostics = append(diagnostics, missingBin)
			}
		}
	}
	return diagnostics
}

// targetDiagnostics reports each bin's refusal and any unsafe span of a resolved
// target. The mapper bounds neither a bin's full width nor the controller
// capacity, so a span the mapper accepts is compared against the drawer
// allocation and the controller's LED count and flagged rather than replaced
// with a different target. A controller LED count of zero is unknown, not a
// capacity of zero, and is not reported.
func (v compatibilityView) targetDiagnostics(targets []CompatibilityBinTarget) []CompatibilityDiagnostic {
	var diagnostics []CompatibilityDiagnostic
	for _, target := range targets {
		finding := CompatibilityDiagnostic{
			BinID:        target.BinID,
			ContainerID:  target.ContainerID,
			ControllerID: target.ControllerID,
			Index:        target.RawLedIndex.Int64,
			Width:        target.EffectiveWidth,
		}
		if _, ok := v.containers[target.ContainerID]; !ok && target.Refusal != CodeContainerNotFound {
			missing := finding
			missing.Code = CodeContainerNotFound
			missing.Severity = CompatibilitySeverityWarning
			diagnostics = append(diagnostics, missing)
		}
		if target.Refusal != "" {
			finding.Code = target.Refusal
			finding.Severity = CompatibilitySeverityWarning
			if target.Refusal == CodeUnmapped {
				// An unmapped bin has no stored index; the raw NULL is preserved in
				// CompatibilityRaw.Bins.
				finding.Severity = CompatibilitySeverityInfo
				finding.Index = 0
			}
			diagnostics = append(diagnostics, finding)
			continue
		}
		if target.Start > math.MaxInt64-target.Count {
			overflow := finding
			overflow.Code = CodeRangeOverflow
			overflow.Severity = CompatibilitySeverityWarning
			overflow.Index = target.Start
			overflow.Width = target.Count
			diagnostics = append(diagnostics, overflow)
			continue
		}
		container := v.containers[target.ContainerID]
		if container.LedCount > 0 {
			// Reuses the containment rule applied when mappings and restore
			// manifests are validated, in the captured coordinate space.
			if err := ValidateBinMappingInSpace(v.space, target.RawLedIndex.Int64, target.Count, container.LedStart, container.LedCount); err != nil {
				outside := finding
				outside.Code = CodeRangeOutsideAllocation
				outside.Severity = CompatibilitySeverityWarning
				outside.Index = target.Start
				outside.Width = target.Count
				outside.Capacity = container.LedCount
				diagnostics = append(diagnostics, outside)
			}
		}
		if controller, ok := v.controllers[target.ControllerID]; ok && controller.LedCount > 0 && target.Start+target.Count > controller.LedCount {
			beyond := finding
			beyond.Code = CodeRangeBeyondCapacity
			beyond.Severity = CompatibilitySeverityWarning
			beyond.Index = target.Start
			beyond.Width = target.Count
			beyond.Capacity = controller.LedCount
			diagnostics = append(diagnostics, beyond)
		}
	}
	return diagnostics
}

// inventoryFingerprintPayload is the deterministic serialization of the
// inventory domain. Field order is fixed and no maps are used, so the same state
// always serializes to the same bytes.
type inventoryFingerprintPayload struct {
	Domain          string              `json:"domain"`
	Version         string              `json:"version"`
	Parts           []db.Part           `json:"parts"`
	PartAssignments []db.PartAssignment `json:"part_assignments"`
	BinIDs          []int64             `json:"bin_ids"`
}

// mappingFingerprintPayload is the deterministic serialization of the mapping
// domain, including the raw flags.
type mappingFingerprintPayload struct {
	Domain      string             `json:"domain"`
	Version     string             `json:"version"`
	Controllers []db.Controller    `json:"controllers"`
	Containers  []db.Container     `json:"containers"`
	Bins        []db.Bin           `json:"bins"`
	Flags       CompatibilityFlags `json:"flags"`
}

// targetsFingerprintPayload is the deterministic serialization of the resolved
// per-bin targets and their refusal codes.
type targetsFingerprintPayload struct {
	Domain  string                      `json:"domain"`
	Version string                      `json:"version"`
	Targets []compatibilityTargetIntent `json:"targets"`
}

// Only effective command intent belongs in the target digest. Raw widths,
// coordinate conventions and container relationships belong in the mapping
// digest; equivalent legacy representations must retain the same target hash.
type compatibilityTargetIntent struct {
	BinID        int64             `json:"bin_id"`
	Resolved     bool              `json:"resolved"`
	ControllerID int64             `json:"controller_id,omitempty"`
	Address      string            `json:"address,omitempty"`
	SegmentID    int64             `json:"segment_id,omitempty"`
	Start        int64             `json:"start,omitempty"`
	Count        int64             `json:"count,omitempty"`
	Refusal      CompatibilityCode `json:"refusal,omitempty"`
}

// FingerprintInventory returns the versioned SHA-256 fingerprint of the
// inventory domain: part identities, their bin assignments and quantities, and
// bin identities. It is independent of the order the rows are supplied in.
func FingerprintInventory(raw CompatibilityRaw) (CompatibilityFingerprint, error) {
	parts := cloneCompatibilityRows(raw.Parts)
	sort.Slice(parts, func(i, j int) bool { return parts[i].ID < parts[j].ID })
	assignments := cloneCompatibilityRows(raw.PartAssignments)
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].ID < assignments[j].ID })
	binIDs := make([]int64, 0, len(raw.Bins))
	for _, bin := range raw.Bins {
		binIDs = append(binIDs, bin.ID)
	}
	sort.Slice(binIDs, func(i, j int) bool { return binIDs[i] < binIDs[j] })

	payload, err := marshalCompatibilityPayload(inventoryFingerprintPayload{
		Domain:          "inventory",
		Version:         CompatibilityFingerprintVersion,
		Parts:           parts,
		PartAssignments: assignments,
		BinIDs:          binIDs,
	})
	if err != nil {
		return CompatibilityFingerprint{}, fmt.Errorf("fingerprint inventory: %w", err)
	}
	return compatibilityDigest(payload), nil
}

// FingerprintMapping returns the versioned SHA-256 fingerprint of the mapping
// domain: controllers, containers, bins and the raw coordinate-space flags,
// including the SQL NULL validity of each nullable column. It is independent of
// the order the rows are supplied in.
func FingerprintMapping(raw CompatibilityRaw, flags CompatibilityFlags) (CompatibilityFingerprint, error) {
	controllers := cloneCompatibilityRows(raw.Controllers)
	sort.Slice(controllers, func(i, j int) bool { return controllers[i].ID < controllers[j].ID })
	containers := cloneCompatibilityRows(raw.Containers)
	sort.Slice(containers, func(i, j int) bool { return containers[i].ID < containers[j].ID })
	bins := cloneCompatibilityRows(raw.Bins)
	sort.Slice(bins, func(i, j int) bool { return bins[i].ID < bins[j].ID })

	payload, err := marshalCompatibilityPayload(mappingFingerprintPayload{
		Domain:      "mapping",
		Version:     CompatibilityFingerprintVersion,
		Controllers: controllers,
		Containers:  containers,
		Bins:        bins,
		Flags:       flags,
	})
	if err != nil {
		return CompatibilityFingerprint{}, fmt.Errorf("fingerprint mapping: %w", err)
	}
	return compatibilityDigest(payload), nil
}

// FingerprintTargets returns the versioned SHA-256 fingerprint of the LED
// targets domain: every bin's resolved target or refusal code. It is independent
// of the order the targets are supplied in.
func FingerprintTargets(targets []CompatibilityBinTarget) (CompatibilityFingerprint, error) {
	canonical := make([]compatibilityTargetIntent, 0, len(targets))
	for _, target := range targets {
		intent := compatibilityTargetIntent{
			BinID:    target.BinID,
			Resolved: target.Resolved,
			Refusal:  target.Refusal,
		}
		if target.Resolved {
			intent.ControllerID = target.ControllerID
			intent.Address = target.Address
			intent.SegmentID = target.SegmentID
			intent.Start = target.Start
			intent.Count = target.Count
		}
		canonical = append(canonical, intent)
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].BinID < canonical[j].BinID })

	payload, err := marshalCompatibilityPayload(targetsFingerprintPayload{
		Domain:  "targets",
		Version: CompatibilityFingerprintVersion,
		Targets: canonical,
	})
	if err != nil {
		return CompatibilityFingerprint{}, fmt.Errorf("fingerprint targets: %w", err)
	}
	return compatibilityDigest(payload), nil
}

// JSON replaces invalid UTF-8 with replacement characters. Reject those values
// rather than certify a digest that has silently normalized stored text.
func marshalCompatibilityPayload(payload any) ([]byte, error) {
	var checkText func(reflect.Value) error
	checkText = func(value reflect.Value) error {
		switch value.Kind() {
		case reflect.String:
			if !utf8.ValidString(value.String()) {
				return errCompatibilityInvalidUTF8
			}
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				field := value.Type().Field(i)
				if field.PkgPath != "" {
					continue
				}
				if err := checkText(value.Field(i)); err != nil {
					return fmt.Errorf("%s.%s: %w", value.Type(), field.Name, err)
				}
			}
		case reflect.Slice:
			for i := 0; i < value.Len(); i++ {
				if err := checkText(value.Index(i)); err != nil {
					return fmt.Errorf("row %d: %w", i, err)
				}
			}
		}
		return nil
	}
	if err := checkText(reflect.ValueOf(payload)); err != nil {
		return nil, err
	}
	return json.Marshal(payload)
}

// compatibilityDigest binds the fingerprint version to the hashed bytes of a
// deterministic payload.
func compatibilityDigest(payload []byte) CompatibilityFingerprint {
	sum := sha256.Sum256(payload)
	return CompatibilityFingerprint{
		Version: CompatibilityFingerprintVersion,
		Digest:  hex.EncodeToString(sum[:]),
	}
}
