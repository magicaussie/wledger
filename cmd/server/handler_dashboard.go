package main

import (
	"net/http"

	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /
func (app *application) handleDashboard(w http.ResponseWriter, r *http.Request) {
	stats, err := app.queries.GetDashboardStats(r.Context())
	if err != nil {
		app.logger.Error("failed to get stats", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	pages.Dashboard(stats).Render(r.Context(), w)
}
