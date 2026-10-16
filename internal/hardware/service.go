package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/internal/wled"
)

type Service interface {
	ListControllers(ctx context.Context) ([]db.Controller, error)
	GetController(ctx context.Context, id int64) (db.Controller, error)
	GetControllerSettings(ctx context.Context, id int64) (controllerconfig.Settings, error)
	CreateController(ctx context.Context, params db.CreateControllerParams) (db.CreateControllerRow, error)
	CreateControllerConfigured(ctx context.Context, params db.CreateControllerParams, settings controllerconfig.Settings) (db.CreateControllerRow, error)
	UpdateController(ctx context.Context, id int64, params db.CreateControllerParams) error
	UpdateControllerConfigured(ctx context.Context, id int64, params db.CreateControllerParams, settings controllerconfig.Settings, confirmMappingImpact bool) error
	ReviewWiringDesigner(ctx context.Context, id int64, settings controllerconfig.Settings) (ElectricalReview, error)
	ApplyWiringDesigner(ctx context.Context, id int64, settings controllerconfig.Settings, expectedSignature string) error
	GetControllerOutputs(ctx context.Context, id int64) ([]db.ControllerOutput, error)
	DeleteController(ctx context.Context, id int64) error
	UpdateStatus(ctx context.Context, id int64) (bool, error)
	GetBinsByController(ctx context.Context, id int64) ([]db.Bin, error)
	GetContainers(ctx context.Context, controllerID int64) ([]db.Container, error)
	SaveGrid(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string) (int64, error)
	// SaveGridInSpace is SaveGrid with a coordinate-space guard: expectedSpace is
	// the coordinate space the submitted payload was authored in. If it does not
	// match the active space at write time (for example the grid page was loaded
	// before an administrator converted the database), the save is refused rather
	// than interpreting the indices in the wrong space.
	SaveGridInSpace(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string, expectedSpace string) (int64, error)
	ExportConfig(ctx context.Context, controllerID int64) ([]byte, error)
	ImportConfig(ctx context.Context, name, ip string, port int64, data []byte) (int64, error)
}

type service struct {
	store  db.Store
	wled   *wled.Client
	logger *slog.Logger
}

func NewService(store db.Store, wledClient *wled.Client, logger *slog.Logger) Service {
	wledClient.SetOperationStore(store)
	return &service{
		store:  store,
		wled:   wledClient,
		logger: logger,
	}
}

func (s *service) GetContainers(ctx context.Context, controllerID int64) ([]db.Container, error) {
	containers, err := s.store.GetContainersByController(ctx, controllerID)
	if err != nil {
		return nil, err
	}

	// Validate config JSON for each container and log if corrupted
	for _, c := range containers {
		if c.ConfigJson.Valid && c.ConfigJson.String != "" {
			var js json.RawMessage
			if err := json.Unmarshal([]byte(c.ConfigJson.String), &js); err != nil {
				s.logger.Error("corrupted container configuration detected",
					"container_id", c.ID,
					"controller_id", controllerID,
					"err", err,
					"json", c.ConfigJson.String,
				)
			}
		}
	}

	return containers, nil
}

func (s *service) ListControllers(ctx context.Context) ([]db.Controller, error) {
	return s.store.GetControllers(ctx)
}

func (s *service) GetController(ctx context.Context, id int64) (db.Controller, error) {
	return s.store.GetController(ctx, id)
}

func (s *service) CreateController(ctx context.Context, params db.CreateControllerParams) (db.CreateControllerRow, error) {
	if err := normalizeControllerHardwareParams(&params, nil); err != nil {
		return db.CreateControllerRow{}, err
	}
	settings := controllerconfig.Settings{ControllerType: params.ControllerType.(string), ConnectionTimeoutMS: params.ConnectionTimeoutMs.(int64)}
	return s.CreateControllerConfigured(ctx, params, settings)
}

