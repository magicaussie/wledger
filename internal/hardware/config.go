package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// hardware config export/import
const hwConfigVersion = "1.0"

type controllerConfig struct {
	Name      string `json:"name"`
	IpAddress string `json:"ip_address"`
	Port      int64  `json:"port"`
}

type containerConfig struct {
	Name          string                 `json:"name"`
	SegmentID     int64                  `json:"segment_id"`
	PositionIndex int64                  `json:"position_index"`
	LedStart      int64                  `json:"led_start"`
	LedCount      int64                  `json:"led_count"`
	Config        mapper.ContainerConfig `json:"config"`
}

type binConfig struct {
	ContainerIndex int    `json:"container_index"`
	X              int    `json:"x"`
	Y              int    `json:"y"`
	LedIndex       *int   `json:"led_index"`
	Width          int    `json:"width"`
	Name           string `json:"name"`
}

type hardwareConfig struct {
	Version       string            `json:"version"`
	ExportedAt    time.Time         `json:"exported_at"`
	BinIndexSpace string            `json:"bin_index_space,omitempty"`
	Controller    controllerConfig  `json:"controller"`
	Containers    []containerConfig `json:"containers"`
	Bins          []binConfig       `json:"bins"`
}

// ExportConfig serializes a controller and its full grid layout (containers +
// bins with their LED/width mappings) into JSON for later re-import.
func (s *service) ExportConfig(ctx context.Context, controllerID int64) ([]byte, error) {
	c, err := s.store.GetController(ctx, controllerID)
	if err != nil {
		return nil, err
	}

	containers, err := s.store.GetContainersByController(ctx, controllerID)
	if err != nil {
		return nil, err
	}

	// Export the actual coordinate space of the stored bin LED indices so a
	// re-import can interpret them without guessing.
	space, err := ledspace.Current(ctx, s.store)
	if err != nil {
		return nil, fmt.Errorf("failed to read LED coordinate space: %w", err)
	}

	cfg := hardwareConfig{
		Version:       hwConfigVersion,
		ExportedAt:    time.Now(),
		BinIndexSpace: space,
		Controller:    controllerConfig{Name: c.Name, IpAddress: c.IpAddress, Port: c.Port.Int64},
	}

	containerIDs := make([]int64, 0, len(containers))
	for _, ct := range containers {
		var cc mapper.ContainerConfig
		if ct.ConfigJson.Valid && ct.ConfigJson.String != "" {
			_ = json.Unmarshal([]byte(ct.ConfigJson.String), &cc)
		}
		cfg.Containers = append(cfg.Containers, containerConfig{
			Name:          ct.Name,
			SegmentID:     ct.SegmentID,
			PositionIndex: ct.PositionIndex,
			LedStart:      ct.LedStart,
			LedCount:      ct.LedCount,
			Config:        cc,
		})
		containerIDs = append(containerIDs, ct.ID)
	}

	for i, ctID := range containerIDs {
		bins, err := s.store.GetBinsByContainer(ctx, ctID)
		if err != nil {
			continue
		}
		for _, b := range bins {
			var ledIndex *int
			if b.LedIndex.Valid {
				v := int(b.LedIndex.Int64)
				ledIndex = &v
			}
			cfg.Bins = append(cfg.Bins, binConfig{
				ContainerIndex: i,
				X:              int(b.GridX.Int64),
				Y:              int(b.GridY.Int64),
				LedIndex:       ledIndex,
				Width:          clampWidth(int(b.Width.Int64)),
				Name:           b.Name,
			})
		}
	}

	return json.MarshalIndent(cfg, "", "  ")
}

