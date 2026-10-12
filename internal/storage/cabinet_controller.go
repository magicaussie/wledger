package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
)

var ErrCabinetControllerInvalid = errors.New("invalid cabinet controller assignment")

// SaveCabinetController is the backwards-compatible single-controller form of
// SaveCabinetControllers. A zero id clears every cabinet controller assignment.
func (s *LayoutService) SaveCabinetController(ctx context.Context, cabinetID, electricalRevision, controllerID int64) error {
	if controllerID < 0 {
		return ErrCabinetControllerInvalid
	}
	var ids []int64
	if controllerID > 0 {
		ids = []int64{controllerID}
	}
	return s.SaveCabinetControllers(ctx, cabinetID, electricalRevision, ids)
}

// SaveCabinetControllers replaces the cabinet's controller membership without
// changing any drawer, bin, inventory or legacy electrical-container data.
// Removed controllers must have their drawer ranges explicitly cleared first.
func (s *LayoutService) SaveCabinetControllers(ctx context.Context, cabinetID, electricalRevision int64, controllerIDs []int64) error {
	if cabinetID <= 0 || len(controllerIDs) > 5 {
		return ErrCabinetControllerInvalid
	}
	desired := make(map[int64]bool, len(controllerIDs))
	for _, id := range controllerIDs {
		if id <= 0 || desired[id] {
			return fmt.Errorf("%w: select between zero and five distinct controllers", ErrCabinetControllerInvalid)
		}
		desired[id] = true
	}

	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		cabinet, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cabinet.ElectricalRevision != electricalRevision {
			return ErrLEDAllocationStale
		}

		current, err := q.ListCabinetControllers(ctx, cabinetID)
		if err != nil {
			return err
		}
		currentByID := make(map[int64]db.CabinetController, len(current))
		for _, member := range current {
			currentByID[member.ControllerID] = member
		}
		if len(currentByID) == len(desired) {
			unchanged := true
			for id := range desired {
				if _, ok := currentByID[id]; !ok {
					unchanged = false
					break
				}
			}
			if unchanged {
				return nil
			}
		}

		for id := range desired {
			if _, exists := currentByID[id]; exists {
				continue
			}
			if _, err := q.GetController(ctx, id); errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: controller #%d does not exist", ErrCabinetControllerInvalid, id)
			} else if err != nil {
				return err
			}
			otherCabinet, err := q.GetCabinetByAssignedController(ctx, id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && otherCabinet.ID != cabinetID {
				return fmt.Errorf("%w: controller #%d is already assigned to cabinet #%d", ErrCabinetControllerInvalid, id, otherCabinet.ID)
			}
		}

		for id := range currentByID {
			if desired[id] {
				continue
			}
			allocations, err := q.ListDrawerLEDAllocationsByController(ctx, id)
			if err != nil {
				return err
			}
			if len(allocations) != 0 {
				return fmt.Errorf("%w: clear controller #%d's %d saved drawer LED range(s) before removing it from this cabinet", ErrUnsafeLayout, id, len(allocations))
			}
			if _, err := q.DeleteCabinetController(ctx, db.DeleteCabinetControllerParams{CabinetID: cabinetID, ControllerID: id}); err != nil {
				return err
			}
		}

		usedSlots := make(map[int64]bool, len(current))
		for _, member := range current {
			if desired[member.ControllerID] {
				usedSlots[member.Slot] = true
			}
		}
		for _, id := range controllerIDs {
			if _, exists := currentByID[id]; exists {
				continue
			}
			slot := int64(0)
			for slot < 5 && usedSlots[slot] {
				slot++
			}
			if slot == 5 {
				return ErrCabinetControllerInvalid
			}
			if err := q.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinetID, ControllerID: id, Slot: slot}); err != nil {
				return fmt.Errorf("assign controller #%d: %w", id, err)
			}
			usedSlots[slot] = true
		}

		// Preserve the legacy pointer whenever it remains assigned. It is NULL
		// when that controller is removed; secondary membership is not promoted
		// implicitly because old clients interpret this column as a single mapping.
		legacyPrimary := cabinet.ControllerID
		if legacyPrimary.Valid && !desired[legacyPrimary.Int64] {
			legacyPrimary = sql.NullInt64{}
		}
		if !legacyPrimary.Valid && len(controllerIDs) > 0 {
			// The first selected controller becomes the primary only when no
			// existing primary survives this edit.
			legacyPrimary = sql.NullInt64{Int64: controllerIDs[0], Valid: true}
		}
		if legacyPrimary != cabinet.ControllerID {
			if _, err := q.UpdateCabinetController(ctx, db.UpdateCabinetControllerParams{ControllerID: legacyPrimary, ID: cabinetID}); err != nil {
				return err
			}
		}

		rows, err := q.AdvanceCabinetElectricalRevision(ctx, db.AdvanceCabinetElectricalRevisionParams{ID: cabinetID, ElectricalRevision: electricalRevision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLEDAllocationStale
		}
		return nil
	})
}
