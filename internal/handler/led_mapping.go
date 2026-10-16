package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/internal/wled"
)

type ledProposal struct {
	DrawerID int64 `json:"drawer_id"`
	Start    int64 `json:"start"`
	Count    int64 `json:"count"`
}
type ledPreviewRequest struct {
	CabinetID      int64   `json:"cabinet_id"`
	DrawerIDs      []int64 `json:"drawer_ids"`
	Start          int64   `json:"start"`
	CountPerDrawer int64   `json:"count_per_drawer"`
	ControllerID   int64   `json:"controller_id"`
	OutputOrder    *int64  `json:"output_order"`
	FirstPixel     *int64  `json:"first_pixel"`
	LastPixel      *int64  `json:"last_pixel"`
}
type ledMappingResult struct {
	Proposals []ledProposal `json:"proposals"`
	Conflicts []string      `json:"conflicts"`
}

var errLEDMappingOverlap = errors.New("mapping overlap")

func ledMappingInvalid(message string) error {
	return fmt.Errorf("%w: %s", storage.ErrLEDAllocationInvalid, message)
}

// ledPhysicalRanges deliberately resolves the configured topology, not hardware.
func ledPhysicalRanges(ctx context.Context, q db.Querier, cabinetID, start, count int64) ([]cabinetpixels.PhysicalPiece, error) {
	t, err := cabinetpixels.Load(ctx, q, cabinetID)
	if err != nil {
		return nil, err
	}
	return cabinetpixels.SplitTopologyRange(t, start, count)
}

// ledValidateAppend runs against the same transaction as the eventual inserts.
// Existing allocations are never replaced, reordered, or repaired here.
func ledValidateAppend(ctx context.Context, q db.Querier, cabinetID int64, proposals []ledProposal) error {
	if cabinetID <= 0 {
		return ledMappingInvalid("cabinet ID must be positive")
	}
	cab, err := q.GetCabinet(ctx, cabinetID)
	if err != nil {
		return err
	}
	if cab.Archived != 0 {
		return ledMappingInvalid("cabinet is archived")
	}
	if len(proposals) == 0 || len(proposals) > 4096 {
		return ledMappingInvalid("provide between 1 and 4096 ranges")
	}
	existing, err := q.ListAllDrawerLEDAllocations(ctx)
	if err != nil {
		return err
	}
	type assigned struct {
		drawer int64
		pieces []cabinetpixels.PhysicalPiece
	}
	all := make([]assigned, 0, len(existing)+len(proposals))
	for _, a := range existing {
		pieces, e := ledPhysicalRanges(ctx, q, a.CabinetID, a.StartPixel, a.PixelCount)
		if e != nil {
			return fmt.Errorf("%w: existing allocation %d cannot be validated: %v", storage.ErrLEDControllerConfiguration, a.ID, e)
		}
		all = append(all, assigned{a.DrawerID, pieces})
	}
	for _, p := range proposals {
		d, e := q.GetDrawer(ctx, p.DrawerID)
		if e != nil {
			return e
		}
		if d.CabinetID != cabinetID || d.Archived != 0 {
			return ledMappingInvalid("drawer is archived or belongs to another cabinet")
		}
		pieces, e := ledPhysicalRanges(ctx, q, cabinetID, p.Start, p.Count)
		if e != nil {
			return ledMappingInvalid(fmt.Sprintf("drawer %d range: %v", p.DrawerID, e))
		}
		for i, x := range pieces {
			for _, y := range pieces[:i] {
				alias := x.ControllerID == y.ControllerID
				if !alias {
					a, e := q.GetController(ctx, x.ControllerID)
					if e != nil {
						return e
					}
					b, e := q.GetController(ctx, y.ControllerID)
					if e != nil {
						return e
					}
					alias = wled.ConfiguredControllersAlias(a, b)
				}
				if alias && x.LocalStart <= y.LocalStart+y.Count-1 && y.LocalStart <= x.LocalStart+x.Count-1 {
					return fmt.Errorf("%w: %w: proposed drawer %d aliases its own physical pixels", storage.ErrLEDAllocationInvalid, errLEDMappingOverlap, p.DrawerID)
				}
			}
		}
		for _, a := range all {
			for _, x := range a.pieces {
				for _, y := range pieces {
					alias := x.ControllerID == y.ControllerID
					if !alias {
						a, e := q.GetController(ctx, x.ControllerID)
						if e != nil {
							return e
						}
						b, e := q.GetController(ctx, y.ControllerID)
						if e != nil {
							return e
						}
						alias = wled.ConfiguredControllersAlias(a, b)
					}
					if alias && x.LocalStart <= y.LocalStart+y.Count-1 && y.LocalStart <= x.LocalStart+x.Count-1 {
						return fmt.Errorf("%w: %w: drawer %d overlaps drawer %d on controller %d", storage.ErrLEDAllocationInvalid, errLEDMappingOverlap, p.DrawerID, a.drawer, x.ControllerID)
					}
				}
			}
		}
		all = append(all, assigned{p.DrawerID, pieces})
	}
	return nil
}