// ImportConfig reads a hardware config JSON payload and creates a new controller
// with its containers and bins. Optional name/ip/port overrides take precedence
// over the values embedded in the file.
func (s *service) ImportConfig(ctx context.Context, name, ip string, port int64, data []byte) (int64, error) {
	var cfg hardwareConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("invalid hardware config json: %w", err)
	}
	if cfg.Version == "" {
		return 0, fmt.Errorf("unsupported or missing hardware config version")
	}
	if cfg.Version != hwConfigVersion {
		return 0, fmt.Errorf("unsupported hardware config version %q", cfg.Version)
	}

	// Determine the coordinate space of the imported config. An unmarked config
	// is segment-relative: that is the contract the existing hardware config
	// format has always used.
	cfgSpace := cfg.BinIndexSpace
	if cfgSpace == "" {
		cfgSpace = ledspace.Segment
	}
	switch cfgSpace {
	case ledspace.Segment, ledspace.Drawer:
	default:
		return 0, fmt.Errorf("%w: unsupported bin_index_space %q", ErrInvalidAllocation, cfg.BinIndexSpace)
	}

	// Optional overrides from the import form.
	if name != "" {
		cfg.Controller.Name = name
	}
	if ip != "" {
		cfg.Controller.IpAddress = ip
	}
	if port != 0 {
		cfg.Controller.Port = port
	}
	if cfg.Controller.IpAddress == "" {
		return 0, fmt.Errorf("controller IP address is required")
	}

	// Derive allocations for imported containers that do not specify one, then
	// validate the resulting set.
	segEnd := map[int64]int64{}
	resolved := make([]db.Container, 0, len(cfg.Containers))
	for i := range cfg.Containers {
		ct := &cfg.Containers[i]
		if ct.LedCount <= 0 {
			ct.LedCount = ct.Config.Length()
			ct.LedStart = segEnd[ct.SegmentID]
		}
		resolved = append(resolved, db.Container{
			Name:      ct.Name,
			SegmentID: ct.SegmentID,
			LedStart:  ct.LedStart,
			LedCount:  ct.LedCount,
		})
		if end := ct.LedStart + ct.LedCount; end > segEnd[ct.SegmentID] {
			segEnd[ct.SegmentID] = end
		}
	}
	if err := ValidateAllocations(resolved); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrInvalidAllocation, err)
	}

	var newID int64
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		// Read the target coordinate space inside the transaction so the imported
		// indices are normalised and validated against a single consistent state.
		// Importing into an unresolved database is rejected: the imported indices
		// cannot be placed in a known coordinate space.
		dbSpace, err := ledspace.Current(ctx, q)
		if err != nil {
			return fmt.Errorf("failed to read LED coordinate space: %w", err)
		}
		if dbSpace == ledspace.Unresolved {
			return fmt.Errorf("%w: database LED coordinate space is unresolved", ErrInvalidAllocation)
		}

		// Normalise every mapped imported bin into the database coordinate space
		// using its drawer's segment-relative allocation, then validate it in that
		// space. This keeps a cross-space import consistent: the imported indices
		// are fully converted, never partially or mixed.
		binRows := make([]binMappingRow, len(cfg.Bins))
		for i := range cfg.Bins {
			b := cfg.Bins[i]
			if b.ContainerIndex < 0 || b.ContainerIndex >= len(resolved) {
				return fmt.Errorf("%w: bin %q references invalid container index %d", ErrInvalidAllocation, b.Name, b.ContainerIndex)
			}
			idx := b.LedIndex
			if idx != nil {
				normalised, err := normaliseBinIndex(cfgSpace, dbSpace, int64(*idx), resolved[b.ContainerIndex])
				if err != nil {
					return fmt.Errorf("%w: bin %q: %v", ErrInvalidAllocation, b.Name, err)
				}
				v := int(normalised)
				idx = &v
			}
			binRows[i] = binMappingRow{
				ContainerIndex: b.ContainerIndex,
				Name:           b.Name,
				LedIndex:       idx,
				Width:          b.Width,
			}
		}
		if err := validateBinMappingsInSpace(dbSpace, binRows, resolved); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidAllocation, err)
		}

		row, err := q.CreateController(ctx, db.CreateControllerParams{
			Name:      cfg.Controller.Name,
			IpAddress: cfg.Controller.IpAddress,
			Port:      sql.NullInt64{Int64: cfg.Controller.Port, Valid: cfg.Controller.Port != 0},
		})
		if err != nil {
			return err
		}
		newID = row.ID

		containerIDs := make([]int64, len(cfg.Containers))
		for i, ct := range cfg.Containers {
			configBytes, _ := json.Marshal(ct.Config)
			id, err := q.CreateContainer(ctx, db.CreateContainerParams{
				Name:          ct.Name,
				ControllerID:  row.ID,
				SegmentID:     ct.SegmentID,
				PositionIndex: ct.PositionIndex,
				LedStart:      ct.LedStart,
				LedCount:      ct.LedCount,
				ConfigJson:    sql.NullString{String: string(configBytes), Valid: true},
			})
			if err != nil {
				return err
			}
			containerIDs[i] = id
		}

		for i, b := range binRows {
			// Container references were validated above, so direct indexing is safe
			// here. The LED index has already been normalised into the database
			// coordinate space.
			_, err := q.CreateBin(ctx, db.CreateBinParams{
				Name:        b.Name,
				ContainerID: containerIDs[b.ContainerIndex],
				LedIndex:    nullIntFromPtr(b.LedIndex),
				Width:       sql.NullInt64{Int64: int64(clampWidth(b.Width)), Valid: true},
				GridX:       sql.NullInt64{Int64: int64(cfg.Bins[i].X), Valid: true},
				GridY:       sql.NullInt64{Int64: int64(cfg.Bins[i].Y), Valid: true},
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	audit.Log(ctx, s.store, "CREATE", "HARDWARE", newID, "Imported hardware config",
		nil, map[string]any{"name": cfg.Controller.Name, "ip_address": cfg.Controller.IpAddress})

	return newID, nil
}

// normaliseBinIndex converts a bin LED index from the config's coordinate space
// into the database's coordinate space using the drawer's segment-relative
// allocation. Same-space conversions are the identity. The caller validates the
// converted index against the drawer allocation in the target space.
func normaliseBinIndex(fromSpace, toSpace string, index int64, c db.Container) (int64, error) {
	if fromSpace == toSpace {
		return index, nil
	}
	switch {
	case fromSpace == ledspace.Segment && toSpace == ledspace.Drawer:
		// segment-absolute -> drawer-relative
		if index < c.LedStart {
			return 0, fmt.Errorf("led_index %d is before drawer %q allocation start %d", index, c.Name, c.LedStart)
		}
		return index - c.LedStart, nil
	case fromSpace == ledspace.Drawer && toSpace == ledspace.Segment:
		// drawer-relative -> segment-absolute
		if c.LedStart > math.MaxInt64-index {
			return 0, fmt.Errorf("drawer %q LED index overflows: start %d index %d", c.Name, c.LedStart, index)
		}
		return c.LedStart + index, nil
	default:
		return 0, fmt.Errorf("cannot convert bin index from %q to %q", fromSpace, toSpace)
	}
}
