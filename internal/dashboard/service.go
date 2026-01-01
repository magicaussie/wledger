package dashboard

import (
	"context"
	"database/sql"
	"sort"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/components"
)

type Service interface {
	GetStats(ctx context.Context) (db.GetDashboardStatsRow, error)
	GetGrid(ctx context.Context) ([]components.DashboardController, error)
	GetWalls(ctx context.Context) ([]db.Wall, error)
	GetWallWithContainers(ctx context.Context, wallID int64) ([]components.DashboardContainer, error)
	GetAllWallsWithContainers(ctx context.Context) ([]components.DashboardWall, error)
	CreateWall(ctx context.Context, name, description string) (int64, error)
	UpdateWall(ctx context.Context, id int64, name, description string, containerIDs []int64) error
	DeleteWall(ctx context.Context, id int64) error
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

func (s *service) GetWalls(ctx context.Context) ([]db.Wall, error) {
	return s.store.GetWalls(ctx)
}

func (s *service) GetWallWithContainers(ctx context.Context, wallID int64) ([]components.DashboardContainer, error) {
	rows, err := s.store.GetWallContainerBins(ctx, wallID)
	if err != nil {
		return nil, err
	}

	// Group by Container
	containerMap := make(map[int64]*components.DashboardContainer)
	binMap := make(map[int64]map[int64]*components.DashboardBin) // containerID -> binID -> DashboardBin

	for _, row := range rows {
		if _, exists := containerMap[row.ContainerID]; !exists {
			containerMap[row.ContainerID] = &components.DashboardContainer{
				ID:               row.ContainerID,
				Name:             row.ContainerName,
				SegmentID:        row.SegmentID,
				ControllerName:   row.ControllerName,
				ControllerOnline: row.IsOnline.Bool,
			}
			binMap[row.ContainerID] = make(map[int64]*components.DashboardBin)
		}

		if !row.BinID.Valid {
			continue
		}

		cBins := binMap[row.ContainerID]
		if _, exists := cBins[row.BinID.Int64]; !exists {
			cBins[row.BinID.Int64] = &components.DashboardBin{
				ID:       row.BinID.Int64,
				Name:     row.BinName.String,
				GridX:    int(row.GridX.Int64),
				GridY:    int(row.GridY.Int64),
				Statuses: []string{},
			}
		}
		bin := cBins[row.BinID.Int64]

		if row.PartID.Valid {
			status := "ok"
			if row.Quantity.Int64 <= row.MinStockThreshold.Int64 {
				status = "critical"
			} else if row.Quantity.Int64 <= row.ReorderLevel.Int64 {
				status = "low"
			}
			bin.Statuses = append(bin.Statuses, status)
		}
	}

	var results = []components.DashboardContainer{}
	for cID, c := range containerMap {
		var bins []components.DashboardBin
		for _, b := range binMap[cID] {
			bins = append(bins, *b)
		}
		sort.Slice(bins, func(i, j int) bool {
			if bins[i].GridY == bins[j].GridY {
				return bins[i].GridX < bins[j].GridX
			}
			return bins[i].GridY < bins[j].GridY
		})
		c.Bins = bins
		results = append(results, *c)
	}

	// re-fetch position mapping to sort correctly
	posMap := make(map[int64]int64)
	for _, row := range rows {
		posMap[row.ContainerID] = row.PositionIndex
	}

	sort.Slice(results, func(i, j int) bool {
		return posMap[results[i].ID] < posMap[results[j].ID]
	})

	return results, nil
}

func (s *service) GetAllWallsWithContainers(ctx context.Context) ([]components.DashboardWall, error) {
	// Get All Walls
	walls, err := s.store.GetWalls(ctx)
	if err != nil {
		return nil, err
	}

	// Get All Cards (Bins for all walls)
	rows, err := s.store.GetAllWallContainerBins(ctx)
	if err != nil {
		return nil, err
	}

	// Group by Wall -> Container -> Bin
	// Map: WallID -> (Map: ContainerID -> *DashboardContainer)
	wallMap := make(map[int64]*components.DashboardWall)
	wallContainerMap := make(map[int64]map[int64]*components.DashboardContainer)
	binMap := make(map[int64]map[int64]*components.DashboardBin) // containerID -> binID -> Bin

	// Initialize Walls
	for _, w := range walls {
		wallMap[w.ID] = &components.DashboardWall{
			ID:          w.ID,
			Name:        w.Name,
			Description: w.Description.String,
			Containers:  []components.DashboardContainer{},
		}
		wallContainerMap[w.ID] = make(map[int64]*components.DashboardContainer)
	}

	for _, row := range rows {
		wallID := row.WallID
		if _, ok := wallMap[wallID]; !ok {
			continue // Should not happen if walls fetched correctly
		}

		cMap := wallContainerMap[wallID]
		if _, exists := cMap[row.ContainerID]; !exists {
			cMap[row.ContainerID] = &components.DashboardContainer{
				ID:               row.ContainerID,
				Name:             row.ContainerName,
				SegmentID:        row.SegmentID,
				ControllerName:   row.ControllerName,
				ControllerOnline: row.IsOnline.Bool,
			}
			binMap[row.ContainerID] = make(map[int64]*components.DashboardBin)
		}

		if !row.BinID.Valid {
			continue
		}

		cBins := binMap[row.ContainerID]
		if _, exists := cBins[row.BinID.Int64]; !exists {
			cBins[row.BinID.Int64] = &components.DashboardBin{
				ID:       row.BinID.Int64,
				Name:     row.BinName.String,
				GridX:    int(row.GridX.Int64),
				GridY:    int(row.GridY.Int64),
				Statuses: []string{},
			}
		}
		bin := cBins[row.BinID.Int64]

		if row.PartID.Valid {
			status := "ok"
			if row.Quantity.Int64 <= row.MinStockThreshold.Int64 {
				status = "critical"
			} else if row.Quantity.Int64 <= row.ReorderLevel.Int64 {
				status = "low"
			}
			bin.Statuses = append(bin.Statuses, status)
		}
	}

	// Flatten and Sort
	var result []components.DashboardWall

	// Create map of position indices for sorting
	// Map: WallID -> (Map: ContainerID -> PositionIndex)
	posMap := make(map[int64]map[int64]int64)
	for _, row := range rows {
		if _, ok := posMap[row.WallID]; !ok {
			posMap[row.WallID] = make(map[int64]int64)
		}
		posMap[row.WallID][row.ContainerID] = row.PositionIndex
	}

	for _, w := range walls {
		dashboardWall := wallMap[w.ID]

		// Flatten containers for this wall
		var containers []components.DashboardContainer
		for cID, c := range wallContainerMap[w.ID] {
			// Flatten bins
			var bins []components.DashboardBin
			for _, b := range binMap[cID] {
				bins = append(bins, *b)
			}
			sort.Slice(bins, func(i, j int) bool {
				if bins[i].GridY == bins[j].GridY {
					return bins[i].GridX < bins[j].GridX
				}
				return bins[i].GridY < bins[j].GridY
			})
			c.Bins = bins
			containers = append(containers, *c)
		}

		// Sort Containers by Position
		wallPosMap := posMap[w.ID]
		sort.Slice(containers, func(i, j int) bool {
			return wallPosMap[containers[i].ID] < wallPosMap[containers[j].ID]
		})

		dashboardWall.Containers = containers
		result = append(result, *dashboardWall)
	}

	// Walls are already sorted by name from GetWalls query (if sorted in SQL)
	// If additional sorting is needed, implement here.

	return result, nil
}

func (s *service) DeleteWall(ctx context.Context, id int64) error {
	return s.store.DeleteWall(ctx, id)
}

func (s *service) CreateWall(ctx context.Context, name, description string) (int64, error) {
	return s.store.CreateWall(ctx, db.CreateWallParams{
		Name:        name,
		Description: sql.NullString{String: description, Valid: description != ""},
	})
}

func (s *service) UpdateWall(ctx context.Context, id int64, name, description string, containerIDs []int64) error {
	return s.store.ExecTx(ctx, func(q db.Querier) error {
		// Update Wall Metadata
		err := q.UpdateWall(ctx, db.UpdateWallParams{
			ID:          id,
			Name:        name,
			Description: sql.NullString{String: description, Valid: description != ""},
		})
		if err != nil {
			return err
		}

		err = q.DeleteWallCardsByWallID(ctx, id)
		if err != nil {
			return err
		}

		for i, cID := range containerIDs {
			err = q.AddContainerToWall(ctx, db.AddContainerToWallParams{
				WallID:        id,
				ContainerID:   cID,
				PositionIndex: int64(i),
				ConfigJson:    sql.NullString{Valid: false},
			})
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func (s *service) GetGrid(ctx context.Context) ([]components.DashboardController, error) {
	gridRows, err := s.store.GetDashboardGrid(ctx)
	if err != nil {
		return nil, err
	}

	return s.newDashboardViewModel(gridRows), nil
}

func (s *service) newDashboardViewModel(gridRows []db.GetDashboardGridRow) []components.DashboardController {
	// ... (Existing implementation preserved for transition)
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
	var controllers = []components.DashboardController{}
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
