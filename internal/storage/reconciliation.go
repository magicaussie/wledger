// Package storage implements the owner-reviewed physical attachment
// reconciliation workflow for the M4 physical storage layout.
//
// The workflow binds an EXISTING logical bin (bins.id) to an EXISTING physical
// drawer (drawers.id) by creating a bin_drawer_attachments row. It never infers
// a layout: it does not treat the legacy 68 mapped bins as 69 drawers, never
// auto-creates bins or drawers, and never copies legacy electrical columns
// (bins.container_id / led_index / width / grid_x / grid_y, containers,
// controllers, part_assignments, system flags) into physical geometry. The
// physical geometry carried by an attachment is new, independent placement
// truth; the legacy columns keep their historical electrical/visual meaning.
//
// The workflow is a two-step, owner-in-the-loop protocol:
//
//  1. Preview evaluates a batch of AttachmentCandidate values read-only and
//     returns one ReconciliationEntry per candidate with a machine-readable
//     Status (pending / blocked / already_attached) and Reason. Nothing is
//     written.
//  2. ApplyReviewed revalidates the same candidates inside a single write
//     transaction and, only when no candidate is blocked, creates the new
//     physical attachments atomically. A blocked candidate blocks the ENTIRE
//     batch: nothing is written and a *BatchBlockedError is returned.
//
// It is explicit and review-driven: a bin that is already attached to a
// different drawer is never silently detached or reassigned. Such a request is
// reported as blocked so that moving or releasing occupied storage can be
// handled by a later, separately reviewed workflow.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/tuxedocurly/wledger/internal/db"
)

// ReconciliationStatus is the machine-readable classification of one candidate.
type ReconciliationStatus string

const (
	// StatusPending: the candidate is valid and unconfirmed; ApplyReviewed will
	// create a new physical attachment for it.
	StatusPending ReconciliationStatus = "pending"
	// StatusBlocked: the candidate is missing, ambiguous, invalid or stale, or it
	// targets storage that is already occupied. ApplyReviewed will not apply it.
	StatusBlocked ReconciliationStatus = "blocked"
	// StatusAlreadyAttached: the bin already carries exactly this reviewed
	// attachment. ApplyReviewed treats it as an idempotent no-op.
	StatusAlreadyAttached ReconciliationStatus = "already_attached"
)

// ReconciliationReason is a stable machine code explaining a candidate's status.
// It never carries prose; callers branch on the code, not on Error() text.
type ReconciliationReason string

const (
	// ReasonNone is the empty reason carried by a pending candidate.
	ReasonNone ReconciliationReason = ""
	// ReasonBinMissing: no bins row exists for the candidate BinID.
	ReasonBinMissing ReconciliationReason = "bin_missing"
	// ReasonDrawerMissing: no drawers row exists for the candidate DrawerID.
	ReasonDrawerMissing ReconciliationReason = "drawer_missing"
	// ReasonCabinetMissing: the drawer's cabinet does not exist.
	ReasonCabinetMissing ReconciliationReason = "cabinet_missing"
	// ReasonCabinetArchived: the drawer's cabinet is archived.
	ReasonCabinetArchived ReconciliationReason = "cabinet_archived"
	// ReasonDrawerArchived: the drawer itself is archived.
	ReasonDrawerArchived ReconciliationReason = "drawer_archived"
	// ReasonGeometryInvalid: the proposed physical geometry is not finite or
	// violates the non-negative origin / positive extent bounds.
	ReasonGeometryInvalid ReconciliationReason = "geometry_invalid"
	// ReasonOutOfBounds: the proposed attachment rectangle does not fit fully
	// inside the target drawer.
	ReasonOutOfBounds ReconciliationReason = "out_of_bounds"
	// ReasonDuplicateBin: the same BinID appears more than once in the batch, so
	// the bin's intended drawer is ambiguous.
	ReasonDuplicateBin ReconciliationReason = "duplicate_bin"
	// ReasonControllerMismatch: the drawer's configured cabinet owns a different
	// controller than the bin's legacy container.
	ReasonControllerMismatch ReconciliationReason = "controller_mismatch"
	// ReasonStaleReview: the bin's current attachment does not match the state
	// the owner reviewed (ExpectedCurrent), so the review is stale.
	ReasonStaleReview ReconciliationReason = "stale_review"
	// ReasonOccupied: the bin is already attached to a different drawer (or with
	// different geometry) than the reviewed candidate. Detaching or reassigning
	// occupied storage needs a later, safe workflow.
	ReasonOccupied ReconciliationReason = "occupied"
)

