package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/qrcode"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/internal/wled"
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
	cabinets, err := h.Queries.ListCabinets(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to fetch cabinet list", http.StatusInternalServerError)
		return
	}

	pages.Hardware(user, controllers, cabinets, r.URL.Query().Get("controller_saved") == "1").Render(r.Context(), w)
}

// POST /hardware
func (h *Handler) HandleHardwareCreate(w http.ResponseWriter, r *http.Request) {
	port, err := parseControllerPort(r.FormValue("port"))
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller settings are invalid: "+err.Error(), http.StatusBadRequest)
		return
	}

	_, err = h.Hardware.CreateController(r.Context(), db.CreateControllerParams{
		Name:      r.FormValue("name"),
		IpAddress: r.FormValue("ip_address"),
		Port:      sql.NullInt64{Int64: port, Valid: true},
	})
	if err != nil {
		status := controllerSettingsStatus(err)
		h.UIError.Respond(w, r, err, "Controller was not created: "+err.Error(), status)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// GET /hardware/{id}/edit
func (h *Handler) HandleHardwareEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid controller ID", http.StatusBadRequest)
		return
	}
	controller, err := h.Hardware.GetController(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
		return
	}
	port := controller.Port.Int64
	if !controller.Port.Valid || port < 1 {
		port = 80
	}
	pages.HardwareControllerEdit(auth.GetUserFromRequest(r), controller, "", controller.Name, controller.IpAddress, strconv.FormatInt(port, 10)).Render(r.Context(), w)
}

// POST /hardware/{id}/update
func (h *Handler) HandleHardwareUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid controller ID", http.StatusBadRequest)
		return
	}
	name, address, portText := r.FormValue("name"), r.FormValue("ip_address"), r.FormValue("port")
	port, err := parseControllerPort(portText)
	if err != nil {
		h.renderControllerEditError(w, r, id, name, address, portText, err.Error(), http.StatusBadRequest)
		return
	}
	err = h.Hardware.UpdateController(r.Context(), id, db.CreateControllerParams{
		Name: name, IpAddress: address, Port: sql.NullInt64{Int64: port, Valid: true},
	})
	if err != nil {
		status := controllerSettingsStatus(err)
		if status == http.StatusInternalServerError {
			h.UIError.Respond(w, r, err, "Controller settings were not saved", status)
			return
		}
		h.renderControllerEditError(w, r, id, name, address, portText, err.Error(), status)
		return
	}
	http.Redirect(w, r, "/hardware?controller_saved=1", http.StatusSeeOther)
}

func (h *Handler) renderControllerEditError(w http.ResponseWriter, r *http.Request, id int64, name, address, port, message string, status int) {
	controller, err := h.Hardware.GetController(r.Context(), id)
	if err != nil {
		h.UIError.Respond(w, r, err, "Controller not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(status)
	pages.HardwareControllerEdit(auth.GetUserFromRequest(r), controller, message, name, address, port).Render(r.Context(), w)
}

func parseControllerPort(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("port is required and must be an integer from 1 to 65535")
	}
	port, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be an integer from 1 to 65535")
	}
	return port, nil
}

func controllerSettingsStatus(err error) int {
	if errors.Is(err, db.ErrPhysicalLocateActive) {
		return http.StatusConflict
	}
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusNotFound
	}
	if errors.Is(err, hardware.ErrInvalidControllerSettings) || errors.Is(err, hardware.ErrDuplicateController) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// POST /hardware/{id}/delete
