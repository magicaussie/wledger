package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/qrcode"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/internal/wled"
	"github.com/tuxedocurly/wledger/web/pages"
)

// Physical storage browsing (M6) — read-only HTTP surface. Nothing here
// contacts WLED or a locate service, and nothing writes.

func (h *Handler) browseService() *storage.BrowseService {
	return storage.NewBrowseService(h.Queries)
}

// HandlePhysicalCabinetQR renders a QR code for an existing physical cabinet.
// Its dedicated code prefix cannot be confused with legacy controller labels.
func (h *Handler) HandlePhysicalCabinetQR(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid physical cabinet id", http.StatusBadRequest)
		return
	}
	if _, err := h.browseService().GetCabinet(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrCabinetNotFound) {
			http.Error(w, "Physical cabinet not found", http.StatusNotFound)
		} else {
			h.UIError.Respond(w, r, err, "Failed to load physical cabinet", http.StatusInternalServerError)
		}
		return
	}
	writeQR(w, r, PhysicalCabinetScanCode(id))
}

// HandlePhysicalDrawerQR renders a QR code for an existing physical drawer.
// Its dedicated code prefix cannot be confused with legacy container labels.
func (h *Handler) HandlePhysicalDrawerQR(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid physical drawer id", http.StatusBadRequest)
		return
	}
	if _, err := h.browseService().GetDrawer(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrDrawerNotFound) {
			http.Error(w, "Physical drawer not found", http.StatusNotFound)
		} else {
			h.UIError.Respond(w, r, err, "Failed to load physical drawer", http.StatusInternalServerError)
		}
		return
	}
	writeQR(w, r, PhysicalDrawerScanCode(id))
}

func writeQR(w http.ResponseWriter, r *http.Request, code string) {
	image, err := qrcode.PNG(code, qrScaleFromQuery(r))
	if err != nil {
		http.Error(w, "Failed to generate QR", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(image)
}

// HandleStorageCabinets renders GET /storage/cabinets: every active physical
// cabinet with its active drawers.
func (h *Handler) HandleStorageCabinets(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	cabinets, err := h.browseService().ListCabinets(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load cabinets", http.StatusInternalServerError)
		return
	}

	pages.StorageCabinets(cabinets, user).Render(r.Context(), w)
}

// HandleStorageCabinet renders GET /storage/cabinets/{id}: one cabinet and its
// active drawers. An absent cabinet id is a 404; a non-numeric id is a 400.
func (h *Handler) HandleStorageCabinet(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, nil, "Invalid cabinet id", http.StatusBadRequest)
		return
	}

	cabinet, err := h.browseService().GetCabinet(r.Context(), id)
	if errors.Is(err, storage.ErrCabinetNotFound) {
		h.UIError.Respond(w, r, err, "Cabinet not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load cabinet", http.StatusInternalServerError)
		return
	}

	pages.StorageCabinet(cabinet, user).Render(r.Context(), w)
}

// HandleStorageDrawer renders GET /storage/drawers/{id}: one drawer, the bins
// physically attached to it and those bins' contents. An absent drawer id is a
// 404; a non-numeric id is a 400. An attachment that references a missing bin
// surfaces as a server error rather than being hidden.
func (h *Handler) HandleStorageDrawer(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUserFromRequest(r)

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		h.UIError.Respond(w, r, nil, "Invalid drawer id", http.StatusBadRequest)
		return
	}

	detail, err := h.browseService().GetDrawer(r.Context(), id)
	if errors.Is(err, storage.ErrDrawerNotFound) {
		h.UIError.Respond(w, r, err, "Drawer not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load drawer", http.StatusInternalServerError)
		return
	}

	cabinet, err := h.Queries.GetCabinet(r.Context(), detail.Drawer.CabinetID)
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load cabinet", http.StatusInternalServerError)
		return
	}
	var selectedBinID int64
	if parsed, parseErr := strconv.ParseInt(r.URL.Query().Get("bin_id"), 10, 64); parseErr == nil && parsed > 0 {
		selectedBinID = parsed
	}
	var session *wled.PhysicalLocateSession
	if h.Locator != nil {
		current, ok, sessionErr := h.Locator.SessionStatusForDrawer(r.Context(), id)
		if sessionErr != nil {
			h.UIError.Respond(w, r, sessionErr, "Locate cleanup state is unavailable; LED state is unknown", http.StatusInternalServerError)
			return
		}
		if ok {
			session = &current
		}
	}
	pages.StorageDrawer(detail, cabinet.Name, user, selectedBinID, session, r.URL.Query().Get("stopped") == "1").Render(r.Context(), w)
}