// ErrReconciliationBlocked is the sentinel wrapped by *BatchBlockedError so
// callers can match a blocked apply with errors.Is.
var ErrReconciliationBlocked = errors.New("physical attachment reconciliation blocked")

// BatchBlockedError reports that an apply was refused because at least one
// candidate was blocked. The whole batch is rolled back, so no attachment was
// created. It carries the blocked entries so the caller can inspect each
// machine Reason. It unwraps to ErrReconciliationBlocked.
type BatchBlockedError struct {
	// Entries holds only the blocked candidates, in the order they were
	// submitted; each carries its Status and machine Reason.
	Entries []ReconciliationEntry
}

func (e *BatchBlockedError) Error() string {
	return fmt.Sprintf("physical attachment reconciliation blocked for %d candidate(s)", len(e.Entries))
}

// Unwrap exposes ErrReconciliationBlocked for errors.Is.
func (e *BatchBlockedError) Unwrap() error { return ErrReconciliationBlocked }

// AttachmentCandidate is one owner-reviewed proposal to attach an existing bin
// to an existing drawer with new physical geometry. BinID and DrawerID are
// stable identities; no bin or drawer is created on their behalf.
type AttachmentCandidate struct {
	// BinID is the existing logical bin to attach.
	BinID int64
	// DrawerID is the existing physical drawer to attach it to.
	DrawerID int64
	// X and Y are the drawer-local, non-negative origin of the bin.
	X, Y float64
	// Width and Height are the strictly positive extents of the bin.
	Width, Height float64
	// DisplayOrder is the bin's ordering within the drawer.
	DisplayOrder int64
	// ExpectedCurrent is the attachment state the owner reviewed for this bin. A
	// zero value means the owner reviewed the bin as unattached. It is compared
	// against the freshly read current attachment so a review that a concurrent
	// writer invalidated is rejected as stale.
	ExpectedCurrent ExpectedAttachment
}

// ExpectedAttachment is the reviewed current attachment state of a bin.
type ExpectedAttachment struct {
	// Attached is whether the owner reviewed the bin as already attached.
	Attached bool
	// DrawerID is the reviewed drawer when Attached is true; ignored otherwise.
	DrawerID int64
}

// AttachmentSnapshot is the machine-readable current attachment of a bin. It is
// nil when the bin has no physical attachment.
type AttachmentSnapshot struct {
	BinID         int64
	DrawerID      int64
	X, Y          float64
	Width, Height float64
	DisplayOrder  int64
}

// ReconciliationEntry is one candidate's Preview or Apply result.
type ReconciliationEntry struct {
	// Candidate is the reviewed proposal this entry describes.
	Candidate AttachmentCandidate
	// BinID and DrawerID are the stable identities from the candidate.
	BinID    int64
	DrawerID int64
	// CabinetID is the owning cabinet of DrawerID, or 0 when it could not be
	// resolved.
	CabinetID int64
	// Existing is the bin's current attachment at evaluation time, or nil when
	// the bin carries none.
	Existing *AttachmentSnapshot
	// Status is the machine classification: pending, blocked or already_attached.
	Status ReconciliationStatus
	// Reason is a stable machine code; empty for a pending candidate.
	Reason ReconciliationReason
}

// ApplyResult is the outcome of an ApplyReviewed call.
type ApplyResult struct {
	// Entries holds one result per candidate, in submission order.
	Entries []ReconciliationEntry
	// Created is the number of new bin_drawer_attachments rows written.
	Created int
	// AlreadyAttached is the number of candidates that matched an existing
	// attachment exactly and were left untouched.
	AlreadyAttached int
}

// Service is the owner-reviewed physical attachment reconciliation service. It
// is stateless apart from the store and safe to reuse across calls.
type Service struct {
	store db.Store
}

// NewService creates a reconciliation service backed by store.
func NewService(store db.Store) *Service {
	return &Service{store: store}
}

// Preview evaluates every candidate read-only and returns one
// ReconciliationEntry per candidate, in submission order. It writes nothing: no
// attachment, cabinet, drawer, bin or legacy row is created or modified.
//
// A candidate is reported as:
//   - already_attached when the bin already carries exactly this attachment;
//   - pending when it is valid and unconfirmed;
//   - blocked, with a machine Reason, when it is missing, ambiguous, invalid,
//     stale, out of bounds for its drawer, or targets occupied storage.
func (s *Service) Preview(ctx context.Context, candidates []AttachmentCandidate) ([]ReconciliationEntry, error) {
	return s.evaluate(ctx, s.store, candidates)
}

