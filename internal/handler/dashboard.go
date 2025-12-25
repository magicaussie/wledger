package handler

import (
	"net/http"

	"github.com/tuxedocurly/wledger/internal/auth"
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

	// Fetch Grid Data via Service
	controllers, err := h.Dashboard.GetGrid(ctx)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load dashboard grid", http.StatusInternalServerError)
		return
	}

	// Render Dashboard with Stats and Controllers
	pages.Dashboard(user, stats, controllers).Render(ctx, w)
}
