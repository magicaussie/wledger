package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

type Service interface {
	ListControllers(ctx context.Context) ([]db.Controller, error)
	GetController(ctx context.Context, id int64) (db.Controller, error)
	CreateController(ctx context.Context, params db.CreateControllerParams) (db.CreateControllerRow, error)
	DeleteController(ctx context.Context, id int64) error
	UpdateStatus(ctx context.Context, id int64) (bool, error)
	GetBinsByController(ctx context.Context, id int64) ([]db.Bin, error)
	SaveGrid(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string) (int64, error)
	Locate(ctx context.Context, controllerID, binID int64) error
}

type service struct {
	store  db.Store
	wled   *wled.Client
	logger *slog.Logger
}

func NewService(store db.Store, wledClient *wled.Client, logger *slog.Logger) Service {
	return &service{
		store:  store,
		wled:   wledClient,
		logger: logger,
	}
}

func (s *service) ListControllers(ctx context.Context) ([]db.Controller, error) {
	return s.store.GetControllers(ctx)
}

func (s *service) GetController(ctx context.Context, id int64) (db.Controller, error) {
	return s.store.GetController(ctx, id)
}

func (s *service) CreateController(ctx context.Context, params db.CreateControllerParams) (db.CreateControllerRow, error) {
	return s.store.CreateController(ctx, params)
}

func (s *service) DeleteController(ctx context.Context, id int64) error {
	return s.store.ExecTx(ctx, func(q db.Querier) error {
		// Delete Bins First (Manual Cascade - though DB has ON DELETE CASCADE, 
		// the audit report suggested we can trust the schema, but for now 
		// we follow the plan to move existing logic).
		err := q.DeleteBinsByController(ctx, sql.NullInt64{Int64: id, Valid: true})
		if err != nil {
			return err
		}

		return q.DeleteController(ctx, id)
	})
}

func (s *service) UpdateStatus(ctx context.Context, id int64) (bool, error) {
	c, err := s.store.GetController(ctx, id)
	if err != nil {
		return false, err
	}

	online, _ := s.wled.Ping(ctx, c.IpAddress)

	if online != c.IsOnline.Bool {
		err := s.store.UpdateControllerStatus(ctx, db.UpdateControllerStatusParams{
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
	return s.store.GetBinsByController(ctx, sql.NullInt64{Int64: id, Valid: true})
}

type gridCellData struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	LedIndex int    `json:"led_index"`
	Name     string `json:"name"`
}

func (s *service) SaveGrid(ctx context.Context, controllerID int64, gridDataJSON string, configJSON string) (int64, error) {
	var newCells []gridCellData
	if err := json.Unmarshal([]byte(gridDataJSON), &newCells); err != nil {
		return 0, fmt.Errorf("invalid grid json: %w", err)
	}

	var newLedCount int64

	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		// Fetch Existing Bins
		existingBins, err := q.GetBinsByController(ctx, sql.NullInt64{Int64: controllerID, Valid: true})
		if err != nil {
			return err
		}

		// Build Map for Diffing: [LedIndex] -> Bin
		existingMap := make(map[int64]db.Bin)
		for _, b := range existingBins {
			if b.LedIndex.Valid {
				existingMap[b.LedIndex.Int64] = b
			}
		}

		maxLedIndex := 0
		// Process Incoming Grid Data
		for _, cell := range newCells {
			if cell.LedIndex > maxLedIndex {
				maxLedIndex = cell.LedIndex
			}

			ledIdx := int64(cell.LedIndex)

			if _, exists := existingMap[ledIdx]; exists {
				// UPDATE EXISTING
				err := q.UpsertBin(ctx, db.UpsertBinParams{
					Name:         cell.Name,
					ControllerID: sql.NullInt64{Int64: controllerID, Valid: true},
					LedIndex:     sql.NullInt64{Int64: ledIdx, Valid: true},
					Width:        sql.NullInt64{Int64: 1, Valid: true},
					GridX:        sql.NullInt64{Int64: int64(cell.X), Valid: true},
					GridY:        sql.NullInt64{Int64: int64(cell.Y), Valid: true},
				})
				if err != nil {
					return err
				}
				// Remove from map to mark as "kept"
				delete(existingMap, ledIdx)

			} else {
				// --- INSERT NEW ---
				_, err := q.CreateBin(ctx, db.CreateBinParams{
					Name:         cell.Name,
					ControllerID: sql.NullInt64{Int64: controllerID, Valid: true},
					LedIndex:     sql.NullInt64{Int64: ledIdx, Valid: true},
					Width:        sql.NullInt64{Int64: 1, Valid: true},
					GridX:        sql.NullInt64{Int64: int64(cell.X), Valid: true},
					GridY:        sql.NullInt64{Int64: int64(cell.Y), Valid: true},
				})
				if err != nil {
					return err
				}
			}
		}

		// Handle Deletions (Orphan Logic)
		for _, binToDelete := range existingMap {
			err := q.DeleteBinByLed(ctx, db.DeleteBinByLedParams{
				ControllerID: sql.NullInt64{Int64: controllerID, Valid: true},
				LedIndex:     binToDelete.LedIndex,
			})
			if err != nil {
				s.logger.Error("failed to delete removed bin", "id", binToDelete.ID, "err", err)
			}
		}

		newLedCount = int64(maxLedIndex + 1)

		// Update Controller Config
		if configJSON != "" {
			err := q.UpdateControllerConfig(ctx, db.UpdateControllerConfigParams{
				ConfigJson: sql.NullString{String: configJSON, Valid: true},
				LedCount:   newLedCount,
				ID:         controllerID,
			})
			if err != nil {
				return err
			}
		}

		return nil
	})

	return newLedCount, err
}

func (s *service) Locate(ctx context.Context, controllerID, binID int64) error {
	// Fetch Settings
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		s.logger.Warn("failed to fetch settings for locate, using defaults", "err", err)
		settings.ColorLocate.String = "#0000FF"
		settings.EnableLocateTimeout.Bool = false
		settings.LocateTimeoutSeconds.Int64 = 0
	}

	// Retrieve Hardware Details
	controller, err := s.store.GetController(ctx, controllerID)
	if err != nil {
		return fmt.Errorf("controller not found: %w", err)
	}

	bin, err := s.store.GetBin(ctx, binID)
	if err != nil {
		return fmt.Errorf("bin not found: %w", err)
	}

	// Calculate LED Positions
	ledIndex := int(bin.LedIndex.Int64)
	width := int(bin.Width.Int64)
	if width < 1 {
		width = 1
	}

	// Trigger WLED
	err = s.wled.LightUp(ctx, controller.IpAddress, ledIndex, width, settings.ColorLocate.String)
	if err != nil {
		s.logger.Error("failed to locate bin", "err", err, "ip", controller.IpAddress)
		return err
	}

	// Handle Auto-Off Timer
	if settings.EnableLocateTimeout.Bool && settings.LocateTimeoutSeconds.Int64 > 0 {
		timeoutDuration := time.Duration(settings.LocateTimeoutSeconds.Int64) * time.Second

		go func(ip string, idx, count int, duration time.Duration) {
			time.Sleep(duration)
			// Create a new context since the original might be cancelled
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			_ = s.wled.LightUp(bgCtx, ip, idx, count, "#000000")
		}(controller.IpAddress, ledIndex, width, timeoutDuration)
	}

	return nil
}
