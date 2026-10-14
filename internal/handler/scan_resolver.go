package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/categories"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
)

type scanResolution struct {
	Type      string `json:"type"`
	ID        int64  `json:"id"`
	Path      string `json:"path"`
	CabinetID int64  `json:"cabinet_id,omitempty"`
}

type scanResolutionError struct {
	status  int
	message string
}

func (e *scanResolutionError) Error() string { return e.message }

func (h *Handler) resolveScanCode(r *http.Request, raw string) (scanResolution, error) {
	code := strings.TrimSpace(raw)
	if code == "" {
		return scanResolution{}, &scanResolutionError{http.StatusBadRequest, "No code provided"}
	}

	resolveID := func(prefix, kind, label, path string, lookup func(int64) error) (scanResolution, error) {
		id, ok := scanIdentityID(code, prefix)
		if !ok {
			return scanResolution{}, &scanResolutionError{http.StatusBadRequest, "Invalid " + strings.ToLower(label) + " code"}
		}
		if err := lookup(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) || errors.Is(err, categories.ErrNotFound) || errors.Is(err, storage.ErrCabinetNotFound) || errors.Is(err, storage.ErrDrawerNotFound) {
				return scanResolution{}, &scanResolutionError{http.StatusNotFound, label + " not found"}
			}
			return scanResolution{}, &scanResolutionError{http.StatusInternalServerError, "Failed to resolve " + label}
		}
		return scanResolution{Type: kind, ID: id, Path: path}, nil
	}

	switch {
	case strings.HasPrefix(code, binScanPrefix):
		return resolveID(binScanPrefix, "bin", "Bin", "", func(id int64) error {
			_, err := h.Queries.GetBin(r.Context(), id)
			return err
		})
	case strings.HasPrefix(code, physicalCabinetScanPrefix):
		return resolveID(physicalCabinetScanPrefix, "physical-cabinet", "Physical cabinet", "", func(id int64) error {
			_, err := h.browseService().GetCabinet(r.Context(), id)
			return err
		})
	case strings.HasPrefix(code, categoryScanPrefix):
		return resolveID(categoryScanPrefix, "category", "Category", "", func(id int64) error {
			_, err := categories.NewService(h.Database).Get(r.Context(), id)
			return err
		})
	case strings.HasPrefix(code, physicalDrawerScanPrefix):
		id, ok := scanIdentityID(code, physicalDrawerScanPrefix)
		if !ok {
			return scanResolution{}, &scanResolutionError{http.StatusBadRequest, "Invalid physical drawer code"}
		}
		detail, err := h.browseService().GetDrawer(r.Context(), id)
		if err != nil {
			if errors.Is(err, storage.ErrDrawerNotFound) {
				return scanResolution{}, &scanResolutionError{http.StatusNotFound, "Physical drawer not found"}
			}
			return scanResolution{}, &scanResolutionError{http.StatusInternalServerError, "Failed to resolve physical drawer"}
		}
		return scanResolution{Type: "physical-drawer", ID: id, CabinetID: detail.Drawer.CabinetID}, nil
	case strings.HasPrefix(code, cabinetScanPrefix):
		return resolveID(cabinetScanPrefix, "legacy-controller", "Legacy controller", "", func(id int64) error {
			_, err := h.Queries.GetController(r.Context(), id)
			return err
		})
	case strings.HasPrefix(code, drawerScanPrefix):
		return resolveID(drawerScanPrefix, "legacy-container", "Legacy container", "", func(id int64) error {
			_, err := h.Queries.GetContainer(r.Context(), id)
			return err
		})
	case strings.HasPrefix(code, partIDScanPrefix):
		return resolveID(partIDScanPrefix, "part", "Part", "", func(id int64) error {
			_, err := h.Queries.GetPart(r.Context(), id)
			return err
		})
	case strings.HasPrefix(code, partScanPrefix):
		code = strings.TrimPrefix(code, partScanPrefix)
	}

	part, err := h.Queries.GetPartByBarcode(r.Context(), sql.NullString{String: code, Valid: code != ""})
	if errors.Is(err, sql.ErrNoRows) {
		return scanResolution{Type: "search", Path: "/parts?q=" + url.QueryEscape(code)}, nil
	}
	if err != nil {
		return scanResolution{}, &scanResolutionError{http.StatusInternalServerError, "Failed to resolve product barcode"}
	}
	return scanResolution{Type: "part", ID: part.ID}, nil
}

func (h *Handler) HandleScanResolve(w http.ResponseWriter, r *http.Request) {
	resolution, err := h.resolveScanCode(r, r.URL.Query().Get("q"))
	if err != nil {
		writeScanResolutionError(w, err)
		return
	}
	if resolution.Type == "search" {
		http.Error(w, "No product matches this barcode", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resolution); err != nil {
		h.Logger.Error("failed to encode scan resolution", "err", err)
	}
}

func writeScanResolutionError(w http.ResponseWriter, err error) {
	var scanErr *scanResolutionError
	if errors.As(err, &scanErr) {
		http.Error(w, scanErr.message, scanErr.status)
		return
	}
	http.Error(w, "Failed to resolve scanned code", http.StatusInternalServerError)
}

func (h *Handler) HandleEntityBarcode(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != chi.URLParam(r, "id") {
		http.Error(w, "Invalid barcode identity", http.StatusBadRequest)
		return
	}

	var code string
	switch chi.URLParam(r, "type") {
	case "bin":
		if _, err := h.Queries.GetBin(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Bin not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load bin", http.StatusInternalServerError)
			}
			return
		}
		code = BinScanCode(id)
	case "physical-cabinet":
		if _, err := h.browseService().GetCabinet(r.Context(), id); err != nil {
			if errors.Is(err, storage.ErrCabinetNotFound) {
				http.Error(w, "Physical cabinet not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load physical cabinet", http.StatusInternalServerError)
			}
			return
		}
		code = PhysicalCabinetScanCode(id)
	case "physical-drawer":
		if _, err := h.browseService().GetDrawer(r.Context(), id); err != nil {
			if errors.Is(err, storage.ErrDrawerNotFound) {
				http.Error(w, "Physical drawer not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load physical drawer", http.StatusInternalServerError)
			}
			return
		}
		code = PhysicalDrawerScanCode(id)
	case "category":
		if _, err := categories.NewService(h.Database).Get(r.Context(), id); err != nil {
			if errors.Is(err, categories.ErrNotFound) {
				http.Error(w, "Category not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load category", http.StatusInternalServerError)
			}
			return
		}
		code = CategoryScanCode(id)
	case "part":
		if _, err := h.Queries.GetPart(r.Context(), id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Part not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load part", http.StatusInternalServerError)
			}
			return
		}
		code = PartScanCode(db.Part{ID: id})
	default:
		http.Error(w, "Unknown barcode identity type", http.StatusBadRequest)
		return
	}

	writeCode128(w, code)
}
