package stock

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
)

// Sentinel errors returned by the stock service so callers can distinguish
// validation failures (missing assignment, ownership mismatch) from unexpected
// database errors.
var (
	// ErrAssignmentNotFound indicates the referenced stock assignment does not exist.
	ErrAssignmentNotFound = errors.New("stock assignment not found")
	// ErrAssignmentOwnership indicates the assignment belongs to a different part.
	ErrAssignmentOwnership = errors.New("stock assignment does not belong to part")
	// ErrInvalidStockRequest indicates an invalid identifier, quantity, or adjustment.
	ErrInvalidStockRequest = errors.New("invalid stock request")
)

// addQuantities returns a+b while rejecting signed overflow. Without this a
// positive adjustment applied to a near-maximum quantity wrapped to a large
// negative value that AdjustStock then clamped to zero, silently destroying the
// recorded quantity.
func addQuantities(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, fmt.Errorf("%w: quantity overflow", ErrInvalidStockRequest)
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, fmt.Errorf("%w: quantity overflow", ErrInvalidStockRequest)
	}
	return a + b, nil
}

type Service interface {
	AssignStock(ctx context.Context, req AssignStockRequest) error
	MoveStock(ctx context.Context, req MoveStockRequest) error
	RemoveStock(ctx context.Context, req RemoveStockRequest) error
	AdjustStock(ctx context.Context, req AdjustStockRequest) error
}

type AssignStockRequest struct {
	PartID   int64
	BinID    int64
	Quantity int
}

type MoveStockRequest struct {
	PartID       int64
	AssignmentID int64
	TargetBinID  int64
}

type RemoveStockRequest struct {
	PartID       int64
	AssignmentID int64
}

type AdjustStockRequest struct {
	PartID       int64
	AssignmentID int64
	Delta        int
}

type service struct {
	store  db.Store
	logger *slog.Logger
}

func NewService(store db.Store, logger *slog.Logger) Service {
	return &service{
		store:  store,
		logger: logger,
	}
}

func (s *service) AssignStock(ctx context.Context, req AssignStockRequest) error {
	if req.PartID <= 0 || req.BinID <= 0 || req.Quantity <= 0 {
		return fmt.Errorf("%w: part, bin, and quantity must be positive", ErrInvalidStockRequest)
	}
	s.logger.Debug("assigning stock", "part_id", req.PartID, "bin_id", req.BinID, "qty", req.Quantity)
	nullBinID := sql.NullInt64{Int64: int64(req.BinID), Valid: true}

	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		existingID, err := q.GetAssignmentID(ctx, db.GetAssignmentIDParams{
			PartID: req.PartID,
			BinID:  nullBinID,
		})

		if err == nil {
			// Assignment exists, fetch current quantity
			s.logger.Debug("updating existing assignment", "id", existingID)
			existing, err := q.GetAssignment(ctx, existingID)
			if err != nil {
				s.logger.Error("failed to fetch existing assignment for stock addition", "err", err, "part_id", req.PartID, "bin_id", req.BinID)
				return fmt.Errorf("failed to fetch existing assignment: %w", err)
			}

			newQty, err := addQuantities(existing.Quantity, int64(req.Quantity))
			if err != nil {
				return err
			}
			err = q.UpdatePartAssignmentQuantity(ctx, db.UpdatePartAssignmentQuantityParams{
				Quantity: newQty,
				PartID:   req.PartID,
				BinID:    nullBinID,
			})
			if err != nil {
				return err
			}

			audit.Log(ctx, q, "STOCK_ADD", "PART", req.PartID, "Added stock to existing bin",
				map[string]any{"quantity": existing.Quantity},
				map[string]any{"quantity": newQty})

		} else if errors.Is(err, sql.ErrNoRows) {
			// New assignment
			s.logger.Debug("creating new assignment", "part_id", req.PartID, "bin_id", req.BinID)
			err = q.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
				PartID:   req.PartID,
				BinID:    nullBinID,
				Quantity: int64(req.Quantity),
			})
			if err != nil {
				return err
			}

			audit.Log(ctx, q, "STOCK_ADD", "PART", req.PartID, "Added stock to new bin", nil,
				map[string]any{"bin_id": req.BinID, "quantity": req.Quantity})
		} else {
			return fmt.Errorf("failed to look up stock assignment: %w", err)
		}
		return nil
	})
}

