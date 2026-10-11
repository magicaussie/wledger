package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

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
	Cabinet        db.Cabinet
	Drawers        []DrawerLayout
	UnattachedBins []db.ListUnattachedBinsRow
}
type DrawerLayout struct {
	Drawer db.Drawer
	Bins   []db.BinDrawerAttachment
}
type LayoutInput struct {
	ID               int64         `json:"id"`
	Revision         int64         `json:"revision"`
	Name             string        `json:"name"`
	CoordinateSystem string        `json:"coordinate_system"`
	DisplayOrder     int64         `json:"display_order"`
	Width            float64       `json:"width"`
	Height           float64       `json:"height"`
	Drawers          []DrawerInput `json:"drawers"`
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
	var out CabinetLayout
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		cabinet, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLayoutNotFound
		}
		if err != nil {
			return err
		}
		out.Cabinet = cabinet
		drawers, err := q.ListDrawersByCabinetIncludingArchived(ctx, cabinetID)
		if err != nil {
			return err
		}
		for _, drawer := range drawers {
			bins, err := q.ListBinDrawerAttachmentsByDrawer(ctx, drawer.ID)
			if err != nil {
				return err
			}
			out.Drawers = append(out.Drawers, DrawerLayout{Drawer: drawer, Bins: bins})
		}
		unattached, err := q.ListUnattachedBins(ctx)
		if err != nil {
			return err
		}
		out.UnattachedBins = unattached
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
		seenBins := map[int64]bool{}
		for bi, b := range d.Bins {
			if b.ID <= 0 || seenBins[b.ID] {
				return layoutError("bin %d is missing or duplicated", bi+1)
			}
			seenBins[b.ID] = true
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
	return nil
}

func (s *LayoutService) Save(ctx context.Context, in LayoutInput) error {
	if err := validate(in); err != nil {
		return err
	}
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
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
		if _, err = q.UpdateCabinetLayoutSettings(ctx, db.UpdateCabinetLayoutSettingsParams{Name: strings.TrimSpace(in.Name), CoordinateSystem: strings.TrimSpace(in.CoordinateSystem), DisplayOrder: in.DisplayOrder, LayoutWidth: in.Width, LayoutHeight: in.Height, ID: in.ID}); err != nil {
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
					return fmt.Errorf("%w: bin %d is already attached to a different drawer", ErrUnsafeLayout, b.ID)
				}
				if exists {
					if _, err = q.UpdateBinDrawerAttachment(ctx, db.UpdateBinDrawerAttachmentParams{DrawerID: id, DisplayOrder: b.DisplayOrder, BinID: b.ID}); err != nil {
						return err
					}
					if _, err = q.UpdateBinDrawerAttachmentGeometry(ctx, db.UpdateBinDrawerAttachmentGeometryParams{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height, BinID: b.ID}); err != nil {
						return err
					}
				} else {
					if _, err := q.UpdateBinName(ctx, db.UpdateBinNameParams{Name: strings.TrimSpace(b.Name), ID: b.ID}); err != nil {
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
		var e error
		id, e = q.CreateCabinet(ctx, db.CreateCabinetParams{Name: strings.TrimSpace(name), ControllerID: sql.NullInt64{}, CoordinateSystem: "grid", DisplayOrder: order})
		if e != nil {
			return e
		}
		_, e = q.UpdateCabinetLayoutSettings(ctx, db.UpdateCabinetLayoutSettingsParams{Name: strings.TrimSpace(name), CoordinateSystem: "grid", DisplayOrder: order, LayoutWidth: width, LayoutHeight: height, ID: id})
		return e
	})
	return id, err
}

// ArchiveCabinet preserves every drawer, bin, stock assignment and electrical
// record. It only hides the cabinet from active physical browsing.
func (s *LayoutService) ArchiveCabinet(ctx context.Context, cabinetID, revision int64) error {
	return s.store.ExecImmediateTx(ctx, func(q db.Querier) error {
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