func ledBuildPreview(ctx context.Context, q db.Querier, in ledPreviewRequest) (ledMappingResult, error) {
	out := ledMappingResult{Proposals: []ledProposal{}, Conflicts: []string{}}
	if in.CabinetID <= 0 || len(in.DrawerIDs) == 0 || len(in.DrawerIDs) > 4096 {
		return out, ledMappingInvalid("cabinet and drawer IDs are required")
	}
	seenDrawers := map[int64]bool{}
	for _, id := range in.DrawerIDs {
		if id <= 0 || seenDrawers[id] {
			return out, ledMappingInvalid("drawer IDs must be positive and unique")
		}
		seenDrawers[id] = true
	}
	if in.ControllerID != 0 || in.FirstPixel != nil || in.LastPixel != nil || in.OutputOrder != nil {
		if in.ControllerID <= 0 || in.FirstPixel == nil || in.LastPixel == nil || len(in.DrawerIDs) != 1 || in.Start != 0 || in.CountPerDrawer != 0 {
			return out, ledMappingInvalid("controller preview requires one drawer and an unambiguous first/last pixel pair")
		}
		if *in.FirstPixel < 0 || *in.LastPixel < *in.FirstPixel || *in.LastPixel == math.MaxInt64 {
			return out, ledMappingInvalid("invalid zero-based controller range")
		}
		t, err := cabinetpixels.Load(ctx, q, in.CabinetID)
		if err != nil {
			return out, ledMappingInvalid(fmt.Sprintf("cannot resolve cabinet topology: %v", err))
		}
		offset := int64(0)
		limit := int64(-1)
		for _, c := range t.Controllers {
			if c.Record.ID == in.ControllerID {
				offset = c.Start
				limit = c.Pixels
				if in.OutputOrder != nil && *in.OutputOrder < 0 {
					return out, ledMappingInvalid("output order must not be negative")
				}
				// Stored output/string DisplayOrder is not a durable identity
				// for WLED's real hw.led.ins array. Without that association,
				// output_order is advisory; both endpoints remain absolute.
				break
			}
		}
		if limit < 0 || *in.LastPixel >= limit {
			return out, ledMappingInvalid("controller/output range is not configured in this cabinet")
		}
		// Convert physical indexes by resolving individual logical pixels. A reverse
		// or custom output can produce several disjoint cabinet ranges.
		selected := []int64{}
		selectedPhysical := map[int64]bool{}

		for n := int64(0); n < limit; n++ {
			pieces, e := cabinetpixels.SplitTopologyRange(t, offset+n, 1)
			if e != nil {
				return out, e
			}
			if len(pieces) != 1 || pieces[0].ControllerID != in.ControllerID || pieces[0].Count != 1 {
				return out, ledMappingInvalid("ambiguous topology")
			}
			pixel := pieces[0].LocalStart - 1
			if pixel >= *in.FirstPixel && pixel <= *in.LastPixel {
				if selectedPhysical[pixel] {
					return out, ledMappingInvalid("physical range has duplicate logical assignments")
				}
				selectedPhysical[pixel] = true
				selected = append(selected, offset+n)
			}
		}
		if int64(len(selected)) != *in.LastPixel-*in.FirstPixel+1 {
			return out, ledMappingInvalid("physical range cannot be uniquely inverted through cabinet topology")
		}
		for _, n := range selected {
			l := len(out.Proposals)
			if l > 0 && out.Proposals[l-1].Start+out.Proposals[l-1].Count == n {
				out.Proposals[l-1].Count++
			} else {
				out.Proposals = append(out.Proposals, ledProposal{in.DrawerIDs[0], n, 1})
			}
		}
	} else {
		if in.Start < 1 || in.CountPerDrawer < 1 {
			return out, ledMappingInvalid("start and count must be positive")
		}
		next := in.Start
		seen := map[int64]bool{}
		for i, id := range in.DrawerIDs {
			if id <= 0 || seen[id] || in.CountPerDrawer > math.MaxInt64-next+1 {
				return out, ledMappingInvalid("invalid, duplicate, or overflowing drawer range")
			}
			seen[id] = true
			out.Proposals = append(out.Proposals, ledProposal{id, next, in.CountPerDrawer})
			if i < len(in.DrawerIDs)-1 {
				if in.CountPerDrawer > math.MaxInt64-next {
					return out, ledMappingInvalid("range overflow")
				}
				next += in.CountPerDrawer
			}
		}
	}
	if err := ledValidateAppend(ctx, q, in.CabinetID, out.Proposals); err != nil {
		if !errors.Is(err, errLEDMappingOverlap) {
			return out, err
		}
		out.Conflicts = append(out.Conflicts, err.Error())
	}
	return out, nil
}