func (h *Handler) HandleHardwareCapacitySave(w http.ResponseWriter, r *http.Request) {
	controllerID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	capacity, capacityErr := strconv.ParseInt(r.FormValue("led_count"), 10, 64)
	expectedCapacity, expectedErr := strconv.ParseInt(r.FormValue("expected_led_count"), 10, 64)
	electricalRevision, revisionErr := strconv.ParseInt(r.FormValue("electrical_revision"), 10, 64)
	if err != nil || capacityErr != nil || expectedErr != nil || revisionErr != nil {
		http.Error(w, "invalid controller capacity request", http.StatusBadRequest)
		return
	}
	confirmMappingImpact := r.FormValue("confirm_mapping_impact") == "confirm"
	save := func() error {
		return h.layoutService().SaveControllerLEDCapacityConfirmed(r.Context(), controllerID, expectedCapacity, capacity, electricalRevision, confirmMappingImpact)
	}
	var saveErr error
	if h.Locator != nil {
		saveErr = h.Locator.WithoutActiveSession(r.Context(), save)
	} else {
		saveErr = save()
	}
	if saveErr != nil {
		status := http.StatusBadRequest
		if errors.Is(saveErr, storage.ErrLEDControllerCapacityStale) || errors.Is(saveErr, storage.ErrLEDAllocationStale) || errors.Is(saveErr, wled.ErrLocateSessionActive) {
			status = http.StatusConflict
		}
		cabinet, cabinetErr := h.Queries.GetCabinetByAssignedController(r.Context(), controllerID)
		if cabinetErr == nil {
			target := fmt.Sprintf("/storage/cabinets/%d/edit?capacity_error=%s", cabinet.ID, url.QueryEscape(saveErr.Error()))
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		h.UIError.Respond(w, r, saveErr, "Controller LED capacity was not saved", status)
		return
	}
	cabinet, err := h.Queries.GetCabinetByAssignedController(r.Context(), controllerID)
	if err == nil {
		http.Redirect(w, r, fmt.Sprintf("/storage/cabinets/%d/edit?capacity_saved=1", cabinet.ID), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

func (h *Handler) HandleHardwareDelete(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, _ := strconv.Atoi(idStr)

	err := h.Hardware.DeleteController(r.Context(), int64(id))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, db.ErrPhysicalLocateActive) {
			status = http.StatusConflict
		}
		h.UIError.Respond(w, r, err, "Failed to delete hardware", status)
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

	// Expose the active coordinate space to the painter. The painter's editing
	// behaviour is unchanged for now; an unknown persisted state is surfaced
	// rather than silently defaulted.
	space, err := ledspace.Current(r.Context(), h.Queries)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to read LED coordinate space", http.StatusInternalServerError)
		return
	}

	// Pass User to Template
	pages.HardwareGrid(user, c, containers, bins, space).Render(r.Context(), w)
}

// POST /hardware/{id}/grid
func (h *Handler) HandleHardwareGridSave(w http.ResponseWriter, r *http.Request) {
	controllerID, _ := strconv.Atoi(chi.URLParam(r, "id"))
	ctx := r.Context()

	// Parse Payload
	gridDataJSON := r.FormValue("grid_data")
	configJSON := r.FormValue("config_data")

	_, err := h.Hardware.SaveGridInSpace(ctx, int64(controllerID), gridDataJSON, configJSON, r.FormValue("bin_index_space"))
	if err != nil {
		// Invalid drawer allocations or bin mappings are client errors.
		if errors.Is(err, hardware.ErrInvalidAllocation) {
			h.UIError.Respond(w, r, err, "Invalid grid layout: check drawer LED allocations and bin mappings, then reload the grid", http.StatusBadRequest)
			return
		}
		if errors.Is(err, db.ErrPhysicalLocateActive) {
			h.UIError.Respond(w, r, err, "Grid changes are blocked while a physical Locate may own pixels", http.StatusConflict)
			return
		}
		// Anything else is a genuine internal failure.
		h.UIError.Respond(w, r, err, "Failed to save grid layout", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
}

// POST /hardware/off
func (h *Handler) HandleGlobalOff(w http.ResponseWriter, r *http.Request) {
	err := h.WLED.GlobalOff(r.Context())
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, wled.ErrHardwareBusy) || errors.Is(err, wled.ErrPhysicalOutputDisabled) {
			status = http.StatusConflict
		}
		h.UIError.Respond(w, r, err, "Failed to trigger global off", status)
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
		if errors.Is(err, wled.ErrCoordinateSpaceUnresolved) || errors.Is(err, wled.ErrHardwareBusy) || errors.Is(err, wled.ErrPhysicalOutputDisabled) {
			h.UIError.Respond(w, r, err, "Locate unavailable: "+err.Error(), http.StatusConflict)
			return
		}
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
	// ParseMultipartForm's maxMemory is only a memory threshold; cap the complete
	// request body so a large file cannot be spooled without bound.
	if !limitRequestBody(w, r, config.MaxUploadSizeImport) {
		return
	}
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
	port := int64(0)
	if strings.TrimSpace(r.FormValue("port")) != "" {
		port, err = parseControllerPort(r.FormValue("port"))
		if err != nil {
			h.UIError.Respond(w, r, err, "Controller import settings are invalid: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	_, err = h.Hardware.ImportConfig(r.Context(), name, ip, port, data)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, db.ErrPhysicalLocateActive) {
			status = http.StatusConflict
		}
		h.UIError.Respond(w, r, err, "Hardware config was not imported: "+err.Error(), status)
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
	if _, err := h.Queries.GetBin(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Bin not found", http.StatusNotFound)
		} else {
			h.UIError.Respond(w, r, err, "Failed to load bin", http.StatusInternalServerError)
		}
		return
	}

	png, err := qrcode.PNG(BinScanCode(id), qrScaleFromQuery(r))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate QR", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
}

// GET /cabinet/{id}/qr — returns a QR PNG for a cabinet's scan code. A cabinet
// is a controller.
func (h *Handler) HandleCabinetQR(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid cabinet id", http.StatusBadRequest)
		return
	}
	if _, err := h.Queries.GetController(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Controller not found", http.StatusNotFound)
		} else {
			h.UIError.Respond(w, r, err, "Failed to load controller", http.StatusInternalServerError)
		}
		return
	}

	png, err := qrcode.PNG(CabinetScanCode(id), qrScaleFromQuery(r))
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to generate QR", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(png)
}

// GET /drawer/{id}/qr — returns a QR PNG for a drawer's scan code. A drawer is a
// container.
func (h *Handler) HandleDrawerQR(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid drawer id", http.StatusBadRequest)
		return
	}
	if _, err := h.Queries.GetContainer(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Container not found", http.StatusNotFound)
		} else {
			h.UIError.Respond(w, r, err, "Failed to load container", http.StatusInternalServerError)
		}
		return
	}

	png, err := qrcode.PNG(DrawerScanCode(id), qrScaleFromQuery(r))
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
		lc := pages.BinLabelController{ID: c.ID, Name: c.Name}
		for _, ct := range containers {
			bins, err := h.Hardware.GetBinsByController(r.Context(), c.ID)
			if err != nil {
				continue
			}
			lcBins := make([]pages.BinLabel, 0, len(bins))
			for _, b := range bins {
				if b.ContainerID.Valid && b.ContainerID.Int64 == ct.ID {
					lcBins = append(lcBins, pages.BinLabel{ID: b.ID, Name: b.Name})
				}
			}
			if len(lcBins) > 0 {
				lc.Containers = append(lc.Containers, pages.BinLabelContainer{
					ID: ct.ID, Name: ct.Name, SegmentID: ct.SegmentID, Bins: lcBins,
				})
			}
		}
		out = append(out, lc)
	}

	pages.BinLabels(user, out).Render(r.Context(), w)
}