func (s *service) AdjustStock(ctx context.Context, req AdjustStockRequest) error {
	if req.PartID <= 0 || req.AssignmentID <= 0 || req.Delta == 0 {
		return fmt.Errorf("%w: part, assignment, and non-zero delta are required", ErrInvalidStockRequest)
	}
	s.logger.Debug("adjusting stock", "id", req.AssignmentID, "delta", req.Delta)

	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		assignment, err := q.GetAssignment(ctx, req.AssignmentID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: id %d", ErrAssignmentNotFound, req.AssignmentID)
		}
		if err != nil {
			return fmt.Errorf("failed to fetch assignment for adjustment: %w", err)
		}
		if assignment.PartID != req.PartID {
			return fmt.Errorf("%w: assignment %d belongs to part %d, not part %d",
				ErrAssignmentOwnership, req.AssignmentID, assignment.PartID, req.PartID)
		}

		newQty, err := addQuantities(assignment.Quantity, int64(req.Delta))
		if err != nil {
			return err
		}
		if newQty < 0 {
			newQty = 0
		}

		// Update by primary key: the part/bin keyed variant cannot match an
		// orphaned assignment (bin_id IS NULL), which would silently update
		// zero rows while still writing an audit entry.
		err = q.UpdatePartAssignmentQuantityByID(ctx, db.UpdatePartAssignmentQuantityByIDParams{
			Quantity: newQty,
			ID:       req.AssignmentID,
		})

		if err != nil {
			return err
		}

		action := "STOCK_INC"
		if req.Delta < 0 {
			action = "STOCK_DEC"
		}
		audit.Log(ctx, q, action, "PART", assignment.PartID, fmt.Sprintf("Adjusted stock by %d", req.Delta),
			map[string]any{"quantity": assignment.Quantity},
			map[string]any{"quantity": newQty})

		return nil
	})
}