func (h *Handler) HandleLEDMappingPreview(w http.ResponseWriter, r *http.Request) {
	var in ledPreviewRequest
	if !ledRequest(w, r, &in) {
		return
	}
	var result ledMappingResult
	err := db.ExecLEDRead(r.Context(), h.Queries, func(q db.LEDQuerier) error { var e error; result, e = ledBuildPreview(r.Context(), q, in); return e })
	if err != nil {
		ledAPIError(w, err)
		return
	}
	ledJSON(w, http.StatusOK, result)
}
func (h *Handler) HandleLEDMappingSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CabinetID int64         `json:"cabinet_id"`
		Proposals []ledProposal `json:"proposals"`
		Confirm   bool          `json:"confirm"`
	}
	if !ledRequest(w, r, &in) {
		return
	}
	if !in.Confirm {
		ledAPIError(w, ledMappingInvalid("explicit confirmation is required"))
		return
	}
	err := db.WithLEDOperationClaim(r.Context(), h.Queries, func(q db.LEDQuerier) error {
		owned, e := db.HasPhysicalLocateOwnership(r.Context(), q)
		if e != nil {
			return e
		}
		if owned {
			return db.ErrPhysicalLocateActive
		}
		if e = ledValidateAppend(r.Context(), q, in.CabinetID, in.Proposals); e != nil {
			return e
		}
		cab, e := q.GetCabinet(r.Context(), in.CabinetID)
		if e != nil {
			return e
		}
		rows, e := q.ListDrawerLEDAllocationsByCabinet(r.Context(), in.CabinetID)
		if e != nil {
			return e
		}
		orders := map[int64]int64{}
		for _, a := range rows {
			if a.DisplayOrder == math.MaxInt64 {
				return ledMappingInvalid("existing allocation order cannot be appended without overflow")
			}
			orders[a.DrawerID] = max(orders[a.DrawerID], a.DisplayOrder+1)
		}
		for _, p := range in.Proposals {
			if orders[p.DrawerID] < 0 {
				return ledMappingInvalid("allocation order overflow")
			}
			_, e = q.CreateDrawerLEDAllocation(r.Context(), db.CreateDrawerLEDAllocationParams{CabinetID: in.CabinetID, DrawerID: p.DrawerID, StartPixel: p.Start, PixelCount: p.Count, DisplayOrder: orders[p.DrawerID]})
			if e != nil {
				return e
			}
			orders[p.DrawerID]++
		}
		n, e := q.AdvanceCabinetElectricalRevision(r.Context(), db.AdvanceCabinetElectricalRevisionParams{ID: in.CabinetID, ElectricalRevision: cab.ElectricalRevision})
		if e != nil {
			return e
		}
		if n != 1 {
			return storage.ErrLEDAllocationStale
		}
		return nil
	})
	if err != nil {
		ledAPIError(w, err)
		return
	}
	ledJSON(w, http.StatusOK, ledMappingResult{in.Proposals, []string{}})
}

