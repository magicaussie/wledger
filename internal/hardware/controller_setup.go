package hardware

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/internal/wiringdesigner"
)

// CreateControllerWithLayout commits the complete unsaved setup draft together.
// Negative output IDs are request-local references, never persisted identities.
func (s *service) CreateControllerWithLayout(ctx context.Context, params db.CreateControllerParams, settings controllerconfig.Settings, layout wiringdesigner.Layout) (int64, error) {
	if !params.Port.Valid {
		return 0, fmt.Errorf("port is required")
	}
	name, address, err := ValidateControllerSettings(params.Name, params.IpAddress, params.Port.Int64)
	if err != nil {
		return 0, err
	}
	if len(settings.Outputs) == 0 || len(settings.Outputs) > 64 || layout.Revision != 0 {
		return 0, fmt.Errorf("start a new draft with at least one output")
	}
	draftIDs := make([]int64, len(settings.Outputs))
	pixels := map[int64]int64{}
	settings.Outputs = append([]controllerconfig.Output(nil), settings.Outputs...)
	for i, output := range settings.Outputs {
		if output.ID >= 0 || output.DisplayOrder != int64(i) {
			return 0, fmt.Errorf("draft output identities/order are invalid")
		}
		if _, duplicate := pixels[output.ID]; duplicate {
			return 0, fmt.Errorf("duplicate draft output identity")
		}
		draftIDs[i] = output.ID
		pixels[output.ID] = output.PixelCount
		settings.Outputs[i].ID = 0
	}
	if _, err = settings.NormalizeAndValidate(); err != nil {
		return 0, fmt.Errorf("check output counts and advanced settings: %w", err)
	}
	if err = wiringdesigner.Validate(layout, pixels); err != nil {
		return 0, err
	}
	params.Name, params.IpAddress = name, address
	params.ControllerType, params.ConnectionTimeoutMs = settings.ControllerType, settings.ConnectionTimeoutMS
	layout.Outputs = append([]wiringdesigner.OutputLayout(nil), layout.Outputs...)
	var id int64
	err = db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		owned, e := db.HasPhysicalLocateOwnership(ctx, q)
		if e != nil {
			return e
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
		if e = ensureUniqueControllerEndpoint(ctx, q, 0, address, params.Port.Int64); e != nil {
			return e
		}
		controller, e := q.CreateController(ctx, params)
		if e != nil {
			return e
		}
		id = controller.ID
		if e = storage.SaveControllerOutputSettingsTx(ctx, q, id, 0, settings, false); e != nil {
			return e
		}
		outputs, e := q.GetControllerOutputs(ctx, id)
		if e != nil {
			return e
		}
		if len(outputs) != len(draftIDs) {
			return fmt.Errorf("output persistence mismatch")
		}
		remap := map[int64]int64{}
		savedPixels := map[int64]int64{}
		for i, output := range outputs {
			if output.DisplayOrder != int64(i) {
				return fmt.Errorf("output order mismatch")
			}
			remap[draftIDs[i]] = output.ID
			savedPixels[output.ID] = output.PixelCount
		}
		for i := range layout.Outputs {
			layout.Outputs[i].OutputID = remap[layout.Outputs[i].OutputID]
		}
		layout.Revision = 1
		layout.TopologySignature, e = topologySignature(ctx, q, id)
		if e != nil {
			return e
		}
		if e = wiringdesigner.Validate(layout, savedPixels); e != nil {
			return e
		}
		raw, e := json.Marshal(layout)
		if e != nil {
			return e
		}
		return q.SetFlag(ctx, db.SetFlagParams{Key: fmt.Sprintf("led_wiring_designer_v1:%d", id), Value: string(raw)})
	})
	if err != nil {
		return 0, err
	}
	audit.Log(ctx, s.store, "CREATE", "HARDWARE", id, "Saved guided controller and graphical wiring layout", nil, map[string]any{"name": name, "output_count": len(settings.Outputs)})
	return id, nil
}