func (s *service) CreateControllerConfigured(ctx context.Context, params db.CreateControllerParams, settings controllerconfig.Settings) (db.CreateControllerRow, error) {
	if !params.Port.Valid {
		return db.CreateControllerRow{}, fmt.Errorf("%w: port is required", ErrInvalidControllerSettings)
	}
	name, address, err := ValidateControllerSettings(params.Name, params.IpAddress, params.Port.Int64)
	if err != nil {
		return db.CreateControllerRow{}, err
	}
	if settings.ControllerType == "" {
		settings.ControllerType = "wled"
	}
	if settings.ConnectionTimeoutMS == 0 {
		settings.ConnectionTimeoutMS = controllerconfig.DefaultConnectionTimeoutMS
	}
	_, err = settings.NormalizeAndValidate()
	if err != nil {
		return db.CreateControllerRow{}, fmt.Errorf("%w: %v", ErrInvalidControllerSettings, err)
	}
	params.Name, params.IpAddress = name, address
	params.ControllerType, params.ConnectionTimeoutMs = settings.ControllerType, settings.ConnectionTimeoutMS

	var row db.CreateControllerRow
	var containerErr error
	err = db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return err
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
		if err := ensureUniqueControllerEndpoint(ctx, q, 0, params.IpAddress, params.Port.Int64); err != nil {
			return err
		}
		row, err = q.CreateController(ctx, params)
		if err != nil {
			return err
		}
		if err := storage.SaveControllerOutputSettingsTx(ctx, q, row.ID, 0, settings, false); err != nil {
			return err
		}

		_, containerErr = q.CreateContainer(ctx, db.CreateContainerParams{
			Name:          row.Name + " (Main)",
			ControllerID:  row.ID,
			SegmentID:     0,
			PositionIndex: 0,
			LedStart:      0,
			LedCount:      64,
			ConfigJson:    sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true},
		})
		return nil
	})
	if err != nil {
		return row, err
	}
	if containerErr != nil {
		s.logger.Error("failed to create default container for new controller", "err", containerErr)
	}
	summary := map[string]any{
		"id": row.ID, "name": row.Name, "ip_address": row.IpAddress,
		"controller_type": settings.ControllerType, "output_count": len(settings.Outputs),
	}
	audit.Log(ctx, s.store, "CREATE", "HARDWARE", row.ID, "Added controller "+row.Name, nil, summary)
	return row, nil
}

func (s *service) GetControllerSettings(ctx context.Context, id int64) (controllerconfig.Settings, error) {
	controller, err := s.store.GetController(ctx, id)
	if err != nil {
		return controllerconfig.Settings{}, err
	}
	return controllerSettingsFromQuerier(ctx, s.store, controller)
}

func (s *service) GetControllerOutputs(ctx context.Context, id int64) ([]db.ControllerOutput, error) {
	if id <= 0 {
		return nil, fmt.Errorf("%w: controller ID must be positive", ErrInvalidControllerSettings)
	}
	return s.store.GetControllerOutputs(ctx, id)
}

func controllerSettingsFromQuerier(ctx context.Context, q db.Querier, controller db.Controller) (controllerconfig.Settings, error) {
	outputs, err := q.GetControllerOutputs(ctx, controller.ID)
	if err != nil {
		return controllerconfig.Settings{}, err
	}
	settings := controllerconfig.Settings{ControllerType: controller.ControllerType, ConnectionTimeoutMS: controller.ConnectionTimeoutMs}
	for _, output := range outputs {
		var customOrder []int64
		if err := json.Unmarshal([]byte(output.CustomOrderJson), &customOrder); err != nil {
			return controllerconfig.Settings{}, fmt.Errorf("controller #%d output #%d custom pattern: %w", controller.ID, output.ID, err)
		}
		settings.Outputs = append(settings.Outputs, controllerconfig.Output{
			ID: output.ID, DisplayOrder: output.DisplayOrder, Name: output.Name, PixelCount: output.PixelCount,
			Direction: output.Direction, Pattern: output.Pattern, Rows: output.Rows, Columns: output.Columns,
			CustomOrder: customOrder,
		})
	}
	if _, err := settings.NormalizeAndValidate(); err != nil {
		return controllerconfig.Settings{}, fmt.Errorf("controller #%d stored hardware configuration is invalid: %w", controller.ID, err)
	}
	return settings, nil
}

