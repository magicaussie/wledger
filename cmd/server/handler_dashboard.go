package main

import (
	"net/http"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /
func (app *application) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)
	stats, err := app.queries.GetDashboardStats(r.Context())
	if err != nil {
		app.logger.Error("failed to get stats", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	pages.Dashboard(user, stats).Render(r.Context(), w)

}
