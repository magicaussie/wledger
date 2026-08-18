package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
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
	Config        mapper.ContainerConfig `json:"config"`
}

type binConfig struct {
	ContainerIndex int    `json:"container_index"`
	X              int    `json:"x"`
	Y              int    `json:"y"`
	LedIndex       int    `json:"led_index"`
	Width          int    `json:"width"`
	Name           string `json:"name"`
}

type hardwareConfig struct {
	Version    string            `json:"version"`
	ExportedAt time.Time         `json:"exported_at"`
	Controller controllerConfig  `json:"controller"`
	Containers []containerConfig `json:"containers"`
	Bins       []binConfig       `json:"bins"`
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

	cfg := hardwareConfig{
		Version:    hwConfigVersion,
		ExportedAt: time.Now(),
		Controller: controllerConfig{Name: c.Name, IpAddress: c.IpAddress, Port: c.Port.Int64},
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
			cfg.Bins = append(cfg.Bins, binConfig{
				ContainerIndex: i,
				X:              int(b.GridX.Int64),
				Y:              int(b.GridY.Int64),
				LedIndex:       int(b.LedIndex.Int64),
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

	var newID int64
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
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
				ConfigJson:    sql.NullString{String: string(configBytes), Valid: true},
			})
			if err != nil {
				return err
			}
			containerIDs[i] = id
		}

		for _, b := range cfg.Bins {
			if b.ContainerIndex < 0 || b.ContainerIndex >= len(containerIDs) {
				continue
			}
			_, err := q.CreateBin(ctx, db.CreateBinParams{
				Name:        b.Name,
				ContainerID: containerIDs[b.ContainerIndex],
				LedIndex:    sql.NullInt64{Int64: int64(b.LedIndex), Valid: true},
				Width:       sql.NullInt64{Int64: int64(clampWidth(b.Width)), Valid: true},
				GridX:       sql.NullInt64{Int64: int64(b.X), Valid: true},
				GridY:       sql.NullInt64{Int64: int64(b.Y), Valid: true},
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