func (s *service) UpdateController(ctx context.Context, id int64, params db.CreateControllerParams) error {
	previous, err := s.GetController(ctx, id)
	if err != nil {
		return err
	}
	settings, err := controllerSettingsFromQuerier(ctx, s.store, previous)
	if err != nil {
		return err
	}
	params.ControllerType = settings.ControllerType
	params.ConnectionTimeoutMs = settings.ConnectionTimeoutMS
	return s.UpdateControllerConfigured(ctx, id, params, settings, false)
}

func (s *service) UpdateControllerConfigured(ctx context.Context, id int64, params db.CreateControllerParams, settings controllerconfig.Settings, confirmMappingImpact bool) error {
	if id <= 0 {
		return fmt.Errorf("%w: controller ID must be positive", ErrInvalidControllerSettings)
	}
	if !params.Port.Valid {
		return fmt.Errorf("%w: port is required", ErrInvalidControllerSettings)
	}
	name, address, err := ValidateControllerSettings(params.Name, params.IpAddress, params.Port.Int64)
	if err != nil {
		return err
	}
	params.Name, params.IpAddress = name, address
	if settings.ControllerType == "" {
		if err := normalizeControllerHardwareParams(&params, nil); err != nil {
			return err
		}
		settings.ControllerType = params.ControllerType.(string)
	}
	if settings.ConnectionTimeoutMS == 0 {
		if timeout, ok := params.ConnectionTimeoutMs.(int64); ok {
			settings.ConnectionTimeoutMS = timeout
		}
	}
	if _, err := settings.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidControllerSettings, err)
	}
	params.ControllerType, params.ConnectionTimeoutMs = settings.ControllerType, settings.ConnectionTimeoutMS

	var previous db.Controller
	var previousSettings controllerconfig.Settings
	err = db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		var err error
		previous, err = q.GetController(ctx, id)
		if err != nil {
			return err
		}
		oldPort := previous.Port.Int64
		if !previous.Port.Valid || oldPort < 1 {
			oldPort = defaultControllerPort
		}
		oldSettings, err := controllerSettingsFromQuerier(ctx, q, previous)
		if err != nil {
			return err
		}
		previousSettings = oldSettings
		outputsChanged := !reflect.DeepEqual(oldSettings.Outputs, settings.Outputs)
		configChanged := oldSettings.ControllerType != settings.ControllerType || oldSettings.ConnectionTimeoutMS != settings.ConnectionTimeoutMS || outputsChanged
		endpointChanged := previous.IpAddress != params.IpAddress || controllerEndpointKey(previous.IpAddress, oldPort) != controllerEndpointKey(params.IpAddress, params.Port.Int64)
		if endpointChanged {
			owned, err := db.HasControllerPhysicalLocateOwnership(ctx, q, id)
			if err != nil {
				return fmt.Errorf("check controller Locate ownership: %w", err)
			}
			if owned {
				return db.ErrPhysicalLocateActive
			}
		}
		if configChanged {
			owned, err := db.HasControllerPhysicalLocateOwnership(ctx, q, id)
			if err != nil {
				return fmt.Errorf("check controller output Locate ownership: %w", err)
			}
			if owned {
				return db.ErrPhysicalLocateActive
			}
		}
		if err := ensureUniqueControllerEndpoint(ctx, q, id, params.IpAddress, params.Port.Int64); err != nil {
			return err
		}
		rows, err := q.UpdateControllerSettings(ctx, db.UpdateControllerSettingsParams{
			ID: id, Name: params.Name, IpAddress: params.IpAddress, Port: params.Port,
			ControllerType: settings.ControllerType, ConnectionTimeoutMs: settings.ConnectionTimeoutMS,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return fmt.Errorf("controller %d was not updated", id)
		}
		if outputsChanged {
			return storage.SaveControllerOutputSettingsTx(ctx, q, id, previous.LedCount, settings, confirmMappingImpact)
		}
		return nil
	})
	if err != nil {
		return err
	}

	oldValue := map[string]any{"name": previous.Name, "ip_address": previous.IpAddress, "port": previous.Port.Int64, "controller_type": previousSettings.ControllerType, "connection_timeout_ms": previousSettings.ConnectionTimeoutMS}
	newValue := map[string]any{"name": params.Name, "ip_address": params.IpAddress, "port": params.Port.Int64, "controller_type": settings.ControllerType, "connection_timeout_ms": settings.ConnectionTimeoutMS, "output_count": len(settings.Outputs)}
	audit.Log(ctx, s.store, "UPDATE", "HARDWARE", id, "Updated controller configuration", oldValue, newValue)
	return nil
}

