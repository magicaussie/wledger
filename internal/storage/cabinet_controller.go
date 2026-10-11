package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
)

var ErrCabinetControllerInvalid = errors.New("invalid cabinet controller assignment")

// SaveCabinetController changes only the electrical controller relationship; it
// never changes physical layout, legacy allocations or inventory. A cabinet may
// be assigned controllerID 0 (unassigned). Existing drawer mappings must be
// explicitly cleared before changing or removing their controller.
func (s *LayoutService) SaveCabinetController(ctx context.Context, cabinetID, electricalRevision, controllerID int64) error {
	if cabinetID <= 0 || controllerID < 0 {
		return ErrCabinetControllerInvalid
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
		if (!cabinet.ControllerID.Valid && controllerID == 0) || (cabinet.ControllerID.Valid && cabinet.ControllerID.Int64 == controllerID) {
			return nil
		}
		allocations, err := q.ListDrawerLEDAllocationsByCabinet(ctx, cabinetID)
		if err != nil {
			return err
		}
		if len(allocations) != 0 {
			return fmt.Errorf("%w: clear the cabinet's %d saved drawer LED range(s) before changing its controller", ErrUnsafeLayout, len(allocations))
		}
		var assignedController sql.NullInt64
		if controllerID > 0 {
			if _, err := q.GetController(ctx, controllerID); errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: controller #%d does not exist", ErrCabinetControllerInvalid, controllerID)
			} else if err != nil {
				return err
			}
			otherCabinet, err := q.GetCabinetByController(ctx, sql.NullInt64{Int64: controllerID, Valid: true})
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && otherCabinet.ID != cabinetID {
				return fmt.Errorf("%w: controller #%d is already assigned to cabinet #%d", ErrCabinetControllerInvalid, controllerID, otherCabinet.ID)
			}
			assignedController = sql.NullInt64{Int64: controllerID, Valid: true}
		}
		rows, err := q.UpdateCabinetController(ctx, db.UpdateCabinetControllerParams{ControllerID: assignedController, ID: cabinetID})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutNotFound
		}
		rows, err = q.AdvanceCabinetElectricalRevision(ctx, db.AdvanceCabinetElectricalRevisionParams{ID: cabinetID, ElectricalRevision: electricalRevision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLEDAllocationStale
		}
		return nil
	})
}
