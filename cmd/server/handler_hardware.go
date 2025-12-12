package main

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /hardware
func (app *application) handleHardwareList(w http.ResponseWriter, r *http.Request) {
	controllers, err := app.queries.GetControllers(r.Context())
	if err != nil {
		app.logger.Error("failed to fetch hardware", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	pages.Hardware(controllers).Render(r.Context(), w)
}

// POST /hardware
func (app *application) handleHardwareCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	ip := r.FormValue("ip_address")
	portStr := r.FormValue("port")
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 80
	}

	_, err := app.queries.CreateController(r.Context(), db.CreateControllerParams{
		Name:      name,
		IpAddress: ip,
		Port:      sql.NullInt64{Int64: int64(port), Valid: true},
	})

	if err != nil {
		app.logger.Error("failed to create controller", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), app.queries, "CREATE", "HARDWARE", 0, "Added controller "+name, nil, nil)
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/{id}/delete
func (app *application) handleHardwareDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	err := app.queries.DeleteController(r.Context(), int64(id))
	if err != nil {
		app.logger.Error("failed to delete controller", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	audit.Log(r.Context(), app.queries, "DELETE", "HARDWARE", int64(id), "Deleted controller", nil, nil)
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// GET /hardware/{id}/status
func (app *application) handleHardwareStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	c, err := app.queries.GetController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	online, _ := app.wled.Ping(r.Context(), c.IpAddress)

	if online != c.IsOnline.Bool {
		_ = app.queries.UpdateControllerStatus(r.Context(), db.UpdateControllerStatusParams{
			IsOnline: sql.NullBool{Bool: online, Valid: true},
			ID:       c.ID,
		})
	}

	if online {
		w.Write([]byte(`<div class="badge badge-success gap-2">Online</div>`))
	} else {
		w.Write([]byte(`<div class="badge badge-error gap-2">Offline</div>`))
	}
}

// GET /hardware/{id}/grid
func (app *application) handleHardwareGrid(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	c, err := app.queries.GetController(r.Context(), int64(id))
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	// Use sql.NullInt64 for ID
	bins, err := app.queries.GetBinsByController(r.Context(), sql.NullInt64{Int64: int64(id), Valid: true})

	// Explicitly define 'bins' as a slice of db.Bin
	if err != nil {
		bins = []db.Bin{}
	}

	pages.HardwareGrid(c, bins).Render(r.Context(), w)
}

// POST /hardware/{id}/grid
func (app *application) handleHardwareGridSave(w http.ResponseWriter, r *http.Request) {
	controllerID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	r.ParseForm()

	for key, values := range r.Form {
		if len(values) > 0 && len(key) > 4 && key[:4] == "bin_" {
			ledIndex, _ := strconv.Atoi(key[4:])
			binName := values[0]

			if binName == "" {
				app.queries.DeleteBinByLed(r.Context(), db.DeleteBinByLedParams{
					ControllerID: sql.NullInt64{Int64: int64(controllerID), Valid: true},
					LedIndex:     sql.NullInt64{Int64: int64(ledIndex), Valid: true},
				})
			} else {
				app.queries.UpsertBin(r.Context(), db.UpsertBinParams{
					Name:         binName,
					ControllerID: sql.NullInt64{Int64: int64(controllerID), Valid: true},
					LedIndex:     sql.NullInt64{Int64: int64(ledIndex), Valid: true},
					Width:        sql.NullInt64{Int64: 1, Valid: true},
				})
			}
		}
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/off
func (app *application) handleGlobalOff(w http.ResponseWriter, r *http.Request) {
	controllers, err := app.queries.GetControllers(r.Context())
	if err != nil {
		app.logger.Error("failed to list controllers", "error", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	app.logger.Info("triggering global off", "controllers", len(controllers))

	for _, c := range controllers {
		go func(ctrlName, ip string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := app.wled.Clear(ctx, ip)
			if err != nil {
				app.logger.Error("failed to clear controller", "name", ctrlName, "ip", ip, "error", err)
			} else {
				app.logger.Info("cleared controller", "name", ctrlName, "ip", ip)
			}
		}(c.Name, c.IpAddress)
	}

	w.WriteHeader(http.StatusOK)
}

// POST /hardware/{id}/locate?bin_id=X
func (app *application) handleHardwareLocate(w http.ResponseWriter, r *http.Request) {
	cidStr := chi.URLParam(r, "id")
	cid, _ := strconv.Atoi(cidStr)
	binID, _ := strconv.Atoi(r.URL.Query().Get("bin_id"))

	controller, err := app.queries.GetController(r.Context(), int64(cid))
	if err != nil {
		app.logger.Error("locate failed: controller not found", "cid", cid)
		http.Error(w, "Controller not found", http.StatusNotFound)
		return
	}

	bin, err := app.queries.GetBin(r.Context(), int64(binID))
	if err != nil {
		app.logger.Error("locate failed: bin not found", "binID", binID)
		http.Error(w, "Bin not found", http.StatusNotFound)
		return
	}

	settings, err := app.queries.GetSettings(r.Context())
	if err != nil {
		settings.ColorLocate.String = "#0000FF"
		settings.EnableLocateTimeout.Bool = false
		settings.LocateTimeoutSeconds.Int64 = 0
	}

	ledIndex := int(bin.LedIndex.Int64)
	width := int(bin.Width.Int64)
	if width < 1 {
		width = 1
	}

	err = app.wled.LightUp(r.Context(), controller.IpAddress, ledIndex, width, settings.ColorLocate.String)
	if err != nil {
		app.logger.Error("failed to locate bin", "error", err, "ip", controller.IpAddress)
	}

	if settings.EnableLocateTimeout.Bool && settings.LocateTimeoutSeconds.Int64 > 0 {
		timeoutDuration := time.Duration(settings.LocateTimeoutSeconds.Int64) * time.Second

		go func(ip string, idx, count int, duration time.Duration) {
			time.Sleep(duration)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = app.wled.LightUp(ctx, ip, idx, count, "#000000")
		}(controller.IpAddress, ledIndex, width, timeoutDuration)
	}

	w.WriteHeader(http.StatusOK)
}