func normalizeControllerHardwareParams(params *db.CreateControllerParams, previous *db.Controller) error {
	controllerType := ""
	switch value := params.ControllerType.(type) {
	case string:
		controllerType = value
	case nil:
	default:
		return fmt.Errorf("%w: controller type must be a string", ErrInvalidControllerSettings)
	}
	if strings.TrimSpace(controllerType) == "" && previous != nil {
		controllerType = previous.ControllerType
	}
	if strings.TrimSpace(controllerType) == "" {
		controllerType = "wled"
	}

	timeout := int64(0)
	switch value := params.ConnectionTimeoutMs.(type) {
	case int64:
		timeout = value
	case int:
		timeout = int64(value)
	case nil:
	default:
		return fmt.Errorf("%w: connection timeout must be an integer", ErrInvalidControllerSettings)
	}
	if timeout == 0 && previous != nil {
		timeout = previous.ConnectionTimeoutMs
	}
	settings := controllerconfig.Settings{ControllerType: controllerType, ConnectionTimeoutMS: timeout}
	if _, err := settings.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidControllerSettings, err)
	}
	params.ControllerType = settings.ControllerType
	params.ConnectionTimeoutMs = settings.ConnectionTimeoutMS
	return nil
}

func ensureUniqueControllerEndpoint(ctx context.Context, q db.Querier, exceptID int64, address string, port int64) error {
	controllers, err := q.GetControllers(ctx)
	if err != nil {
		return fmt.Errorf("check controller endpoints: %w", err)
	}
	key := controllerEndpointKey(address, port)
	for _, controller := range controllers {
		if controller.ID == exceptID {
			continue
		}
		controllerPort := controller.Port.Int64
		if !controller.Port.Valid || controllerPort < 1 {
			controllerPort = defaultControllerPort
		}
		if controllerEndpointKey(controller.IpAddress, controllerPort) == key {
			return ErrDuplicateController
		}
	}
	return nil
}

func (s *service) DeleteController(ctx context.Context, id int64) error {
	var controller db.Controller
	err := db.WithOperationClaim(ctx, s.store, func(q db.Querier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return err
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
		controller, err = q.GetController(ctx, id)
		if err != nil {
			return err
		}
		return q.DeleteController(ctx, id)
	})
	if err != nil {
		return err
	}

	summary := map[string]any{
		"id":         controller.ID,
		"name":       controller.Name,
		"ip_address": controller.IpAddress,
	}
	audit.Log(ctx, s.store, "DELETE", "HARDWARE", id, "Deleted controller", summary, nil)
	return nil
}

func (s *service) UpdateStatus(ctx context.Context, id int64) (bool, error) {
	c, err := s.store.GetController(ctx, id)
	if err != nil {
		return false, err
	}

	online, _ := s.wled.PingController(ctx, c)

	if online != c.IsOnline.Bool {
		err := s.store.SetControllerOnlineStatus(ctx, db.SetControllerOnlineStatusParams{
			IsOnline: sql.NullBool{Bool: online, Valid: true},
			ID:       c.ID,
		})
		if err != nil {
			s.logger.Error("failed to update controller online status", "err", err, "id", c.ID, "online", online)
		}
	}

	return online, nil
}

func (s *service) GetBinsByController(ctx context.Context, id int64) ([]db.Bin, error) {
	containers, err := s.store.GetContainersByController(ctx, id)
	if err != nil {
		return nil, err
	}

	var allBins []db.Bin
	for _, c := range containers {
		bins, err := s.store.GetBinsByContainer(ctx, sql.NullInt64{Int64: c.ID, Valid: true})
		if err == nil {
			allBins = append(allBins, bins...)
		}
	}

	return allBins, nil
}

