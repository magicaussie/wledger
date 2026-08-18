package handler

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/qrcode"
	"github.com/tuxedocurly/wledger/web/pages"
)

// GET /hardware
func (h *Handler) HandleHardwareList(w http.ResponseWriter, r *http.Request) {
	// Get User
	user := auth.GetUserFromRequest(r)

	controllers, err := h.Hardware.ListControllers(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch hardware list", http.StatusInternalServerError)
		return
	}

	// Pass User to Template
	pages.Hardware(user, controllers).Render(r.Context(), w)
}

// POST /hardware
func (h *Handler) HandleHardwareCreate(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("name")
	ip := r.FormValue("ip_address")

	// Ensure IP is not empty
	if ip == "" {
		h.UIError.Respond(w, r, nil, "IP Address is required", http.StatusBadRequest)
		return
	}

	portStr := r.FormValue("port")
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 80
	}

	_, err := h.Hardware.CreateController(r.Context(), db.CreateControllerParams{
		Name:      name,
		IpAddress: ip,
		Port:      sql.NullInt64{Int64: int64(port), Valid: true},
	})

	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to create controller", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/{id}/delete
func (h *Handler) HandleHardwareDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	err := h.Hardware.DeleteController(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to delete hardware", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// GET /hardware/{id}/status
func (h *Handler) HandleHardwareStatus(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	online, err := h.Hardware.UpdateStatus(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
		return
	}

	if online {
		w.Write([]byte(`<div class="badge badge-success gap-2">Online</div>`))
	} else {
		w.Write([]byte(`<div class="badge badge-error gap-2">Offline</div>`))
	}
}

// GET /hardware/{id}/grid
func (h *Handler) HandleHardwareGrid(w http.ResponseWriter, r *http.Request) {
	// Get User
	user := auth.GetUserFromRequest(r)

	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	c, err := h.Hardware.GetController(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
		return
	}

	bins, err := h.Hardware.GetBinsByController(r.Context(), int64(id))
	if err != nil {
		bins = []db.Bin{}
	}

	containers, err := h.Hardware.GetContainers(r.Context(), int64(id))
	if err != nil {
		containers = []db.Container{}
	}

	// Pass User to Template
	pages.HardwareGrid(user, c, containers, bins).Render(r.Context(), w)
}

// POST /hardware/{id}/grid
func (h *Handler) HandleHardwareGridSave(w http.ResponseWriter, r *http.Request) {
	controllerID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	ctx := r.Context()

	// Parse Payload
	gridDataJSON := r.FormValue("grid_data")
	configJSON := r.FormValue("config_data")

	_, err := h.Hardware.SaveGrid(ctx, int64(controllerID), gridDataJSON, configJSON)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to save grid layout", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/off
func (h *Handler) HandleGlobalOff(w http.ResponseWriter, r *http.Request) {
	err := h.WLED.GlobalOff(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to trigger global off", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// POST /hardware/{id}/locate
func (h *Handler) HandleHardwareLocate(w http.ResponseWriter, r *http.Request) {
	cidStr := chi.URLParam(r, "id")
	cid, _ := strconv.Atoi(cidStr)
	binID, _ := strconv.Atoi(r.URL.Query().Get("bin_id"))

	err := h.WLED.LocateBin(r.Context(), int64(cid), int64(binID))
	if err != nil {
		h.UIError.Respond(w, r, err, "Locate failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GET /hardware/{id}/export — download a controller's full grid config as JSON.
func (h *Handler) HandleHardwareExport(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	data, err := h.Hardware.ExportConfig(r.Context(), int64(id))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to export hardware config", http.StatusInternalServerError)
		return
	}

	c, err := h.Hardware.GetController(r.Context(), int64(id))
	if err != nil {
		c.Name = fmt.Sprintf("controller-%d", id)
	}

	filename := fmt.Sprintf("hardware_%s_%s.json", safeFilename(c.Name), time.Now().Format("20060102"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	w.Write(data)
}

// POST /hardware/import — create a new controller (with its grid layout) from a
// JSON config file. Optional name/ip/port override fields take precedence.
func (h *Handler) HandleHardwareImport(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(config.MaxUploadSizeImport)
	if err != nil {
		h.UIError.Respond(w, r, err, "Upload too large or invalid", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, _, err := r.FormFile("config_file")
	if err != nil {
		h.UIError.Respond(w, r, err, "No config file provided", http.StatusBadRequest)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to read config file", http.StatusBadRequest)
		return
	}

	name := r.FormValue("name")
	ip := r.FormValue("ip_address")
	port, _ := strconv.Atoi(r.FormValue("port"))

	_, err = h.Hardware.ImportConfig(r.Context(), name, ip, int64(port), data)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to import hardware config", http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// safeFilename sanitizes a controller name for use in a download filename.
func safeFilename(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	s = strings.Trim(s, "-")
	if s == "" {
		return "controller"
	}
	return s
}

// GET /bin/{id}/qr — returns a QR PNG for a bin's scan code. Scanning the code
// routes to /parts?bin=<id> so stock in that physical bin can be reviewed or a
// part can be assigned to it.
func (h *Handler) HandleBinQR(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid bin id", http.StatusBadRequest)
		return
	}

	png, err := qrcode.PNG("wledger:bin:"+idStr, qrScaleFromQuery(r))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate QR", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
}

// GET /hardware/labels — renders a printable sheet of QR labels for every bin
// on all controllers, so physical labels can be printed and stuck on the bins.
func (h *Handler) HandleBinLabels(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	controllers, err := h.Hardware.ListControllers(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch hardware", http.StatusInternalServerError)
		return
	}

	var out []pages.BinLabelController
	for _, c := range controllers {
		containers, err := h.Hardware.GetContainers(r.Context(), c.ID)
		if err != nil {
			continue
		}
		lc := pages.BinLabelController{Name: c.Name}
		for _, ct := range containers {
			bins, err := h.Hardware.GetBinsByController(r.Context(), c.ID)
			if err != nil {
				continue
			}
			lcBins := make([]pages.BinLabel, 0, len(bins))
			for _, b := range bins {
				if b.ContainerID == ct.ID {
					lcBins = append(lcBins, pages.BinLabel{ID: b.ID, Name: b.Name})
				}
			}
			if len(lcBins) > 0 {
				lc.Containers = append(lc.Containers, pages.BinLabelContainer{
					Name: ct.Name, SegmentID: ct.SegmentID, Bins: lcBins,
				})
			}
		}
		out = append(out, lc)
	}

	pages.BinLabels(user, out).Render(r.Context(), w)
}