func (h *Handler) HandleLEDBinPixel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BinID        int64  `json:"bin_id"`
		DrawerID     int64  `json:"drawer_id"`
		ControllerID int64  `json:"controller_id"`
		Pixel        *int64 `json:"pixel"`
		Confirm      bool   `json:"confirm"`
		Clear        bool   `json:"clear"`
	}
	if !ledRequest(w, r, &in) {
		return
	}
	if !in.Confirm || in.BinID <= 0 || in.DrawerID <= 0 {
		ledAPIError(w, ledMappingInvalid("bin, drawer and explicit confirmation are required"))
		return
	}
	if in.Clear {
		if err := wled.ClearBinPixel(r.Context(), h.Queries, in.BinID, in.DrawerID); err != nil {
			ledAPIError(w, err)
			return
		}
		ledJSON(w, http.StatusOK, map[string]any{"bin_id": in.BinID, "drawer_id": in.DrawerID, "cleared": true, "assigned": false})
		return
	}
	if in.ControllerID <= 0 || in.Pixel == nil || *in.Pixel < 0 {
		ledAPIError(w, ledMappingInvalid("controller and zero-based pixel are required when assigning an override"))
		return
	}
	p, relative, err := wled.AssignBinPixel(r.Context(), h.Queries, in.BinID, in.DrawerID, in.ControllerID, *in.Pixel)
	if err != nil {
		ledAPIError(w, err)
		return
	}
	ledJSON(w, http.StatusOK, map[string]any{"bin_id": p.BinID, "drawer_id": p.DrawerID, "controller_id": p.ControllerID, "pixel": p.Pixel, "relative_pixel": relative, "allocation_id": p.AllocationID, "pixel_offset": p.Offset})
}

type ledValidation struct {
	Severity     string `json:"severity"`
	ControllerID int64  `json:"controller_id"`
	CabinetID    int64  `json:"cabinet_id"`
	DrawerID     int64  `json:"drawer_id"`
	BinID        int64  `json:"bin_id"`
	Message      string `json:"message"`
	Repair       string `json:"repair"`
}
type ledDataPhysical struct {
	ControllerID  int64 `json:"controller_id"`
	FirstPixel    int64 `json:"first_pixel"`
	Count         int64 `json:"count"`
	RelativeStart int64 `json:"relative_start"`
}
type ledDataRange struct {
	Start    int64             `json:"start"`
	Count    int64             `json:"count"`
	Physical []ledDataPhysical `json:"physical"`
}
type ledDataBin struct {
	ID            int64  `json:"id"`
	Label         string `json:"label"`
	Assigned      bool   `json:"assigned"`
	RelativePixel *int64 `json:"relative_pixel,omitempty"`
	ControllerID  *int64 `json:"controller_id,omitempty"`
	Pixel         *int64 `json:"pixel,omitempty"`
	AllocationID  *int64 `json:"allocation_id,omitempty"`
	Stale         bool   `json:"stale"`
}
type ledDataDrawer struct {
	ID     int64          `json:"id"`
	Label  string         `json:"label"`
	Ranges []ledDataRange `json:"ranges"`
	Bins   []ledDataBin   `json:"bins"`
}
type ledDataCabinet struct {
	ID      int64           `json:"id"`
	Name    string          `json:"name"`
	Drawers []ledDataDrawer `json:"drawers"`
}
type ledDataController struct {
	ID                   int64  `json:"id"`
	Name                 string `json:"name"`
	LEDCount             int64  `json:"led_count"`
	Verified             bool   `json:"verified"`
	AllocatedPixelCount  *int64 `json:"allocated_pixel_count,omitempty"`
	UnassignedPixelCount *int64 `json:"unassigned_pixel_count,omitempty"`
}

