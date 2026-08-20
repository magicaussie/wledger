package api

import (
	"net/http"
	"strconv"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/pages"
)

// PartDTO is the JSON representation of a part for M2M consumers.
type PartDTO struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	PartNumber    string `json:"part_number"`
	Manufacturer  string `json:"manufacturer"`
	Supplier      string `json:"supplier"`
	Barcode       string `json:"barcode"`
	TotalStock    int64  `json:"total_stock"`
	ValidStock    int64  `json:"valid_stock"`
	OrphanedStock int64  `json:"orphaned_stock"`
	ImageURL      string `json:"image_url"`
}

type AssignmentDTO struct {
	ID             int64  `json:"id"`
	Quantity       int64  `json:"quantity"`
	BinID          *int64 `json:"bin_id"`
	BinName        string `json:"bin_name"`
	ControllerID   *int64 `json:"controller_id"`
	ControllerName string `json:"controller_name"`
	ControllerIP   string `json:"controller_ip"`
}

type ControllerDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	IPAddress string `json:"ip_address"`
	Online    bool   `json:"online"`
	LedCount  int64  `json:"led_count"`
}

func parseInt(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func toPartDTO(v pages.PartView) *PartDTO {
	return &PartDTO{
		ID:            v.ID,
		Name:          v.Name,
		Description:   v.Description.String,
		PartNumber:    v.PartNumber.String,
		Manufacturer:  "",
		Supplier:      "",
		Barcode:       v.BarcodeData.String,
		TotalStock:    v.TotalStock,
		ValidStock:    v.ValidStock,
		OrphanedStock: v.OrphanedStock,
		ImageURL:      v.ImagePath.String,
	}
}

func assignmentToDTO(a db.GetPartAssignmentsRow) AssignmentDTO {
	d := AssignmentDTO{
		ID:             a.ID,
		Quantity:       a.Quantity,
		BinName:        a.BinName.String,
		ControllerName: a.ControllerName.String,
		ControllerIP:   a.ControllerIp.String,
	}
	if a.BinID.Valid {
		d.BinID = &a.BinID.Int64
	}
	if a.ControllerID.Valid {
		d.ControllerID = &a.ControllerID.Int64
	}
	return d
}

// health is a liveness probe.
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// globalOff turns off all LEDs on all controllers.
func (h *Handler) globalOff(w http.ResponseWriter, r *http.Request) {
	if err := h.wled.GlobalOff(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to turn off LEDs: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// locatePart locates a part by id, flashing the LEDs of every bin it is stored in.
func (h *Handler) locatePart(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid part id")
		return
	}
	if err := h.wled.LocatePart(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "locate failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// locateBin locates a bin by id, flashing its LEDs.
func (h *Handler) locateBin(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid bin id")
		return
	}

	bin, err := h.queries.GetBin(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "bin not found")
		return
	}
	container, err := h.queries.GetContainer(r.Context(), bin.ContainerID)
	if err != nil {
		writeError(w, http.StatusNotFound, "bin container not found")
		return
	}
	if err := h.wled.LocateBin(r.Context(), container.ControllerID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "locate failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// searchParts returns parts matching a keyword (name / part number / barcode).
// Supports ?q= and optional ?bin= filter.
func (h *Handler) searchParts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var binID *int64
	if bi := r.URL.Query().Get("bin"); bi != "" {
		if v, err := parseInt(bi); err == nil {
			binID = &v
		}
	}

	views, err := h.parts.ListParts(r.Context(), q, 1, binID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "search failed")
		return
	}
	out := make([]PartDTO, 0, len(views))
	for _, v := range views {
		out = append(out, *toPartDTO(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"parts": out})
}

// getPart returns a part with its stock assignments.
func (h *Handler) getPart(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid part id")
		return
	}
	detail, err := h.parts.GetPartDetail(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "part not found")
		return
	}

	part := detail.Part
	dto := PartDTO{
		ID:           part.ID,
		Name:         part.Name,
		Description:  part.Description.String,
		PartNumber:   part.PartNumber.String,
		Manufacturer: part.Manufacturer.String,
		Supplier:     part.Supplier.String,
		Barcode:      part.BarcodeData.String,
		ImageURL:     part.ImagePath.String,
	}
	for _, a := range detail.Stock {
		dto.TotalStock += a.Quantity
		if a.BinID.Valid {
			dto.ValidStock += a.Quantity
		} else {
			dto.OrphanedStock += a.Quantity
		}
	}

	assignments := make([]AssignmentDTO, 0, len(detail.Stock))
	for _, a := range detail.Stock {
		assignments = append(assignments, assignmentToDTO(a))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"part":        dto,
		"assignments": assignments,
	})
}

// listHardware returns the configured LED controllers.
func (h *Handler) listHardware(w http.ResponseWriter, r *http.Request) {
	controllers, err := h.queries.GetControllers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list controllers")
		return
	}
	out := make([]ControllerDTO, 0, len(controllers))
	for _, c := range controllers {
		out = append(out, ControllerDTO{
			ID: c.ID, Name: c.Name, IPAddress: c.IpAddress,
			Online: c.IsOnline.Bool, LedCount: c.LedCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"controllers": out})
}