// ApplyReviewed revalidates and applies a batch of reviewed candidates
// atomically. All candidates are re-evaluated inside a single write transaction
// (BEGIN IMMEDIATE) before any mutation, and each bin's expected previous
// attachment is checked before its row is written.
//
// If any candidate is blocked the ENTIRE batch is refused with a
// *BatchBlockedError (matching ErrReconciliationBlocked) and nothing is written.
// Otherwise each pending candidate creates exactly one new
// bin_drawer_attachments row and each affected cabinet's layout_revision is
// advanced by one, all in the same transaction. already_attached candidates are
// idempotent no-ops that leave existing ids, geometry and timestamps unchanged.
//
// On any error the returned ApplyResult is the zero value: no partial count is
// reported, because the whole transaction is rolled back. Blocked candidate
// detail lives in the returned *BatchBlockedError.
//
// No existing attachment is ever detached, reassigned or updated, and no other
// table is touched.
func (s *Service) ApplyReviewed(ctx context.Context, candidates []AttachmentCandidate) (ApplyResult, error) {
	var result ApplyResult
	err := s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		entries, err := s.evaluate(ctx, q, candidates)
		if err != nil {
			return err
		}
		result.Entries = entries

		var blocked []ReconciliationEntry
		for _, e := range entries {
			if e.Status == StatusBlocked {
				blocked = append(blocked, e)
			}
		}
		if len(blocked) > 0 {
			return &BatchBlockedError{Entries: blocked}
		}

		touched := map[int64]bool{}
		for _, e := range entries {
			switch e.Status {
			case StatusAlreadyAttached:
				result.AlreadyAttached++
				continue
			case StatusPending:
			default:
				return fmt.Errorf("unexpected reconciliation status %q", e.Status)
			}
			c := e.Candidate
			if err := q.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{
				BinID:        c.BinID,
				DrawerID:     c.DrawerID,
				X:            c.X,
				Y:            c.Y,
				Width:        c.Width,
				Height:       c.Height,
				DisplayOrder: c.DisplayOrder,
			}); err != nil {
				return fmt.Errorf("failed to create attachment for bin %d: %w", c.BinID, err)
			}
			result.Created++
			touched[e.CabinetID] = true
		}

		// Advance the layout revision of every affected cabinet once, so a
		// consumer of the physical layout can invalidate caches deterministically.
		ids := make([]int64, 0, len(touched))
		for id := range touched {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			cabinet, err := q.GetCabinet(ctx, id)
			if err != nil {
				return fmt.Errorf("failed to load cabinet %d for revision bump: %w", id, err)
			}
			if _, err := q.UpdateCabinetLayoutRevision(ctx, db.UpdateCabinetLayoutRevisionParams{
				LayoutRevision: cabinet.LayoutRevision + 1,
				ID:             id,
			}); err != nil {
				return fmt.Errorf("failed to advance layout revision for cabinet %d: %w", id, err)
			}
		}
		return nil
	})
	if err != nil {
		// Any failure — a blocked candidate, a failed insert or a failed revision
		// bump — rolls the whole transaction back, so the result is zero. Blocked
		// candidate detail is carried by *BatchBlockedError instead.
		return ApplyResult{}, err
	}
	return result, nil
}