type containerInJSON struct {
	ID            *int64                 `json:"id"`
	Name          string                 `json:"name"`
	SegmentID     int64                  `json:"segment_id"`
	PositionIndex int64                  `json:"position_index"`
	LedStart      int64                  `json:"led_start"`
	LedCount      int64                  `json:"led_count"`
	Config        mapper.ContainerConfig `json:"config"`
}

type binInJSON struct {
	ContainerIndex int    `json:"container_index"`
	X              int    `json:"x"`
	Y              int    `json:"y"`
	LedIndex       *int   `json:"led_index"`
	Width          int    `json:"width"`
	Name           string `json:"name"`
}

// clampWidth ensures a bin's LED width is at least 1 (backward compatible
// with bins that historically had no width or width < 1).
func clampWidth(w int) int {
	if w < 1 {
		return 1
	}
	return w
}

// nullIntFromPtr converts an optional LED index to a nullable column value. A
// nil pointer represents an unmapped bin and is stored as SQL NULL, preserving
// the unmapped state without inventing a coordinate.
func nullIntFromPtr(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

// ErrReconciledLayout marks a refused legacy write because a controller's
// legacy bins carry reconciled physical attachments. The legacy grid writer and
// import match bins by grid coordinate, so allowing them to run would let that
// matching replace, delete or reassign a reconciled bin's stable identity and
// stock. It can be matched with errors.Is.
var ErrReconciledLayout = errors.New("reconciled layout protects legacy bin identities")

// guardReconciledController refuses a legacy mutation when any bin under the
// controller's existing containers carries a bin_drawer_attachments row. Those
// attachments bind stable reconciled bin IDs to physical drawers, so a legacy
// grid save or an import into this controller could otherwise disturb them.
//
// It reads through the caller's transaction so the refusal shares the write
// reservation and can never race a concurrent reconciliation. sql.ErrNoRows
// means the bin has no attachment (unbound) and is not a failure; any other
// read error is propagated so a partial check can never pass as safe.
func guardReconciledController(ctx context.Context, q db.Querier, controllerID int64) error {
	containers, err := q.GetContainersByController(ctx, controllerID)
	if err != nil {
		return fmt.Errorf("failed to read containers for controller %d reconciliation guard: %w", controllerID, err)
	}

	for _, c := range containers {
		bins, err := q.GetBinsByContainer(ctx, sql.NullInt64{Int64: c.ID, Valid: true})
		if err != nil {
			return fmt.Errorf("failed to read bins for container %d reconciliation guard: %w", c.ID, err)
		}
		for _, b := range bins {
			_, err := q.GetBinDrawerAttachment(ctx, b.ID)
			if err == nil {
				return fmt.Errorf("%w: bin %d under container %d is attached to a physical drawer", ErrReconciledLayout, b.ID, c.ID)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("failed to read attachment for bin %d reconciliation guard: %w", b.ID, err)
			}
		}
	}

	return nil
}

func (s *service) SaveGrid(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string) (int64, error) {
	return s.SaveGridInSpace(ctx, controllerID, gridDataJSON, configJSON, "")
}

func (s *service) SaveGridInSpace(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string, expectedSpace string) (int64, error) {
	var inputBins []binInJSON
	if err := json.Unmarshal([]byte(gridDataJSON), &inputBins); err != nil {
		return 0, fmt.Errorf("invalid grid json: %w", err)
	}

	var inputContainers []containerInJSON
	if err := json.Unmarshal([]byte(configJSON), &inputContainers); err != nil {
		return 0, fmt.Errorf("invalid config json: %w", err)
	}

	var totalLedCount int64 = 0

	// Resolve and validate drawer LED allocations before writing anything.
	resolved, err := resolveAllocations(inputContainers)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidAllocation, err)
	}

	// The submitted bin indices are interpreted in the active coordinate space,
	// which is read and validated inside the transaction below, so the write can
	// never mix coordinate spaces. In segment mode the indices are segment-
	// absolute; in drawer mode they are relative to the owning drawer's allocation
	// (led_start/led_count). Unresolved indices have no known coordinate system, so
	// editing is refused rather than written to with assumed coordinates.
	binRows := make([]binMappingRow, len(inputBins))
	for i := range inputBins {
		binRows[i] = binMappingRow{
			ContainerIndex: inputBins[i].ContainerIndex,
			Name:           inputBins[i].Name,
			LedIndex:       inputBins[i].LedIndex,
			Width:          inputBins[i].Width,
		}
	}

	err = s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		owned, err := db.HasPhysicalLocateOwnership(ctx, q)
		if err != nil {
			return fmt.Errorf("check durable physical Locate ownership before grid save: %w", err)
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
		// Read the active coordinate space from the same transaction that performs
		// the write. The write reservation is taken up front (BEGIN IMMEDIATE), so a
		// concurrent coordinate conversion cannot commit between this read and the
		// write. Segment-relative and drawer-relative databases are both editable,
		// each in its own coordinate space; an unresolved database is rejected
		// rather than written to with indices whose meaning is unknown.
		space, err := ledspace.Current(ctx, q)
		if err != nil {
			return fmt.Errorf("failed to read LED coordinate space: %w", err)
		}
		if space == ledspace.Unresolved {
			return fmt.Errorf("%w: grid editing is disabled while the LED coordinate space is unresolved", ErrInvalidAllocation)
		}

		// Refuse a save whose payload was authored in a different coordinate space
		// than the one in force now. This prevents a grid page loaded before an
		// administrator conversion from silently writing segment-relative indices
		// into a drawer-relative database (or vice versa).
		if expectedSpace != "" && expectedSpace != space {
			return fmt.Errorf("%w: the LED coordinate space changed from %q to %q; reload the grid before saving", ErrInvalidAllocation, expectedSpace, space)
		}

		// Validate every mapped bin against its drawer's allocation in the active
		// coordinate space before any persistent change. Unmapped bins (nil
		// led_index) are allowed. In drawer space this enforces the [0,count)
		// containment that keeps bins inside their owning drawer; because drawer
		// allocations are themselves validated non-overlapping, drawers sharing one
		// segment cannot hold overlapping LED ranges.
		if err := validateBinMappingsInSpace(space, binRows, resolved); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAllocation, err)
		}

		// Protect reconciled physical identities. Before any write, refuse the save
		// when a bin under this controller's legacy containers is attached to a
		// physical drawer: the legacy writer matches bins by grid coordinate, so
		// letting it run could replace, delete or reassign a reconciled identity.
		// The check shares this immediate transaction and its write reservation.
		if err := guardReconciledController(ctx, q, controllerID); err != nil {
			return err
		}

		// Sync Containers
		existingContainers, err := q.GetContainersByController(ctx, controllerID)
		if err != nil {
			return err
		}

		containerIDMap := make(map[int]int64) // Index in input -> DB ID
		updatedIDs := make(map[int64]bool)

		for i, c := range inputContainers {
			configBytes, _ := json.Marshal(c.Config)
			configStr := string(configBytes)

			if c.ID != nil {
				// Update existing
				err := q.UpdateContainerConfig(ctx, db.UpdateContainerConfigParams{
					ID:            *c.ID,
					Name:          c.Name,
					SegmentID:     c.SegmentID,
					PositionIndex: int64(i), // Use loop index as position
					LedStart:      c.LedStart,
					LedCount:      c.LedCount,
					ConfigJson:    sql.NullString{String: configStr, Valid: true},
				})
				if err != nil {
					return fmt.Errorf("failed to update container %d: %w", *c.ID, err)
				}
				containerIDMap[i] = *c.ID
				updatedIDs[*c.ID] = true
			} else {
				// Create new
				newID, err := q.CreateContainer(ctx, db.CreateContainerParams{
					Name:          c.Name,
					ControllerID:  controllerID,
					SegmentID:     c.SegmentID,
					PositionIndex: int64(i), // Use loop index as position
					LedStart:      c.LedStart,
					LedCount:      c.LedCount,
					ConfigJson:    sql.NullString{String: configStr, Valid: true},
				})
				if err != nil {
					return fmt.Errorf("failed to create container: %w", err)
				}
				containerIDMap[i] = newID
			}
		}

		// Delete containers that are no longer present
		for _, ec := range existingContainers {
			if !updatedIDs[ec.ID] {
				err := q.DeleteContainer(ctx, ec.ID)
				if err != nil {
					return fmt.Errorf("failed to delete container %d: %w", ec.ID, err)
				}
			}
		}

		// Sync Bins
		for i := range inputContainers {
			dbID := containerIDMap[i]

			// Get all input bins for THIS container
			var containerInputBins []binInJSON
			for _, b := range inputBins {
				if b.ContainerIndex == i {
					containerInputBins = append(containerInputBins, b)
				}
			}

			// Get existing bins from DB to preserve IDs (and part assignments)
			existingBins, err := q.GetBinsByContainer(ctx, sql.NullInt64{Int64: dbID, Valid: true})
			if err != nil {
				return err
			}

			// Clear all LED indices for this container to avoid unique constraint violations during swap/resize
			// SQLite UNIQUE(container_id, led_index) allows multiple NULLs
			err = q.ClearContainerBinLedIndices(ctx, sql.NullInt64{Int64: dbID, Valid: true})
			if err != nil {
				return fmt.Errorf("failed to clear bin indices: %w", err)
			}

			// Map existing bins by Position (X,Y)
			existingByPos := make(map[string]db.Bin)
			for _, bin := range existingBins {
				if bin.GridX.Valid && bin.GridY.Valid {
					key := fmt.Sprintf("%d,%d", bin.GridX.Int64, bin.GridY.Int64)
					existingByPos[key] = bin
				}
			}

			processedBinIDs := make(map[int64]bool)

			for _, b := range containerInputBins {
				key := fmt.Sprintf("%d,%d", b.X, b.Y)

				if existing, found := existingByPos[key]; found {
					// Update existing bin
					err := q.UpdateBin(ctx, db.UpdateBinParams{
						ID:       existing.ID,
						Name:     b.Name,
						LedIndex: nullIntFromPtr(b.LedIndex),
						Width:    sql.NullInt64{Int64: int64(clampWidth(b.Width)), Valid: true},
						GridX:    sql.NullInt64{Int64: int64(b.X), Valid: true},
						GridY:    sql.NullInt64{Int64: int64(b.Y), Valid: true},
					})
					if err != nil {
						return err
					}
					processedBinIDs[existing.ID] = true
				} else {
					// Create new bin
					_, err := q.CreateBin(ctx, db.CreateBinParams{
						Name:        b.Name,
						ContainerID: sql.NullInt64{Int64: dbID, Valid: true},
						LedIndex:    nullIntFromPtr(b.LedIndex),
						Width:       sql.NullInt64{Int64: int64(clampWidth(b.Width)), Valid: true},
						GridX:       sql.NullInt64{Int64: int64(b.X), Valid: true},
						GridY:       sql.NullInt64{Int64: int64(b.Y), Valid: true},
					})
					if err != nil {
						return err
					}
				}

				if b.LedIndex != nil {
					// Report the physical (segment-relative) LED end for the audit trail.
					// In drawer space the stored index is relative to the drawer, so the
					// drawer's allocation start is added back.
					base := int64(0)
					if space == ledspace.Drawer && b.ContainerIndex >= 0 && b.ContainerIndex < len(resolved) {
						base = resolved[b.ContainerIndex].LedStart
					}
					if end := base + int64(*b.LedIndex) + int64(clampWidth(b.Width)); end > totalLedCount {
						totalLedCount = end
					}
				}
			}

			// Delete orphans (bins that were in DB but not in input)
			for _, bin := range existingBins {
				if !processedBinIDs[bin.ID] {
					err := q.DeleteBin(ctx, bin.ID)
					if err != nil {
						return err
					}
				}
			}
		}

		// Audit Log
		audit.Log(ctx, q, "UPDATE", "HARDWARE", controllerID, "Updated LED Grid Layout",
			nil,
			map[string]any{"led_count": totalLedCount})

		return nil
	})

	return totalLedCount, err
}
