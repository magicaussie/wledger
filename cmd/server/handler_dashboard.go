package main

import (
	"net/http"
	"sort"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /
func (app *application) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	ctx := r.Context()

	// Fetch Stats (Existing functionality)
	stats, err := app.queries.GetDashboardStats(ctx)
	if err != nil {
		app.logger.Error("failed to get dashboard stats", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Fetch Grid Data (New functionality)
	gridRows, err := app.queries.GetDashboardGrid(ctx)
	if err != nil {
		app.logger.Error("failed to fetch dashboard grid", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	// Process Grid Data: Map rows to DashboardBin structs
	binMap := make(map[int64]*components.DashboardBin)

	for _, row := range gridRows {
		// Initialize bin if missing
		if _, exists := binMap[row.BinID]; !exists {
			binMap[row.BinID] = &components.DashboardBin{
				ID:       row.BinID,
				Name:     row.BinName,
				GridX:    int(row.GridX.Int64),
				GridY:    int(row.GridY.Int64),
				Statuses: []string{},
			}
		}

		// Calculate Status for Parts in this bin
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
			binMap[row.BinID].Statuses = append(binMap[row.BinID].Statuses, status)
		}
	}

	// Flatten map to slice
	var gridBins []components.DashboardBin
	for _, b := range binMap {
		gridBins = append(gridBins, *b)
	}

	// Sort for stability
	sort.Slice(gridBins, func(i, j int) bool {
		return gridBins[i].ID < gridBins[j].ID
	})

	// Render Dashboard with Stats and Grid
	pages.Dashboard(user, stats, gridBins).Render(ctx, w)
}
