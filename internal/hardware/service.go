package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/tuxedocurly/wledger/internal/audit"
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
	row, err := s.store.CreateController(ctx, params)
	if err != nil {
		return row, err
	}

	summary := map[string]any{
		"id":         row.ID,
		"name":       row.Name,
		"ip_address": row.IpAddress,
	}
	audit.Log(ctx, s.store, "CREATE", "HARDWARE", row.ID, "Added controller "+row.Name, nil, summary)

	return row, nil
}

func (s *service) DeleteController(ctx context.Context, id int64) error {
	// Fetch before delete for logging
	c, err := s.store.GetController(ctx, id)
	if err != nil {
		return err
	}

	summary := map[string]any{
		"id":         c.ID,
		"name":       c.Name,
		"ip_address": c.IpAddress,
	}
	audit.Log(ctx, s.store, "DELETE", "HARDWARE", id, "Deleted controller", summary, nil)

	return s.store.DeleteController(ctx, id)
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

	// Fetch old count for logging before update
	var oldLedCount int
	existingBins, err := s.store.GetBinsByController(ctx, sql.NullInt64{Int64: controllerID, Valid: true})
	if err == nil {
		oldLedCount = len(existingBins)
	}

	var newLedCount int64

	err = s.store.ExecTx(ctx, func(q db.Querier) error {
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
				// INSERT NEW
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

		// Audit Log
		audit.Log(ctx, q, "UPDATE", "HARDWARE", controllerID, "Updated LED Grid Layout",
			map[string]any{"led_count": oldLedCount},
			map[string]any{"led_count": newLedCount})

		return nil
	})

	return newLedCount, err
}