// Coverage is the union of configured controller address intervals assigned to
// drawers, not a measurement of hardware or a count of explicit bin overrides.
// Unknown/invalid topology leaves counts absent rather than implying zero use.
func ledValidateCoverage(ctx context.Context, q db.Querier, controllers []ledDataController, validation *[]ledValidation) (int, error) {
	members, err := q.ListAllCabinetControllers(ctx)
	if err != nil {
		return 0, err
	}
	cabinets, err := q.ListCabinetsIncludingArchived(ctx)
	if err != nil {
		return 0, err
	}
	drawers, err := q.ListAllDrawers(ctx)
	if err != nil {
		return 0, err
	}
	allocations, err := q.ListAllDrawerLEDAllocations(ctx)
	if err != nil {
		return 0, err
	}
	controllerByID := map[int64]ledDataController{}
	for _, c := range controllers {
		controllerByID[c.ID] = c
	}
	cabinetByID := map[int64]bool{}
	for _, c := range cabinets {
		cabinetByID[c.ID] = true
	}
	drawerByID := map[int64]db.Drawer{}
	for _, d := range drawers {
		drawerByID[d.ID] = d
		if !cabinetByID[d.CabinetID] {
			*validation = append(*validation, ledValidation{Severity: "error", CabinetID: d.CabinetID, DrawerID: d.ID, Message: "Drawer references a missing cabinet", Repair: "Review the invalid drawer relationship; no automatic repair applied."})
		}
	}
	attachments, err := q.ListBinDrawerAttachments(ctx)
	if err != nil {
		return 0, err
	}
	for _, a := range attachments {
		if _, ok := drawerByID[a.DrawerID]; !ok {
			*validation = append(*validation, ledValidation{Severity: "error", DrawerID: a.DrawerID, BinID: a.BinID, Message: "Bin attachment references a missing drawer", Repair: "Review the invalid attachment; no automatic repair applied."})
		}
	}
	assigned := map[int64]bool{}
	unknown := map[int64]bool{}
	for _, m := range members {
		_, knownController := controllerByID[m.ControllerID]
		if !knownController || !cabinetByID[m.CabinetID] {
			*validation = append(*validation, ledValidation{Severity: "error", ControllerID: m.ControllerID, CabinetID: m.CabinetID, Message: "Cabinet membership references a missing controller or cabinet", Repair: "Review the invalid cabinet controller relationship; no automatic repair applied."})
			unknown[m.ControllerID] = true
			continue
		}
		assigned[m.ControllerID] = true
	}
	type interval struct{ start, end int64 }
	coverage := map[int64][]interval{}
	topologies := map[int64]cabinetpixels.Topology{}
	for _, a := range allocations {
		d, knownDrawer := drawerByID[a.DrawerID]
		if !knownDrawer || !cabinetByID[a.CabinetID] || d.CabinetID != a.CabinetID {
			*validation = append(*validation, ledValidation{Severity: "error", CabinetID: a.CabinetID, DrawerID: a.DrawerID, Message: fmt.Sprintf("Allocation %d has invalid cabinet/drawer ownership", a.ID), Repair: "Review the allocation references; no automatic repair applied."})
			for _, m := range members {
				if m.CabinetID == a.CabinetID || (knownDrawer && m.CabinetID == d.CabinetID) {
					unknown[m.ControllerID] = true
				}
			}
			continue
		}
		topology, ok := topologies[a.CabinetID]
		var topologyErr error
		if !ok {
			topology, topologyErr = cabinetpixels.Load(ctx, q, a.CabinetID)
			if topologyErr == nil {
				topologies[a.CabinetID] = topology
			}
		}
		var pieces []cabinetpixels.PhysicalPiece
		if topologyErr == nil {
			pieces, topologyErr = cabinetpixels.SplitTopologyRange(topology, a.StartPixel, a.PixelCount)
		}
		if topologyErr != nil {
			*validation = append(*validation, ledValidation{Severity: "error", CabinetID: a.CabinetID, DrawerID: a.DrawerID, Message: fmt.Sprintf("Allocation %d cannot be mapped: %v", a.ID, topologyErr), Repair: "Review configured topology and range bounds; coverage counts are unknown until corrected."})
			for _, m := range members {
				if m.CabinetID == a.CabinetID {
					unknown[m.ControllerID] = true
				}
			}
			continue
		}
		for _, p := range pieces {
			c, ok := controllerByID[p.ControllerID]
			start := p.LocalStart - 1
			if !ok || start < 0 || p.Count < 1 || p.Count > c.LEDCount-start {
				unknown[p.ControllerID] = true
				continue
			}
			coverage[p.ControllerID] = append(coverage[p.ControllerID], interval{start, start + p.Count})
		}
	}
	unassignedControllers := 0
	for i, c := range controllers {
		if !assigned[c.ID] {
			unassignedControllers++
			*validation = append(*validation, ledValidation{Severity: "warning", ControllerID: c.ID, Message: "Controller is not assigned to a cabinet", Repair: "Assign the configured controller to its intended cabinet if it should participate in mapping."})
		}
		if c.LEDCount < 0 || unknown[c.ID] {
			continue
		}
		spans := coverage[c.ID]
		sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
		allocated, end := int64(0), int64(0)
		for _, span := range spans {
			left := max(end, span.start)
			if span.end > left {
				allocated += span.end - left
			}
			end = max(end, span.end)
		}
		unassigned := c.LEDCount - allocated
		controllers[i].AllocatedPixelCount = &allocated
		controllers[i].UnassignedPixelCount = &unassigned
		if unassigned > 0 {
			*validation = append(*validation, ledValidation{Severity: "info", ControllerID: c.ID, Message: fmt.Sprintf("%d of %d configured pixel addresses are not allocated to drawers", unassigned, c.LEDCount), Repair: "Allocate additional drawer ranges only where intended; unused capacity is not automatically repaired."})
		}
	}
	return unassignedControllers, nil
}

