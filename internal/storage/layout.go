package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
)

var (
	ErrLayoutNotFound = errors.New("physical layout not found")
	ErrLayoutStale    = errors.New("physical layout has changed; reload before saving")
	ErrLayoutInvalid  = errors.New("invalid physical layout")
	ErrUnsafeLayout   = errors.New("unsafe physical layout operation")
)

type LayoutService struct{ store db.Store }

func NewLayoutService(store db.Store) *LayoutService { return &LayoutService{store: store} }

type CabinetLayout struct {
	Cabinet             db.Cabinet
	Controller          *db.Controller
	Controllers         []db.Controller
	AssignedControllers []AssignedController
	ControllerError     string
	Wiring              cabinetpixels.Topology
	Drawers             []DrawerLayout
	UnattachedBins      []db.ListUnattachedBinsRow
}
type AssignedController struct {
	Slot              int64
	StringsConfigured int64
	Controller        db.Controller
}
type DrawerLayout struct {
	Drawer         db.Drawer
	Bins           []LayoutBin
	LedAllocations []db.DrawerLedAllocation
}

type LayoutBin struct {
	BinID        int64   `json:"bin_id"`
	Name         string  `json:"name"`
	DrawerID     int64   `json:"drawer_id"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
	DisplayOrder int64   `json:"display_order"`
}

// DecorationInput describes Paint-style canvas art. It is deliberately separate
// from drawers and bins so art can never acquire an inventory or LED identity.
type DecorationInput struct {
	Key    string  `json:"key"`
	Kind   string  `json:"kind"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Text   string  `json:"text,omitempty"`
}