func (s *service) MoveStock(ctx context.Context, req MoveStockRequest) error {
	if req.PartID <= 0 || req.AssignmentID <= 0 || req.TargetBinID <= 0 {
		return fmt.Errorf("%w: part, assignment, and target bin must be positive", ErrInvalidStockRequest)
	}
	s.logger.Debug("moving stock", "part_id", req.PartID, "assignment_id", req.AssignmentID, "target_bin_id", req.TargetBinID)

	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		source, err := q.GetAssignment(ctx, req.AssignmentID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: id %d", ErrAssignmentNotFound, req.AssignmentID)
		}
		if err != nil {
			return fmt.Errorf("failed to fetch source assignment: %w", err)
		}

		// The assignment must belong to the part named in the request. The part
		// id and assignment id arrive as independent URL segments, so without
		// this check a mismatched request would merge the source quantity into a
		// different part's target assignment and delete the source row.
		if source.PartID != req.PartID {
			s.logger.Warn("stock move rejected: assignment does not belong to part",
				"assignment_id", req.AssignmentID, "assignment_part_id", source.PartID, "requested_part_id", req.PartID)
			return fmt.Errorf("%w: assignment %d belongs to part %d, not part %d",
				ErrAssignmentOwnership, req.AssignmentID, source.PartID, req.PartID)
		}

		// Check for same bin move (No-op)
		if source.BinID.Valid && source.BinID.Int64 == req.TargetBinID {
			s.logger.Debug("source and target bins are identical, ignoring move request", "bin_id", req.TargetBinID)
			return nil
		}

		s.logger.Debug("checking if target bin already has an assignment for this part", "part_id", req.PartID, "bin_id", req.TargetBinID)
		targetID, err := q.GetAssignmentID(ctx, db.GetAssignmentIDParams{
			PartID: req.PartID,
			BinID:  sql.NullInt64{Int64: req.TargetBinID, Valid: true},
		})

		if err == nil {
			s.logger.Debug("merging stock into existing target assignment", "target_id", targetID)
			target, err := q.GetAssignment(ctx, targetID)
			if err != nil {
				return fmt.Errorf("failed to fetch target for merge: %w", err)
			}

			newQty, err := addQuantities(target.Quantity, source.Quantity)
			if err != nil {
				return fmt.Errorf("failed to merge target stock: %w", err)
			}

			err = q.UpdatePartAssignmentQuantityByID(ctx, db.UpdatePartAssignmentQuantityByIDParams{
				Quantity: newQty,
				ID:       targetID,
			})
			if err != nil {
				return fmt.Errorf("failed to update target stock: %w", err)
			}

			err = q.DeleteAssignment(ctx, req.AssignmentID)
			if err != nil {
				return fmt.Errorf("failed to delete source stock after merge: %w", err)
			}

			audit.Log(ctx, q, "STOCK_MERGE", "PART", req.PartID, fmt.Sprintf("Merged stock into bin %d", req.TargetBinID),
				map[string]any{"bin_id": source.BinID.Int64, "quantity": source.Quantity},
				map[string]any{"bin_id": req.TargetBinID, "quantity": newQty}) // newQty is total in target
		} else if errors.Is(err, sql.ErrNoRows) {
			s.logger.Debug("reassigning assignment to new bin")
			err = q.ReassignPartAssignment(ctx, db.ReassignPartAssignmentParams{
				BinID: sql.NullInt64{Int64: req.TargetBinID, Valid: true},
				ID:    req.AssignmentID,
			})
			if err != nil {
				return fmt.Errorf("failed to move stock: %w", err)
			}

			audit.Log(ctx, q, "STOCK_MOVE", "PART", req.PartID, fmt.Sprintf("Moved stock to bin %d", req.TargetBinID),
				map[string]any{"bin_id": source.BinID.Int64},
				map[string]any{"bin_id": req.TargetBinID})
		} else {
			return fmt.Errorf("failed to look up target stock assignment: %w", err)
		}
		return nil
	})
}

func (s *service) RemoveStock(ctx context.Context, req RemoveStockRequest) error {
	if req.PartID <= 0 || req.AssignmentID <= 0 {
		return fmt.Errorf("%w: part and assignment must be positive", ErrInvalidStockRequest)
	}
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		assignment, err := q.GetAssignment(ctx, req.AssignmentID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: id %d", ErrAssignmentNotFound, req.AssignmentID)
		}
		if err != nil {
			return fmt.Errorf("failed to fetch assignment for removal: %w", err)
		}

		// The assignment must belong to the part named in the request; otherwise
		// a mismatched request could delete another part's stock and record the
		// removal against the wrong part.
		if assignment.PartID != req.PartID {
			s.logger.Warn("stock removal rejected: assignment does not belong to part",
				"assignment_id", req.AssignmentID, "assignment_part_id", assignment.PartID, "requested_part_id", req.PartID)
			return fmt.Errorf("%w: assignment %d belongs to part %d, not part %d",
				ErrAssignmentOwnership, req.AssignmentID, assignment.PartID, req.PartID)
		}

		if err := q.DeleteAssignment(ctx, req.AssignmentID); err != nil {
			return err
		}

		// Audit only after the deletion succeeds; it runs inside the same
		// transaction, so a later failure rolls the entry back too.
		audit.Log(ctx, q, "STOCK_REMOVE", "PART", assignment.PartID, "Removed stock",
			map[string]any{
				"bin_id":   assignment.BinID.Int64,
				"quantity": assignment.Quantity,
			}, nil)

		return nil
	})
}