// evaluate classifies every candidate against q without writing anything. It is
// shared by Preview and ApplyReviewed so the read-only preview and the
// transactional revalidation can never drift apart.
func (s *Service) evaluate(ctx context.Context, q db.Querier, candidates []AttachmentCandidate) ([]ReconciliationEntry, error) {
	counts := map[int64]int{}
	for _, c := range candidates {
		counts[c.BinID]++
	}

	entries := make([]ReconciliationEntry, 0, len(candidates))
	for _, c := range candidates {
		e := ReconciliationEntry{
			Candidate: c,
			BinID:     c.BinID,
			DrawerID:  c.DrawerID,
			Status:    StatusPending,
			Reason:    ReasonNone,
		}

		if counts[c.BinID] > 1 {
			e.blocked(ReasonDuplicateBin)
			entries = append(entries, e)
			continue
		}

		if !validGeometry(c.X, c.Y, c.Width, c.Height) {
			e.blocked(ReasonGeometryInvalid)
			entries = append(entries, e)
			continue
		}

		bin, err := q.GetBin(ctx, c.BinID)
		if errors.Is(err, sql.ErrNoRows) {
			e.blocked(ReasonBinMissing)
			entries = append(entries, e)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to load bin %d: %w", c.BinID, err)
		}

		drawer, err := q.GetDrawer(ctx, c.DrawerID)
		if errors.Is(err, sql.ErrNoRows) {
			e.blocked(ReasonDrawerMissing)
			entries = append(entries, e)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to load drawer %d: %w", c.DrawerID, err)
		}
		e.CabinetID = drawer.CabinetID

		cabinet, err := q.GetCabinet(ctx, drawer.CabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			e.blocked(ReasonCabinetMissing)
			entries = append(entries, e)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to load cabinet %d: %w", drawer.CabinetID, err)
		}
		if cabinet.Archived != 0 {
			e.blocked(ReasonCabinetArchived)
			entries = append(entries, e)
			continue
		}
		if drawer.Archived != 0 {
			e.blocked(ReasonDrawerArchived)
			entries = append(entries, e)
			continue
		}

		// The attachment rectangle must lie fully inside the target drawer. The
		// comparisons use subtraction rather than addition so a large extent can
		// never overflow into a false fit. Drawer extents are positive by schema.
		if c.Width > drawer.Width || c.Height > drawer.Height ||
			c.X > drawer.Width-c.Width || c.Y > drawer.Height-c.Height {
			e.blocked(ReasonOutOfBounds)
			entries = append(entries, e)
			continue
		}

		// A configured cabinet claims one electrical controller. The bin's
		// controller is its legacy container's controller. A draft cabinet with a
		// NULL controller attaches physical bins without claiming electrical
		// compatibility, so no controller assumption is made for it.
		if cabinet.ControllerID.Valid {
			container, err := q.GetContainer(ctx, bin.ContainerID)
			if err != nil {
				return nil, fmt.Errorf("failed to load container %d for bin %d: %w", bin.ContainerID, c.BinID, err)
			}
			if container.ControllerID != cabinet.ControllerID.Int64 {
				e.blocked(ReasonControllerMismatch)
				entries = append(entries, e)
				continue
			}
		}

		current, err := loadAttachment(ctx, q, c.BinID)
		if err != nil {
			return nil, err
		}
		e.Existing = current

		// An exact replay of an already-applied attachment is an idempotent no-op,
		// reported before the reviewed prior state is checked, so the very same
		// reviewed candidate can be retried without tripping the stale-state guard.
		if current != nil && attachmentMatches(current, c) {
			e.Status = StatusAlreadyAttached
			entries = append(entries, e)
			continue
		}

		if !expectedMatches(c.ExpectedCurrent, current) {
			e.blocked(ReasonStaleReview)
			entries = append(entries, e)
			continue
		}

		if current != nil {
			e.blocked(ReasonOccupied)
			entries = append(entries, e)
			continue
		}

		entries = append(entries, e)
	}
	return entries, nil
}

// blocked marks an entry as blocked with reason.
func (e *ReconciliationEntry) blocked(reason ReconciliationReason) {
	e.Status = StatusBlocked
	e.Reason = reason
}

// validGeometry reports whether the proposed physical geometry is finite and
// satisfies the schema's non-negative origin / strictly positive extent bounds.
func validGeometry(x, y, width, height float64) bool {
	if math.IsNaN(x) || math.IsInf(x, 0) ||
		math.IsNaN(y) || math.IsInf(y, 0) ||
		math.IsNaN(width) || math.IsInf(width, 0) ||
		math.IsNaN(height) || math.IsInf(height, 0) {
		return false
	}
	if x < 0 || y < 0 {
		return false
	}
	if width <= 0 || height <= 0 {
		return false
	}
	return true
}

// loadAttachment reads a bin's current attachment, returning nil when none
// exists.
func loadAttachment(ctx context.Context, q db.Querier, binID int64) (*AttachmentSnapshot, error) {
	row, err := q.GetBinDrawerAttachment(ctx, binID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load attachment for bin %d: %w", binID, err)
	}
	return &AttachmentSnapshot{
		BinID:        row.BinID,
		DrawerID:     row.DrawerID,
		X:            row.X,
		Y:            row.Y,
		Width:        row.Width,
		Height:       row.Height,
		DisplayOrder: row.DisplayOrder,
	}, nil
}

// expectedMatches reports whether the current attachment is the state the owner
// reviewed.
func expectedMatches(exp ExpectedAttachment, current *AttachmentSnapshot) bool {
	if !exp.Attached {
		return current == nil
	}
	return current != nil && current.DrawerID == exp.DrawerID
}

// attachmentMatches reports whether the current attachment is exactly the
// reviewed candidate (drawer and full physical geometry), making an apply an
// idempotent no-op.
func attachmentMatches(current *AttachmentSnapshot, c AttachmentCandidate) bool {
	return current.DrawerID == c.DrawerID &&
		current.X == c.X &&
		current.Y == c.Y &&
		current.Width == c.Width &&
		current.Height == c.Height &&
		current.DisplayOrder == c.DisplayOrder
}