func (h *Handler) HandleLEDTestCentreData(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Controllers           []ledDataController          `json:"controllers"`
		Cabinets              []ledDataCabinet             `json:"cabinets"`
		Operations            []wled.PhysicalLocateSession `json:"operations"`
		Emergency             []wled.ControllerOffResult   `json:"emergency_results"`
		Styles                wled.FunctionSettings        `json:"styles"`
		Validation            []ledValidation              `json:"validation"`
		PhysicalOutputEnabled bool                         `json:"physical_output_enabled"`
		UnassignedControllers int                          `json:"unassigned_controller_count"`
	}{Controllers: []ledDataController{}, Cabinets: []ledDataCabinet{}, Operations: []wled.PhysicalLocateSession{}, Emergency: []wled.ControllerOffResult{}, Validation: []ledValidation{}, PhysicalOutputEnabled: config.PhysicalWLEDOutputEnabled()}
	err := db.ExecLEDRead(r.Context(), h.Queries, func(q db.LEDQuerier) error {
		ctx := r.Context()
		var e error
		out.Styles, e = wled.LoadFunctionSettings(ctx, q)
		if e != nil {
			return e
		}
		controllers, e := q.GetControllers(ctx)
		if e != nil {
			return e
		}
		for _, c := range controllers {
			out.Controllers = append(out.Controllers, ledDataController{ID: c.ID, Name: c.Name, LEDCount: c.LedCount, Verified: c.LocateMappingVerified})
			outputs, readErr := q.GetControllerOutputs(ctx, c.ID)
			if readErr != nil {
				return readErr
			}
			if len(outputs) > 0 {
				settings := controllerconfig.Settings{ControllerType: c.ControllerType, ConnectionTimeoutMS: c.ConnectionTimeoutMs}
				var configErr error
				for _, output := range outputs {
					var custom []int64
					if e := json.Unmarshal([]byte(output.CustomOrderJson), &custom); e != nil {
						configErr = e
						break
					}
					settings.Outputs = append(settings.Outputs, controllerconfig.Output{ID: output.ID, DisplayOrder: output.DisplayOrder, Name: output.Name, PixelCount: output.PixelCount, Direction: output.Direction, Pattern: output.Pattern, Rows: output.Rows, Columns: output.Columns, CustomOrder: custom})
				}
				if configErr == nil {
					var total int64
					total, configErr = settings.NormalizeAndValidate()
					if configErr == nil && total != c.LedCount {
						configErr = fmt.Errorf("configured output capacity %d does not match controller capacity %d", total, c.LedCount)
					}
				}
				if configErr != nil {
					out.Validation = append(out.Validation, ledValidation{Severity: "error", ControllerID: c.ID, Message: configErr.Error(), Repair: "Review configured output counts and pixel permutations; no automatic repair applied."})
				}
			}
			if c.LedCount <= 0 {
				out.Validation = append(out.Validation, ledValidation{Severity: "warning", ControllerID: c.ID, Message: "Controller has no positive configured capacity", Repair: "Configure controller output capacities before mapping pixels."})
			}
			if !c.LocateMappingVerified {
				out.Validation = append(out.Validation, ledValidation{Severity: "warning", ControllerID: c.ID, Message: "Controller physical mapping is not verified", Repair: "Verify controller mapping before enabling physical output."})
			}
		}
		savedPixels, e := q.ListPhysicalBinPixels(ctx)
		if e != nil {
			return e
		}
		attachments, e := q.ListBinDrawerAttachments(ctx)
		if e != nil {
			return e
		}
		attached := map[int64]int64{}
		for _, a := range attachments {
			attached[a.BinID] = a.DrawerID
		}
		for i, p := range savedPixels {
			if attached[p.BinID] != p.DrawerID {
				out.Validation = append(out.Validation, ledValidation{Severity: "error", ControllerID: p.ControllerID, DrawerID: p.DrawerID, BinID: p.BinID, Message: "Stored bin pixel belongs to a different or detached drawer attachment", Repair: "Attach the bin to its intended drawer and explicitly reassign its pixel; Locate fails closed."})
			}
			for _, prior := range savedPixels[:i] {
				alias := p.ControllerID == prior.ControllerID
				if !alias {
					var a, b db.Controller
					for _, c := range controllers {
						if c.ID == p.ControllerID {
							a = c
						}
						if c.ID == prior.ControllerID {
							b = c
						}
					}
					alias = wled.ConfiguredControllersAlias(a, b)
				}
				if alias && p.Pixel == prior.Pixel {
					out.Validation = append(out.Validation, ledValidation{Severity: "error", ControllerID: p.ControllerID, DrawerID: p.DrawerID, BinID: p.BinID, Message: fmt.Sprintf("Physical pixel is also assigned to bin %d", prior.BinID), Repair: "Review duplicate assignments and controller aliases; no automatic repair applied."})
				}
			}
		}
		cabinets, e := q.ListCabinets(ctx)
		if e != nil {
			return e
		}
		type occupied struct {
			cabinetID, drawerID int64
			piece               cabinetpixels.PhysicalPiece
		}
		occupiedPixels := []occupied{}
		for _, c := range cabinets {
			topology, topologyErr := cabinetpixels.Load(ctx, q, c.ID)
			if topologyErr == nil && topology.Total > 0 {
				_, topologyErr = cabinetpixels.SplitTopologyRange(topology, 1, topology.Total)
			}
			if topologyErr != nil {
				out.Validation = append(out.Validation, ledValidation{Severity: "error", CabinetID: c.ID, Message: topologyErr.Error(), Repair: "Review configured controller capacities and electrical output topology."})
			}
			dc := ledDataCabinet{c.ID, c.Name, []ledDataDrawer{}}
			drawers, e := q.ListDrawersByCabinet(ctx, c.ID)
			if e != nil {
				return e
			}
			for _, d := range drawers {
				dd := ledDataDrawer{d.ID, d.Label, []ledDataRange{}, []ledDataBin{}}
				ranges, e := q.ListDrawerLEDAllocationsByDrawer(ctx, d.ID)
				if e != nil {
					return e
				}
				relative := int64(0)
				for _, a := range ranges {
					pieces, e := ledPhysicalRanges(ctx, q, c.ID, a.StartPixel, a.PixelCount)
					if e != nil {
						out.Validation = append(out.Validation, ledValidation{Severity: "error", CabinetID: c.ID, DrawerID: d.ID, Message: e.Error(), Repair: "Review cabinet topology and drawer ranges; no automatic repair applied."})
					}
					physical := []ledDataPhysical{}
					for _, piece := range pieces {
						// Emit exact logical-to-physical assignments. A reversed
						// span's ascending interval does not describe logical order.
						topology, loadErr := cabinetpixels.Load(ctx, q, c.ID)
						if loadErr != nil {
							return loadErr
						}
						for n := int64(0); n < piece.Count; n++ {
							one, mapErr := cabinetpixels.SplitTopologyRange(topology, piece.CabinetStart+n, 1)
							if mapErr != nil {
								return mapErr
							}
							if len(one) != 1 {
								return ledMappingInvalid("ambiguous pixel topology")
							}
							physical = append(physical, ledDataPhysical{one[0].ControllerID, one[0].LocalStart - 1, 1, relative + piece.CabinetStart + n - a.StartPixel})
						}
						for _, old := range occupiedPixels {
							alias := old.piece.ControllerID == piece.ControllerID
							if !alias {
								var first, second db.Controller
								for _, configured := range controllers {
									if configured.ID == old.piece.ControllerID {
										first = configured
									}
									if configured.ID == piece.ControllerID {
										second = configured
									}
								}
								alias = wled.ConfiguredControllersAlias(first, second)
							}
							if alias && old.piece.LocalStart <= piece.LocalStart+piece.Count-1 && piece.LocalStart <= old.piece.LocalStart+old.piece.Count-1 {
								out.Validation = append(out.Validation, ledValidation{Severity: "error", ControllerID: piece.ControllerID, CabinetID: c.ID, DrawerID: d.ID, Message: fmt.Sprintf("Physical range overlaps cabinet %d drawer %d", old.cabinetID, old.drawerID), Repair: "Review overlapping drawer allocations; no automatic repairs applied."})
							}
						}
						occupiedPixels = append(occupiedPixels, occupied{c.ID, d.ID, piece})
					}
					dd.Ranges = append(dd.Ranges, ledDataRange{a.StartPixel, a.PixelCount, physical})
					if a.PixelCount > 0 && a.PixelCount <= math.MaxInt64-relative {
						relative += a.PixelCount
					}
				}
				if len(ranges) == 0 {
					out.Validation = append(out.Validation, ledValidation{Severity: "warning", CabinetID: c.ID, DrawerID: d.ID, Message: "Drawer has no pixel assignment", Repair: "Preview and confirm a drawer allocation."})
				}
				attachments, e := q.ListBinDrawerAttachmentsByDrawer(ctx, d.ID)
				if e != nil {
					return e
				}
				for _, a := range attachments {
					b, e := q.GetBin(ctx, a.BinID)
					if errors.Is(e, sql.ErrNoRows) {
						out.Validation = append(out.Validation, ledValidation{Severity: "error", CabinetID: c.ID, DrawerID: d.ID, BinID: a.BinID, Message: "Drawer attachment references a missing bin", Repair: "Review the invalid attachment; no automatic repair applied."})
						continue
					}
					if e != nil {
						return e
					}
					item := ledDataBin{ID: b.ID, Label: b.Name}
					saved, loadErr := q.GetPhysicalBinPixel(ctx, b.ID)
					if errors.Is(loadErr, sql.ErrNoRows) {
						out.Validation = append(out.Validation, ledValidation{Severity: "warning", CabinetID: c.ID, DrawerID: d.ID, BinID: b.ID, Message: "Bin has no explicit pixel assignment; Locate uses the whole drawer.", Repair: "Assign a physical pixel to this bin if a single-pixel override is wanted."})
					} else if loadErr != nil {
						return loadErr
					} else {
						item.Assigned = true
						item.ControllerID = &saved.ControllerID
						item.Pixel = &saved.Pixel
						item.AllocationID = &saved.AllocationID
						topology, topErr := cabinetpixels.Load(ctx, q, c.ID)
						if topErr == nil {
							_, _, relative, resolveErr := wled.ResolveBinPixelOverride(ctx, q, b.ID, d.ID, topology, ranges)
							topErr = resolveErr
							if resolveErr == nil {
								item.RelativePixel = &relative
							}
						}
						if topErr != nil {
							item.Stale = true
							out.Validation = append(out.Validation, ledValidation{Severity: "error", ControllerID: saved.ControllerID, CabinetID: c.ID, DrawerID: d.ID, BinID: b.ID, Message: topErr.Error(), Repair: "Reassign this bin pixel explicitly; Locate is blocked until corrected."})
						}
					}
					dd.Bins = append(dd.Bins, item)
				}
				dc.Drawers = append(dc.Drawers, dd)
			}
			out.Cabinets = append(out.Cabinets, dc)
		}
		out.UnassignedControllers, e = ledValidateCoverage(ctx, q, out.Controllers, &out.Validation)
		if e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		ledAPIError(w, err)
		return
	}
	if h.Locator != nil {
		out.Operations, err = h.Locator.Operations(r.Context())
		if err != nil {
			ledAPIError(w, err)
			return
		}
		if out.Operations == nil {
			out.Operations = []wled.PhysicalLocateSession{}
		}
		out.Emergency, err = h.Locator.EmergencyStatus(r.Context())
		if err != nil {
			ledAPIError(w, err)
			return
		}
		if out.Emergency == nil {
			out.Emergency = []wled.ControllerOffResult{}
		}
	}
	ledJSON(w, http.StatusOK, out)
}