type LayoutInput struct {
	ID               int64             `json:"id"`
	Revision         int64             `json:"revision"`
	Name             string            `json:"name"`
	CoordinateSystem string            `json:"coordinate_system"`
	DisplayOrder     int64             `json:"display_order"`
	Width            float64           `json:"width"`
	Height           float64           `json:"height"`
	Drawers          []DrawerInput     `json:"drawers"`
	Decorations      []DecorationInput `json:"decorations"`
}
type DrawerInput struct {
	ID           int64      `json:"id"`
	Label        string     `json:"label"`
	DisplayOrder int64      `json:"display_order"`
	X            float64    `json:"x"`
	Y            float64    `json:"y"`
	Width        float64    `json:"width"`
	Height       float64    `json:"height"`
	Bins         []BinInput `json:"bins"`
}
type BinInput struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	DisplayOrder int64   `json:"display_order"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
}

func (s *LayoutService) Get(ctx context.Context, cabinetID int64) (CabinetLayout, error) {
	// JSON editor consumers require arrays, never null, for an empty cabinet.
	var out = CabinetLayout{Drawers: make([]DrawerLayout, 0), UnattachedBins: make([]db.ListUnattachedBinsRow, 0)}
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		cabinet, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		out.Cabinet = cabinet
		out.Wiring, err = cabinetpixels.Load(ctx, q, cabinetID)
		if err != nil {
			return err
		}
		controllers, err := q.GetControllers(ctx)
		if err != nil {
			return err
		}
		if controllers != nil {
			out.Controllers = controllers
		}
		if out.Controllers == nil {
			out.Controllers = make([]db.Controller, 0)
		}
		members, err := q.ListCabinetControllers(ctx, cabinetID)
		if err != nil {
			return err
		}
		out.AssignedControllers = make([]AssignedController, 0, len(members))
		for _, member := range members {
			controller, controllerErr := q.GetController(ctx, member.ControllerID)
			if errors.Is(controllerErr, sql.ErrNoRows) {
				out.ControllerError = fmt.Sprintf("Cabinet references missing LED controller #%d.", member.ControllerID)
				continue
			}
			if controllerErr != nil {
				return controllerErr
			}
			out.AssignedControllers = append(out.AssignedControllers, AssignedController{Slot: member.Slot, StringsConfigured: member.StringsConfigured, Controller: controller})
			if cabinet.ControllerID.Valid && cabinet.ControllerID.Int64 == controller.ID {
				primary := controller
				out.Controller = &primary
			}
		}
		if out.Controller == nil && cabinet.ControllerID.Valid {
			controller, controllerErr := q.GetController(ctx, cabinet.ControllerID.Int64)
			if errors.Is(controllerErr, sql.ErrNoRows) {
				out.ControllerError = fmt.Sprintf("Cabinet references missing LED controller #%d.", cabinet.ControllerID.Int64)
			} else if controllerErr != nil {
				return controllerErr
			} else {
				out.Controller = &controller
			}
		}
		drawers, err := q.ListDrawersByCabinetIncludingArchived(ctx, cabinetID)
		if err != nil {
			return err
		}
		for _, drawer := range drawers {
			attachments, err := q.ListBinDrawerAttachmentsByDrawer(ctx, drawer.ID)
			if err != nil {
				return err
			}
			bins := make([]LayoutBin, 0, len(attachments))
			for _, attachment := range attachments {
				bin, err := q.GetBin(ctx, attachment.BinID)
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: placement refers to missing bin %d", ErrUnsafeLayout, attachment.BinID)
				}
				if err != nil {
					return err
				}
				bins = append(bins, LayoutBin{
					BinID: attachment.BinID, Name: bin.Name, DrawerID: attachment.DrawerID,
					X: attachment.X, Y: attachment.Y, Width: attachment.Width, Height: attachment.Height,
					DisplayOrder: attachment.DisplayOrder,
				})
			}
			allocations, err := q.ListDrawerLEDAllocationsByDrawer(ctx, drawer.ID)
			if err != nil {
				return err
			}
			if allocations == nil {
				allocations = make([]db.DrawerLedAllocation, 0)
			}
			out.Drawers = append(out.Drawers, DrawerLayout{Drawer: drawer, Bins: bins, LedAllocations: allocations})
		}
		unattached, err := q.ListUnattachedBins(ctx)
		if err != nil {
			return err
		}
		if unattached != nil {
			out.UnattachedBins = unattached
		}
		return nil
	})
	return out, err
}

func finitePositive(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }
func validRect(x, y, w, h, maxW, maxH float64) bool {
	return !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0) && x >= 0 && y >= 0 && finitePositive(w) && finitePositive(h) && x+w <= maxW && y+h <= maxH
}
func overlaps(aX, aY, aW, aH, bX, bY, bW, bH float64) bool {
	return aX < bX+bW && aX+aW > bX && aY < bY+bH && aY+aH > bY
}
func layoutError(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrLayoutInvalid}, a...)...)
}

func validate(in LayoutInput) error {
	if strings.TrimSpace(in.Name) == "" || len(strings.TrimSpace(in.Name)) > 200 {
		return layoutError("cabinet name is required")
	}
	if strings.TrimSpace(in.CoordinateSystem) == "" || !finitePositive(in.Width) || !finitePositive(in.Height) {
		return layoutError("cabinet dimensions must be positive")
	}
	seenDrawers := map[int64]bool{}
	seenBins := map[int64]bool{}
	for i, d := range in.Drawers {
		if d.ID != 0 {
			if seenDrawers[d.ID] {
				return layoutError("drawer %d occurs twice", d.ID)
			}
			seenDrawers[d.ID] = true
		}
		if strings.TrimSpace(d.Label) == "" {
			return layoutError("drawer %d requires a label", i+1)
		}
		if !validRect(d.X, d.Y, d.Width, d.Height, in.Width, in.Height) {
			return layoutError("drawer %q is outside cabinet bounds", d.Label)
		}
		for j := 0; j < i; j++ {
			other := in.Drawers[j]
			if overlaps(d.X, d.Y, d.Width, d.Height, other.X, other.Y, other.Width, other.Height) {
				return layoutError("drawers %q and %q overlap", d.Label, other.Label)
			}
		}
		for bi, b := range d.Bins {
			if b.ID <= 0 || seenBins[b.ID] {
				return layoutError("bin %d is missing or duplicated", bi+1)
			}
			seenBins[b.ID] = true
			if strings.TrimSpace(b.Name) == "" || len(strings.TrimSpace(b.Name)) > 200 {
				return layoutError("bin %d requires a name of 1 to 200 characters", b.ID)
			}
			if !validRect(b.X, b.Y, b.Width, b.Height, d.Width, d.Height) {
				return layoutError("bin %d in drawer %q is outside bounds", b.ID, d.Label)
			}
			for pj := 0; pj < bi; pj++ {
				p := d.Bins[pj]
				if overlaps(b.X, b.Y, b.Width, b.Height, p.X, p.Y, p.Width, p.Height) {
					return layoutError("bins %d and %d overlap", b.ID, p.ID)
				}
			}
		}
	}
	seenDecorations := map[string]bool{}
	for _, d := range in.Decorations {
		if d.Key == "" || seenDecorations[d.Key] || (d.Kind != "outline" && d.Kind != "rectangle" && d.Kind != "line" && d.Kind != "label") {
			return layoutError("decorative object has an invalid type or identity")
		}
		seenDecorations[d.Key] = true
		if math.IsNaN(d.X) || math.IsNaN(d.Y) || math.IsInf(d.X, 0) || math.IsInf(d.Y, 0) || d.X < 0 || d.Y < 0 || math.IsNaN(d.Width) || math.IsNaN(d.Height) || math.IsInf(d.Width, 0) || math.IsInf(d.Height, 0) || d.Width < 0 || d.Height < 0 || d.X+d.Width > in.Width || d.Y+d.Height > in.Height {
			return layoutError("decorative object is outside cabinet bounds")
		}
		if d.Kind != "line" && d.Kind != "label" && (!finitePositive(d.Width) || !finitePositive(d.Height)) {
			return layoutError("decorative rectangle dimensions must be positive")
		}
		if d.Kind == "label" && (strings.TrimSpace(d.Text) == "" || len(d.Text) > 300) {
			return layoutError("decorative labels must contain 1 to 300 characters")
		}
	}
	return nil
}

func (s *LayoutService) Save(ctx context.Context, in LayoutInput) error {
	if err := validate(in); err != nil {
		return err
	}
	decorations := in.Decorations
	if decorations == nil {
		decorations = make([]DecorationInput, 0)
	}
	decorationsJSON, err := json.Marshal(decorations)
	if err != nil {
		return layoutError("decorative drawing data is invalid")
	}
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := ensureNoPhysicalLocate(ctx, q); err != nil {
			return err
		}
		cab, err := q.GetCabinet(ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cab.LayoutRevision != in.Revision {
			return ErrLayoutStale
		}
		existing, err := q.ListDrawersByCabinetIncludingArchived(ctx, in.ID)
		if err != nil {
			return err
		}
		byID := map[int64]db.Drawer{}
		for _, d := range existing {
			byID[d.ID] = d
		}
		provided := map[int64]bool{}
		attached := map[int64]db.BinDrawerAttachment{}
		for _, d := range existing {
			bins, e := q.ListBinDrawerAttachmentsByDrawer(ctx, d.ID)
			if e != nil {
				return e
			}
			for _, b := range bins {
				attached[b.BinID] = b
			}
		}
		providedBins := map[int64]bool{}
		for _, d := range in.Drawers {
			if d.ID != 0 {
				if _, ok := byID[d.ID]; !ok {
					return layoutError("drawer %d does not belong to this cabinet", d.ID)
				}
				provided[d.ID] = true
			}
			for _, b := range d.Bins {
				providedBins[b.ID] = true
			}
		}
		for _, d := range existing {
			if !provided[d.ID] {
				return fmt.Errorf("%w: drawer %q cannot be removed; archive it only after its bins are safely moved", ErrUnsafeLayout, d.Label)
			}
		}
		for binID := range attached {
			if !providedBins[binID] {
				return fmt.Errorf("%w: attached bin %d cannot be omitted; explicitly preserve or relocate it", ErrUnsafeLayout, binID)
			}
		}
		if _, err = q.UpdateCabinetLayoutSettings(ctx, db.UpdateCabinetLayoutSettingsParams{Name: strings.TrimSpace(in.Name), CoordinateSystem: strings.TrimSpace(in.CoordinateSystem), DisplayOrder: in.DisplayOrder, LayoutWidth: in.Width, LayoutHeight: in.Height, LayoutDecorationsJson: string(decorationsJSON), ID: in.ID}); err != nil {
			return err
		}
		for _, input := range in.Drawers {
			id := input.ID
			if id == 0 {
				id, err = q.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: in.ID, Label: strings.TrimSpace(input.Label), DisplayOrder: input.DisplayOrder, X: input.X, Y: input.Y, Width: input.Width, Height: input.Height})
				if err != nil {
					return err
				}
			} else {
				if _, err = q.UpdateDrawer(ctx, db.UpdateDrawerParams{CabinetID: in.ID, Label: strings.TrimSpace(input.Label), DisplayOrder: input.DisplayOrder, ID: id}); err != nil {
					return err
				}
				if _, err = q.UpdateDrawerGeometry(ctx, db.UpdateDrawerGeometryParams{ID: id, X: input.X, Y: input.Y, Width: input.Width, Height: input.Height}); err != nil {
					return err
				}
			}
			for _, b := range input.Bins {
				current, exists := attached[b.ID]
				if exists && current.DrawerID != id {
					return fmt.Errorf("%w: bin %d is already attached to a different drawer; use Move bin", ErrUnsafeLayout, b.ID)
				}
				if _, err := q.GetBin(ctx, b.ID); err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return layoutError("bin %d does not exist", b.ID)
					}
					return err
				}
				if _, err := q.UpdateBinName(ctx, db.UpdateBinNameParams{Name: strings.TrimSpace(b.Name), ID: b.ID}); err != nil {
					return err
				}
				if exists {
					if _, err = q.UpdateBinDrawerAttachment(ctx, db.UpdateBinDrawerAttachmentParams{DrawerID: id, DisplayOrder: b.DisplayOrder, BinID: b.ID}); err != nil {
						return err
					}
					if _, err = q.UpdateBinDrawerAttachmentGeometry(ctx, db.UpdateBinDrawerAttachmentGeometryParams{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height, BinID: b.ID}); err != nil {
						return err
					}
				} else {
					if _, err := q.GetBinDrawerAttachment(ctx, b.ID); err == nil {
						return fmt.Errorf("%w: bin %d is already attached outside this cabinet", ErrUnsafeLayout, b.ID)
					} else if !errors.Is(err, sql.ErrNoRows) {
						return err
					}
					if err := q.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: b.ID, DrawerID: id, X: b.X, Y: b.Y, Width: b.Width, Height: b.Height, DisplayOrder: b.DisplayOrder}); err != nil {
						return err
					}
				}
			}
		}
		rows, err := q.AdvanceCabinetLayoutRevision(ctx, db.AdvanceCabinetLayoutRevisionParams{ID: in.ID, LayoutRevision: in.Revision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutStale
		}
		return nil
	})
}

func (s *LayoutService) CreateCabinet(ctx context.Context, name string, order int64, width, height float64) (int64, error) {
	if strings.TrimSpace(name) == "" || !finitePositive(width) || !finitePositive(height) {
		return 0, layoutError("cabinet name and positive dimensions are required")
	}
	var id int64
	err := s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := ensureNoPhysicalLocate(ctx, q); err != nil {
			return err
		}
		var e error
		id, e = q.CreateCabinet(ctx, db.CreateCabinetParams{Name: strings.TrimSpace(name), ControllerID: sql.NullInt64{}, CoordinateSystem: "grid", DisplayOrder: order})
		if e != nil {
			return e
		}
		_, e = q.UpdateCabinetLayoutSettings(ctx, db.UpdateCabinetLayoutSettingsParams{Name: strings.TrimSpace(name), CoordinateSystem: "grid", DisplayOrder: order, LayoutWidth: width, LayoutHeight: height, LayoutDecorationsJson: "[]", ID: id})
		return e
	})
	return id, err
}

// CreatePhysicalBin creates a new inventory bin and its physical placement in
// one transaction. Electrical container and LED fields intentionally remain NULL.
func (s *LayoutService) CreatePhysicalBin(ctx context.Context, cabinetID, drawerID, revision int64, name string, x, y, width, height float64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return 0, layoutError("bin name is required and must be at most 200 characters")
	}
	if !finitePositive(width) || !finitePositive(height) {
		return 0, layoutError("bin width and height must be positive")
	}

	var binID int64
	err := s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := ensureNoPhysicalLocate(ctx, q); err != nil {
			return err
		}
		cabinet, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cabinet.LayoutRevision != revision {
			return ErrLayoutStale
		}
		drawer, err := q.GetDrawer(ctx, drawerID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if drawer.CabinetID != cabinetID || drawer.Archived != 0 {
			return fmt.Errorf("%w: choose an active drawer in this cabinet", ErrUnsafeLayout)
		}
		if !validRect(x, y, width, height, drawer.Width, drawer.Height) {
			return layoutError("bin is outside drawer bounds")
		}
		attachments, err := q.ListBinDrawerAttachmentsByDrawer(ctx, drawerID)
		if err != nil {
			return err
		}
		if err := checkBinOverlap(attachments, 0, x, y, width, height); err != nil {
			return err
		}
		binID, err = q.CreateBin(ctx, db.CreateBinParams{
			Name: name, ContainerID: sql.NullInt64{}, LedIndex: sql.NullInt64{},
			Width: sql.NullInt64{}, GridX: sql.NullInt64{}, GridY: sql.NullInt64{},
		})
		if err != nil {
			return err
		}
		if err := q.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{
			BinID: binID, DrawerID: drawerID, X: x, Y: y, Width: width, Height: height, DisplayOrder: int64(len(attachments)),
		}); err != nil {
			return err
		}
		rows, err := q.AdvanceCabinetLayoutRevision(ctx, db.AdvanceCabinetLayoutRevisionParams{ID: cabinetID, LayoutRevision: revision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutStale
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return binID, nil
}

// MovePhysicalBin moves an existing bin to another active drawer in the same
// cabinet. It updates only the attachment row, preserving the bin ID, QR code,
// stock assignments and any separate electrical data.
func (s *LayoutService) MovePhysicalBin(ctx context.Context, cabinetID, binID, targetDrawerID, revision int64, x, y float64) error {
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := ensureNoPhysicalLocate(ctx, q); err != nil {
			return err
		}
		cabinet, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cabinet.LayoutRevision != revision {
			return ErrLayoutStale
		}
		attachment, err := q.GetBinDrawerAttachment(ctx, binID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: bin has no physical placement to move", ErrUnsafeLayout)
		}
		if err != nil {
			return err
		}
		source, err := q.GetDrawer(ctx, attachment.DrawerID)
		if err != nil {
			return err
		}
		target, err := q.GetDrawer(ctx, targetDrawerID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if source.CabinetID != cabinetID || target.CabinetID != cabinetID || source.Archived != 0 || target.Archived != 0 {
			return fmt.Errorf("%w: move bins between active drawers in the same cabinet", ErrUnsafeLayout)
		}
		if source.ID == target.ID {
			return layoutError("choose a different destination drawer")
		}
		if _, err := q.GetBin(ctx, binID); err != nil {
			return err
		}
		if !validRect(x, y, attachment.Width, attachment.Height, target.Width, target.Height) {
			return layoutError("bin is outside destination drawer bounds")
		}
		attachments, err := q.ListBinDrawerAttachmentsByDrawer(ctx, targetDrawerID)
		if err != nil {
			return err
		}
		if err := checkBinOverlap(attachments, binID, x, y, attachment.Width, attachment.Height); err != nil {
			return err
		}
		if _, err := q.UpdateBinDrawerAttachment(ctx, db.UpdateBinDrawerAttachmentParams{DrawerID: targetDrawerID, DisplayOrder: int64(len(attachments)), BinID: binID}); err != nil {
			return err
		}
		if _, err := q.UpdateBinDrawerAttachmentGeometry(ctx, db.UpdateBinDrawerAttachmentGeometryParams{X: x, Y: y, Width: attachment.Width, Height: attachment.Height, BinID: binID}); err != nil {
			return err
		}
		rows, err := q.AdvanceCabinetLayoutRevision(ctx, db.AdvanceCabinetLayoutRevisionParams{ID: cabinetID, LayoutRevision: revision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutStale
		}
		return nil
	})
}

func checkBinOverlap(attachments []db.BinDrawerAttachment, exceptBinID int64, x, y, width, height float64) error {
	for _, other := range attachments {
		if other.BinID != exceptBinID && overlaps(x, y, width, height, other.X, other.Y, other.Width, other.Height) {
			return layoutError("bin placement overlaps bin %d", other.BinID)
		}
	}
	return nil
}

func ensureNoPhysicalLocate(ctx context.Context, q db.Querier) error {
	owned, err := db.HasPhysicalLocateOwnership(ctx, q)
	if err != nil {
		return fmt.Errorf("check durable Locate ownership before physical layout change: %w", err)
	}
	if owned {
		return db.ErrPhysicalLocateActive
	}
	return nil
}

// ArchiveCabinet preserves every drawer, bin, stock assignment and electrical
// record. It only hides the cabinet from active physical browsing.
func (s *LayoutService) ArchiveCabinet(ctx context.Context, cabinetID, revision int64) error {
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := ensureNoPhysicalLocate(ctx, q); err != nil {
			return err
		}
		cab, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cab.LayoutRevision != revision {
			return ErrLayoutStale
		}
		if _, err = q.ArchiveCabinet(ctx, cabinetID); err != nil {
			return err
		}
		rows, err := q.AdvanceCabinetLayoutRevision(ctx, db.AdvanceCabinetLayoutRevisionParams{ID: cabinetID, LayoutRevision: revision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutStale
		}
		return nil
	})
}

// DeleteCabinet only removes an empty physical cabinet. Drawer, bin, inventory
// and electrical records are never cascaded.
func (s *LayoutService) DeleteCabinet(ctx context.Context, cabinetID, revision int64) error {
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := ensureNoPhysicalLocate(ctx, q); err != nil {
			return err
		}
		cab, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cab.LayoutRevision != revision {
			return ErrLayoutStale
		}
		drawers, err := q.ListDrawersByCabinetIncludingArchived(ctx, cabinetID)
		if err != nil {
			return err
		}
		if len(drawers) != 0 {
			return fmt.Errorf("%w: cabinet has %d drawer(s); remove them explicitly first", ErrUnsafeLayout, len(drawers))
		}
		rows, err := q.DeleteCabinet(ctx, cabinetID)
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutNotFound
		}
		return nil
	})
}

func (s *LayoutService) ArchiveDrawer(ctx context.Context, cabinetID, drawerID, revision int64) error {
	return s.drawerMutation(ctx, cabinetID, drawerID, revision, true)
}
func (s *LayoutService) DeleteDrawer(ctx context.Context, cabinetID, drawerID, revision int64) error {
	return s.drawerMutation(ctx, cabinetID, drawerID, revision, false)
}
func (s *LayoutService) drawerMutation(ctx context.Context, cabinetID, drawerID, revision int64, archive bool) error {
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := ensureNoPhysicalLocate(ctx, q); err != nil {
			return err
		}
		cab, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cab.LayoutRevision != revision {
			return ErrLayoutStale
		}
		drawer, err := q.GetDrawer(ctx, drawerID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if drawer.CabinetID != cabinetID {
			return fmt.Errorf("%w: drawer does not belong to cabinet", ErrUnsafeLayout)
		}
		if !archive {
			bins, e := q.ListBinDrawerAttachmentsByDrawer(ctx, drawerID)
			if e != nil {
				return e
			}
			if len(bins) != 0 {
				return fmt.Errorf("%w: drawer has %d bin placement(s); remove or relocate them first", ErrUnsafeLayout, len(bins))
			}
			allocations, e := q.ListDrawerLEDAllocationsByDrawer(ctx, drawerID)
			if e != nil {
				return e
			}
			if len(allocations) != 0 {
				return fmt.Errorf("%w: drawer has %d saved LED range(s); clear its electrical mapping first", ErrUnsafeLayout, len(allocations))
			}
		}
		if archive {
			_, err = q.ArchiveDrawer(ctx, drawerID)
		} else {
			_, err = q.DeleteDrawer(ctx, drawerID)
		}
		if err != nil {
			return err
		}
		rows, err := q.AdvanceCabinetLayoutRevision(ctx, db.AdvanceCabinetLayoutRevisionParams{ID: cabinetID, LayoutRevision: revision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutStale
		}
		return nil
	})
}

// DetachBinPlacement removes only the physical attachment. The bins row,
// assignments, quantities, legacy container and LED coordinates stay untouched.
func (s *LayoutService) DetachBinPlacement(ctx context.Context, cabinetID, binID, revision int64) error {
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
		if err := ensureNoPhysicalLocate(ctx, q); err != nil {
			return err
		}
		cab, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		if cab.LayoutRevision != revision {
			return ErrLayoutStale
		}
		attachment, err := q.GetBinDrawerAttachment(ctx, binID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		drawer, err := q.GetDrawer(ctx, attachment.DrawerID)
		if err != nil {
			return err
		}
		if drawer.CabinetID != cabinetID {
			return fmt.Errorf("%w: placement belongs to another cabinet", ErrUnsafeLayout)
		}
		rows, err := q.DeleteBinDrawerAttachment(ctx, binID)
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutNotFound
		}
		rows, err = q.AdvanceCabinetLayoutRevision(ctx, db.AdvanceCabinetLayoutRevisionParams{ID: cabinetID, LayoutRevision: revision})
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrLayoutStale
		}
		return nil
	})
}

func sortDrawerLayouts(items []DrawerLayout) {
	sort.Slice(items, func(i, j int) bool { return items[i].Drawer.ID < items[j].Drawer.ID })
}
