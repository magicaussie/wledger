package handler

import (
	"context"
	"net/http"

	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/web/components"
)

// HandleStorageBinPicker serves GET /storage/bin_picker: the M6 bin-selection
// fragment used by the Add Stock and Move Stock forms.
//
// Selection is independent of the electrical model. Bins are grouped by
// PHYSICAL placement, and every remaining bin is offered under a clearly
// labelled "No physical placement" group so a legacy row (including a bin whose
// container_id is NULL) stays selectable with no location inferred for it.
//
// The fragment is read-only and renders the existing selection contract
// unchanged: one hidden input named bin_id, the bin-selected /
// bin-selected-form event pair, and stable bins.id values.
func (h *Handler) HandleStorageBinPicker(w http.ResponseWriter, r *http.Request) {
	view, err := h.storageBinPickerView(r.Context())
	if err != nil {
		h.UIError.Respond(w, r, err, "Failed to load bins", http.StatusInternalServerError)
		return
	}

	if err := components.StorageBinPicker(view).Render(r.Context(), w); err != nil {
		h.Logger.Error("failed to render storage bin picker", "err", err)
	}
}

// storageBinPickerView builds the picker read model from physical placement
// truth: storage.BrowseService for the active cabinet/drawer hierarchy, plus
// every bin_drawer_attachments row to decide which bins have no recorded
// placement. A bin's legacy container_id is never consulted.
func (h *Handler) storageBinPickerView(ctx context.Context) (components.StorageBinPickerView, error) {
	browse := storage.NewBrowseService(h.Queries)
	view := components.StorageBinPickerView{
		Cabinets: make([]components.StorageBinPickerCabinet, 0),
		Inactive: make([]components.StorageBinPickerBin, 0),
		Unplaced: make([]components.StorageBinPickerBin, 0),
	}

	snapshot, err := browse.BinPicker(ctx)
	if err != nil {
		return view, err
	}
	for _, cabinet := range snapshot.Cabinets {
		group := components.StorageBinPickerCabinet{
			ID:      cabinet.ID,
			Name:    cabinet.Name,
			Drawers: make([]components.StorageBinPickerDrawer, 0, len(cabinet.Drawers)),
		}
		for _, detail := range cabinet.Drawers {
			drawerGroup := components.StorageBinPickerDrawer{
				ID:    detail.Drawer.ID,
				Label: detail.Drawer.Label,
				Bins:  make([]components.StorageBinPickerBin, 0, len(detail.Bins)),
			}
			for _, bin := range detail.Bins {
				drawerGroup.Bins = append(drawerGroup.Bins, components.StorageBinPickerBin{
					ID:   bin.BinID,
					Name: bin.Name,
				})
			}
			group.Drawers = append(group.Drawers, drawerGroup)
		}
		view.Cabinets = append(view.Cabinets, group)
	}

	for _, bin := range snapshot.Inactive {
		view.Inactive = append(view.Inactive, components.StorageBinPickerBin{
			ID:   bin.BinID,
			Name: bin.Name,
		})
	}
	for _, bin := range snapshot.Unplaced {
		view.Unplaced = append(view.Unplaced, components.StorageBinPickerBin{
			ID:   bin.BinID,
			Name: bin.Name,
		})
	}

	return view, nil
}
