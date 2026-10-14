package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/categories"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/web/pages"
)

func (h *Handler) HandleScanLabel(w http.ResponseWriter, r *http.Request) {
	idText := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != idText {
		http.Error(w, "Invalid label identity", http.StatusBadRequest)
		return
	}

	label := pages.ScanLabelView{ID: id}
	switch chi.URLParam(r, "type") {
	case "physical-cabinet":
		cabinet, err := h.browseService().GetCabinet(r.Context(), id)
		if err != nil {
			if errors.Is(err, storage.ErrCabinetNotFound) {
				http.Error(w, "Physical cabinet not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load physical cabinet", http.StatusInternalServerError)
			}
			return
		}
		label.Kind = "Physical cabinet"
		label.Name = cabinet.Name
		label.Identity = fmt.Sprintf("Physical cabinet #%d", id)
		label.QRURL = fmt.Sprintf("/storage/cabinets/%d/qr", id)
	case "physical-drawer":
		drawer, err := h.browseService().GetDrawer(r.Context(), id)
		if err != nil {
			if errors.Is(err, storage.ErrDrawerNotFound) {
				http.Error(w, "Physical drawer not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load physical drawer", http.StatusInternalServerError)
			}
			return
		}
		label.Kind = "Physical drawer"
		label.Name = drawer.Drawer.Label
		if label.Name == "" {
			label.Name = fmt.Sprintf("Drawer #%d", id)
		}
		label.Identity = fmt.Sprintf("Physical drawer #%d · cabinet #%d", id, drawer.Drawer.CabinetID)
		label.QRURL = fmt.Sprintf("/storage/drawers/%d/qr", id)
	case "bin":
		bin, err := h.Queries.GetBin(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Bin not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load bin", http.StatusInternalServerError)
			}
			return
		}
		label.Kind = "Inventory bin"
		label.Name = bin.Name
		label.Identity = fmt.Sprintf("Inventory bin #%d", id)
		label.QRURL = fmt.Sprintf("/storage/bins/%d/qr", id)
	case "category":
		category, err := categories.NewService(h.Database).Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, categories.ErrNotFound) {
				http.NotFound(w, r)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load category", http.StatusInternalServerError)
			}
			return
		}
		label.Kind = "Category"
		label.Name = category.Name
		label.Identity = fmt.Sprintf("WLEDger category #%d", id)
		label.QRURL = fmt.Sprintf("/categories/%d/qr", id)
	case "part":
		part, err := h.Queries.GetPart(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.Error(w, "Part not found", http.StatusNotFound)
			} else {
				h.UIError.Respond(w, r, err, "Failed to load part", http.StatusInternalServerError)
			}
			return
		}
		label.Kind = "Product"
		label.Name = part.Name
		label.Identity = fmt.Sprintf("WLEDger product #%d", id)
		if part.BarcodeData.Valid && part.BarcodeData.String != "" {
			label.Identity += " · manufacturer barcode " + part.BarcodeData.String
		}
		label.QRURL = fmt.Sprintf("/parts/%d/qr", id)
	default:
		http.Error(w, "Unknown label identity type", http.StatusBadRequest)
		return
	}
	label.Code128URL = fmt.Sprintf("/scan/barcode/%s/%d", chi.URLParam(r, "type"), id)
	pages.ScanLabel(auth.GetUserFromRequest(r), label).Render(r.Context(), w)
}
