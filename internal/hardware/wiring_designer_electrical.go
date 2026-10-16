package hardware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/internal/wiringdesigner"
)

var ErrElectricalDesignerDependencies = errors.New("electrical configuration cannot be changed while legacy or cabinet mappings exist")
var ErrElectricalDesignerStale = errors.New("controller topology changed; review again before applying")

type ElectricalReview struct {
	OldSettings       controllerconfig.Settings `json:"old_settings"`
	NewSettings       controllerconfig.Settings `json:"new_settings"`
	ExpectedSignature string                    `json:"expected_signature"`
	Changes           []string                  `json:"changes"`
}

func topologySignature(ctx context.Context, q db.Querier, id int64) (string, error) {
	outputs, err := q.GetControllerOutputs(ctx, id)
	if err != nil {
		return "", err
	}
	topology := make([]wiringdesigner.TopologyOutput, 0, len(outputs))
	for _, o := range outputs {
		topology = append(topology, wiringdesigner.TopologyOutput{ID: o.ID, DisplayOrder: o.DisplayOrder, PixelCount: o.PixelCount, Direction: o.Direction, Pattern: o.Pattern, Rows: o.Rows, Columns: o.Columns, CustomOrder: o.CustomOrderJson})
	}
	return wiringdesigner.TopologySignature(topology), nil
}

func (s *service) wiringReview(ctx context.Context, q db.Querier, id int64, proposed controllerconfig.Settings) (ElectricalReview, error) {
	controller, err := q.GetController(ctx, id)
	if err != nil {
		return ElectricalReview{}, err
	}
	owned, err := db.HasControllerPhysicalLocateOwnership(ctx, q, id)
	if err != nil {
		return ElectricalReview{}, err
	}
	if owned {
		return ElectricalReview{}, db.ErrPhysicalLocateActive
	}
	old, err := controllerSettingsFromQuerier(ctx, q, controller)
	if err != nil {
		return ElectricalReview{}, err
	}
	if _, err = proposed.NormalizeAndValidate(); err != nil {
		return ElectricalReview{}, fmt.Errorf("%w: %v", ErrInvalidControllerSettings, err)
	}
	knownIDs := make(map[int64]bool, len(old.Outputs))
	for _, output := range old.Outputs {
		knownIDs[output.ID] = true
	}
	seenIDs := make(map[int64]bool)
	for _, output := range proposed.Outputs {
		if output.ID > 0 {
			if !knownIDs[output.ID] || seenIDs[output.ID] {
				return ElectricalReview{}, fmt.Errorf("unknown or repeated output identity %d; reload the controller before editing", output.ID)
			}
			seenIDs[output.ID] = true
		}
	}
	containers, err := q.GetContainersByController(ctx, id)
	if err != nil {
		return ElectricalReview{}, err
	}
	var binsCount int
	for _, container := range containers {
		bins, e := q.GetBinsByContainer(ctx, sql.NullInt64{Int64: container.ID, Valid: true})
		if e != nil {
			return ElectricalReview{}, e
		}
		binsCount += len(bins)
	}
	_, err = q.GetCabinetByAssignedController(ctx, id)
	assigned := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ElectricalReview{}, err
	}
	_, legacyErr := q.GetCabinetByController(ctx, sql.NullInt64{Int64: id, Valid: true})
	if legacyErr != nil && !errors.Is(legacyErr, sql.ErrNoRows) {
		return ElectricalReview{}, legacyErr
	}
	if binsCount > 0 || assigned || legacyErr == nil {
		return ElectricalReview{}, ErrElectricalDesignerDependencies
	}
	sig, err := topologySignature(ctx, q, id)
	if err != nil {
		return ElectricalReview{}, err
	}
	changes := []string{}
	if old.ControllerType != proposed.ControllerType {
		changes = append(changes, "controller_type")
	}
	if old.ConnectionTimeoutMS != proposed.ConnectionTimeoutMS {
		changes = append(changes, "connection_timeout_ms")
	}
	if !reflect.DeepEqual(old.Outputs, proposed.Outputs) {
		changes = append(changes, "outputs")
	}
	return ElectricalReview{OldSettings: old, NewSettings: proposed, ExpectedSignature: sig, Changes: changes}, nil
}

func (s *service) ReviewWiringDesigner(ctx context.Context, id int64, settings controllerconfig.Settings) (ElectricalReview, error) {
	var result ElectricalReview
	err := db.WithOperationClaim(ctx, s.store, func(q db.Querier) error { var e error; result, e = s.wiringReview(ctx, q, id, settings); return e })
	return result, err
}

func (s *service) ApplyWiringDesigner(ctx context.Context, id int64, settings controllerconfig.Settings, expectedSignature string) error {
	if expectedSignature == "" {
		return ErrElectricalDesignerStale
	}
	return db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		review, err := s.wiringReview(ctx, q, id, settings)
		if err != nil {
			return err
		}
		if review.ExpectedSignature != expectedSignature {
			return ErrElectricalDesignerStale
		}
		settings = review.NewSettings
		controller, err := q.GetController(ctx, id)
		if err != nil {
			return err
		}
		updated, err := q.UpdateControllerHardwareSettings(ctx, db.UpdateControllerHardwareSettingsParams{ID: id, ControllerType: settings.ControllerType, ConnectionTimeoutMs: settings.ConnectionTimeoutMS})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf("controller %d was not updated", id)
		}
		return storage.SaveControllerOutputSettingsTx(ctx, q, id, controller.LedCount, settings, true)
	})
}
