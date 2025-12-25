package dashboard

import (
	"context"
	"sort"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/components"
)

type Service interface {
	GetStats(ctx context.Context) (db.GetDashboardStatsRow, error)
	GetGrid(ctx context.Context) ([]components.DashboardController, error)
}

type service struct {
	store db.Store
}

func NewService(store db.Store) Service {
	return &service{
		store: store,
	}
}

func (s *service) GetStats(ctx context.Context) (db.GetDashboardStatsRow, error) {
	return s.store.GetDashboardStats(ctx)
}

func (s *service) GetGrid(ctx context.Context) ([]components.DashboardController, error) {
	gridRows, err := s.store.GetDashboardGrid(ctx)
	if err != nil {
		return nil, err
	}

	return s.newDashboardViewModel(gridRows), nil
}

func (s *service) newDashboardViewModel(gridRows []db.GetDashboardGridRow) []components.DashboardController {
	// Process Grid Data: Group by Controller
	// Map: ControllerID -> *DashboardController
	ctrlMap := make(map[int64]*components.DashboardController)
	// Map: ControllerID -> (Map: BinID -> *DashboardBin)
	// Needed to efficiently aggregate part statuses into unique bins per controller
	ctrlBinMap := make(map[int64]map[int64]*components.DashboardBin)

	for _, row := range gridRows {
		// Get or Create Controller
		if _, exists := ctrlMap[row.ControllerID]; !exists {
			ctrlMap[row.ControllerID] = &components.DashboardController{
				ID:   row.ControllerID,
				Name: row.ControllerName,
				Bins: nil, // Populated later
			}
			ctrlBinMap[row.ControllerID] = make(map[int64]*components.DashboardBin)
		}

		// Get or Create Bin within Controller
		cBins := ctrlBinMap[row.ControllerID]
		if _, exists := cBins[row.BinID]; !exists {
			cBins[row.BinID] = &components.DashboardBin{
				ID:       row.BinID,
				Name:     row.BinName,
				GridX:    int(row.GridX.Int64),
				GridY:    int(row.GridY.Int64),
				Statuses: []string{},
			}
		}
		bin := cBins[row.BinID]

		// Calculate and Append Status (if part exists)
		if row.PartID.Valid {
			status := "ok"
			qty := row.Quantity.Int64
			min := row.MinStockThreshold.Int64
			reorder := row.ReorderLevel.Int64

			if qty <= min {
				status = "critical"
			} else if qty <= reorder {
				status = "low"
			}
			bin.Statuses = append(bin.Statuses, status)
		}
	}

	// Flatten Maps to Slices and Sort
	var controllers []components.DashboardController
	for cID, ctrl := range ctrlMap {
		// Flatten Bins for this controller
		var bins []components.DashboardBin
		for _, b := range ctrlBinMap[cID] {
			bins = append(bins, *b)
		}

		// Sort Bins (Grid Order: Y then X)
		sort.Slice(bins, func(i, j int) bool {
			if bins[i].GridY == bins[j].GridY {
				return bins[i].GridX < bins[j].GridX
			}
			return bins[i].GridY < bins[j].GridY
		})

		ctrl.Bins = bins
		controllers = append(controllers, *ctrl)
	}

	// Sort Controllers by Name
	sort.Slice(controllers, func(i, j int) bool {
		return controllers[i].Name < controllers[j].Name
	})

	return controllers
}
