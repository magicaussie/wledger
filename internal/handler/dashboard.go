package handler

import (
	"net/http"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/components"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /
func (h *Handler) HandleDashboard(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	ctx := r.Context()

	// Fetch Stats via Service
	stats, err := h.Dashboard.GetStats(ctx)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load dashboard stats", http.StatusInternalServerError)
		return
	}

	// Fetch All Walls with Containers
	dashboardWalls, err := h.Dashboard.GetAllWallsWithContainers(ctx)
	if err != nil {
		// Log error but proceed? Or show empty?
		// For now, empty list
		dashboardWalls = []components.DashboardWall{}
	}

	// Legacy Dashboard Support: If no walls exist, fetch all controllers/bins for a default view
	var legacyControllers []components.DashboardController
	if len(dashboardWalls) == 0 {
		legacyControllers, _ = h.Dashboard.GetGrid(ctx)
	}
	if legacyControllers == nil {
		legacyControllers = []components.DashboardController{}
	}

	// Render Dashboard
	pages.Dashboard(user, stats, dashboardWalls, legacyControllers).Render(ctx, w)
}
