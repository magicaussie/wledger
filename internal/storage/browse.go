package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/tuxedocurly/wledger/internal/db"
)

// This file adds the read-only physical browsing surface for the physical
// storage layout. It answers three questions for a handler: which active
// cabinets exist with their drawers, what one cabinet looks like, and what a
// single drawer physically holds.
//
// It is strictly read-only and additive: it reuses the existing cabinets,
// drawers, bin_drawer_attachments, bins and part_assignments queries, adds no
// migration, writes nothing, and never infers or backfills an attachment.
//
// Physical location truth is bin_drawer_attachments (bin_id -> drawer_id). A
// bin's legacy bins.container_id is an optional ELECTRICAL association and is
// never consulted to decide where a bin physically lives: a bin whose
// container_id is NULL is a fully visible physical attachment like any other,
// and a bin's contents are read through its stable bins.id.
//
// Every read runs inside one deferred transaction (Store.ExecTx) so the cabinet
// hierarchy, attachment rows and inventory quantities come from a single
// consistent snapshot. The transaction is a plain read: it takes no write
// reservation, so browsing never blocks a writer.

// Sentinel errors returned by the physical browsing service, so a caller can
// distinguish an absent or broken physical record from an unexpected database
// failure.
var (
	// ErrCabinetNotFound indicates no cabinets row exists for the requested id.
	ErrCabinetNotFound = errors.New("physical cabinet not found")
	// ErrDrawerNotFound indicates no drawers row exists for the requested id.
	ErrDrawerNotFound = errors.New("physical drawer not found")
	// ErrBinMissing indicates a bin_drawer_attachments row references a bin
	// whose bins row does not exist. The dangling reference is reported as an
	// error, never silently skipped and never repaired.
	ErrBinMissing = errors.New("attached bin not found")
)

// BrowseCabinet is one physical cabinet together with its browsable active
// drawers. It is a read model: nothing here is written back or inferred.
type BrowseCabinet struct {
	// ID is the stable cabinet identity.
	ID int64
	// Name is the cabinet's editable label.
	Name string
	// ControllerID is the cabinet's exclusively assigned WLED controller, or
	// invalid when the cabinet has none yet.
	ControllerID sql.NullInt64
	// CoordinateSystem is the cabinet's stored coordinate convention.
	CoordinateSystem string
	// LayoutRevision advances whenever the physical layout changes.
	LayoutRevision int64
	// ElectricalRevision advances whenever the electrical mapping changes.
	ElectricalRevision int64
	// LayoutWidth and LayoutHeight bound cabinet-local drawer geometry.
	LayoutWidth, LayoutHeight float64
	// DisplayOrder orders the cabinet among its siblings.
	DisplayOrder int64
	// Archived is 1 when the cabinet is archived. It is exposed, not hidden:
	// only the "active cabinets" listing filters it out.
	Archived int64
	// Drawers holds the cabinet's active drawers in deterministic layout order
	// (display_order, then id). It is never nil, so an empty cabinet marshals
	// and renders as an empty list rather than a missing one.
	Drawers []BrowseDrawer
}

// BrowseDrawer is one physical drawer's identity, label, ordering and
// cabinet-local geometry. It carries no bin or inventory data; GetDrawer
// returns the drawer together with its physically attached bins.
type BrowseDrawer struct {
	// ID is the stable drawer identity.
	ID int64
	// CabinetID is the owning cabinet's stable identity.
	CabinetID int64
	// Label is the drawer's editable label.
	Label string
	// DisplayOrder orders the drawer within its cabinet.
	DisplayOrder int64
	// X and Y are the drawer's non-negative, cabinet-local origin.
	X, Y float64
	// Width and Height are the drawer's strictly positive cabinet-local size.
	Width, Height float64
	// Archived is 1 when the drawer is archived. It is exposed, not hidden.
	Archived int64
}

// BrowseDrawerDetail is one physical drawer with the bins physically attached
// to it.
type BrowseDrawerDetail struct {
	// Drawer is the drawer itself.
	Drawer BrowseDrawer
	// Bins holds every bin attached to this drawer, in deterministic physical
	// order (display_order, then bin id). It is never nil.
	Bins []BrowseBin
}

// BinPickerSnapshot is the complete physical bin selection view from one
// consistent database snapshot.
type BinPickerSnapshot struct {
	// Cabinets contains active cabinets and drawers with active physical
	// attachments.
	Cabinets []BinPickerCabinet
	// Inactive contains bins with an attachment that is not reachable through
	// an active cabinet/drawer hierarchy.
	Inactive []BinPickerBin
	// Unplaced contains bins with no physical attachment row.
	Unplaced []BinPickerBin
}

// BinPickerCabinet groups active drawers and their attached bins.
type BinPickerCabinet struct {
	ID      int64
	Name    string
	Drawers []BrowseDrawerDetail
}

// BinPickerBin contains the stable identity and label used for bin selection.
type BinPickerBin struct {
	BinID int64
	Name  string
}

// BrowseBin is one stable bin as it physically appears inside a drawer. Its
// placement comes from bin_drawer_attachments, not from any legacy bin column.
type BrowseBin struct {
	// BinID is the stable bin identity. It survives layout, numbering and
	// mapping changes.
	BinID int64
	// Name is the bin's editable label.
	Name string
	// X and Y are the bin's non-negative, drawer-local origin.
	X, Y float64
	// Width and Height are the bin's strictly positive drawer-local size.
	Width, Height float64
	// DisplayOrder orders the bin within its drawer.
	DisplayOrder int64
	// Contents holds the part types stocked in this bin, in deterministic
	// order. It is never nil, so an empty bin renders as an empty list.
	Contents []BrowsePart
}

// BrowsePart is one part type stocked in a bin, carrying the stable part
// identity and the exact recorded quantity for this location.
type BrowsePart struct {
	// PartID is the stable part identity.
	PartID int64
	// Name is the part's display name.
	Name string
	// PartNumber is the manufacturer part number, or invalid when unset.
	PartNumber sql.NullString
	// ImagePath is the stored image reference, or invalid when unset.
	ImagePath sql.NullString
	// Quantity is the exact recorded stock for this part in this bin. A zero
	// quantity is preserved as zero.
	Quantity int64
}

// BrowseService is the read-only physical browsing service. It is stateless
// apart from the store and safe to reuse across calls. It exposes no mutation.
type BrowseService struct {
	store db.Store
}

// NewBrowseService creates a physical browsing service backed by store.
func NewBrowseService(store db.Store) *BrowseService {
	return &BrowseService{store: store}
}

// ListCabinets returns every ACTIVE physical cabinet (archived = 0) with its
// active drawers, ordered deterministically by display_order then id at both
// levels. It reads one consistent snapshot and writes nothing.
func (s *BrowseService) ListCabinets(ctx context.Context) ([]BrowseCabinet, error) {
	cabinets := make([]BrowseCabinet, 0)
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		rows, err := q.ListCabinets(ctx)
		if err != nil {
			return fmt.Errorf("failed to list physical cabinets: %w", err)
		}
		for _, row := range rows {
			drawers, err := activeDrawers(ctx, q, row.ID)
			if err != nil {
				return err
			}
			cabinet := newBrowseCabinet(row)
			cabinet.Drawers = drawers
			cabinets = append(cabinets, cabinet)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return cabinets, nil
}

// GetCabinet returns one physical cabinet with its active drawers. The cabinet
// row is returned whenever it exists, including an archived one, so the caller
// can decide how to present Archived; only a genuinely absent id is an error
// matching ErrCabinetNotFound. The drawer list applies the same active-drawer
// rule as ListCabinets, because archived drawers are not part of the browsable
// layout. It reads one consistent snapshot and writes nothing.
func (s *BrowseService) GetCabinet(ctx context.Context, cabinetID int64) (BrowseCabinet, error) {
	var cabinet BrowseCabinet
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		row, err := q.GetCabinet(ctx, cabinetID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: id %d", ErrCabinetNotFound, cabinetID)
		}
		if err != nil {
			return fmt.Errorf("failed to load physical cabinet %d: %w", cabinetID, err)
		}
		drawers, err := activeDrawers(ctx, q, row.ID)
		if err != nil {
			return err
		}
		cabinet = newBrowseCabinet(row)
		cabinet.Drawers = drawers
		return nil
	})
	if err != nil {
		return BrowseCabinet{}, err
	}
	return cabinet, nil
}

// GetDrawer returns one physical drawer with every bin physically attached to
// it and each bin's inventory contents. The drawer row is returned whenever it
// exists, including an archived one, with Archived exposed; only a genuinely
// absent id is an error matching ErrDrawerNotFound.
//
// Attachment rows are physical truth: the bin set comes exclusively from
// bin_drawer_attachments for this drawer, so a bin whose legacy container_id is
// NULL is visible exactly like any other and no bin is located by container_id.
// Bins are ordered by display_order then bin id, and each bin's contents by part
// name, preserving stable part identities and exact quantities (including zero).
// An attachment that references a missing bins row is reported as ErrBinMissing
// rather than skipped. It reads one consistent snapshot and writes nothing.
func (s *BrowseService) GetDrawer(ctx context.Context, drawerID int64) (BrowseDrawerDetail, error) {
	var detail BrowseDrawerDetail
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		var err error
		detail, err = getDrawer(ctx, q, drawerID)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return BrowseDrawerDetail{}, err
	}
	return detail, nil
}

// BinPicker returns every bin exactly once, partitioned by its recorded
// physical attachment, using one read-only snapshot. Active placements are
// grouped by cabinet and drawer; attachments outside that active hierarchy are
// exposed separately from bins that have no attachment row.
func (s *BrowseService) BinPicker(ctx context.Context) (BinPickerSnapshot, error) {
	snapshot := BinPickerSnapshot{
		Cabinets: make([]BinPickerCabinet, 0),
		Inactive: make([]BinPickerBin, 0),
		Unplaced: make([]BinPickerBin, 0),
	}
	err := s.store.ExecTx(ctx, func(q db.Querier) error {
		attachments, err := q.ListBinDrawerAttachments(ctx)
		if err != nil {
			return fmt.Errorf("failed to list physical bin attachments: %w", err)
		}
		attached := make(map[int64]struct{}, len(attachments))
		for _, attachment := range attachments {
			attached[attachment.BinID] = struct{}{}
		}

		active := make(map[int64]struct{})
		cabinetRows, err := q.ListCabinets(ctx)
		if err != nil {
			return fmt.Errorf("failed to list physical cabinets: %w", err)
		}
		for _, cabinetRow := range cabinetRows {
			drawers, err := activeDrawers(ctx, q, cabinetRow.ID)
			if err != nil {
				return err
			}
			group := BinPickerCabinet{
				ID:      cabinetRow.ID,
				Name:    cabinetRow.Name,
				Drawers: make([]BrowseDrawerDetail, 0, len(drawers)),
			}
			for _, drawer := range drawers {
				detail, err := getDrawer(ctx, q, drawer.ID)
				if err != nil {
					return err
				}
				for _, bin := range detail.Bins {
					active[bin.BinID] = struct{}{}
				}
				group.Drawers = append(group.Drawers, detail)
			}
			snapshot.Cabinets = append(snapshot.Cabinets, group)
		}

		bins, err := q.GetAllBins(ctx)
		if err != nil {
			return fmt.Errorf("failed to list bins: %w", err)
		}
		existing := make(map[int64]struct{}, len(bins))
		for _, bin := range bins {
			existing[bin.ID] = struct{}{}
		}
		for binID := range attached {
			if _, ok := existing[binID]; !ok {
				return fmt.Errorf("%w: bin %d has a physical attachment", ErrBinMissing, binID)
			}
		}

		for _, bin := range bins {
			item := BinPickerBin{BinID: bin.ID, Name: bin.Name}
			if _, ok := active[bin.ID]; ok {
				continue
			}
			if _, ok := attached[bin.ID]; ok {
				snapshot.Inactive = append(snapshot.Inactive, item)
				continue
			}
			snapshot.Unplaced = append(snapshot.Unplaced, item)
		}
		return nil
	})
	if err != nil {
		return BinPickerSnapshot{}, err
	}
	return snapshot, nil
}

func getDrawer(ctx context.Context, q db.Querier, drawerID int64) (BrowseDrawerDetail, error) {
	row, err := q.GetDrawer(ctx, drawerID)
	if errors.Is(err, sql.ErrNoRows) {
		return BrowseDrawerDetail{}, fmt.Errorf("%w: id %d", ErrDrawerNotFound, drawerID)
	}
	if err != nil {
		return BrowseDrawerDetail{}, fmt.Errorf("failed to load physical drawer %d: %w", drawerID, err)
	}

	attachments, err := q.ListBinDrawerAttachmentsByDrawer(ctx, drawerID)
	if err != nil {
		return BrowseDrawerDetail{}, fmt.Errorf("failed to list bins attached to drawer %d: %w", drawerID, err)
	}

	bins := make([]BrowseBin, 0, len(attachments))
	for _, attachment := range attachments {
		bin, err := q.GetBin(ctx, attachment.BinID)
		if errors.Is(err, sql.ErrNoRows) {
			return BrowseDrawerDetail{}, fmt.Errorf("%w: bin %d attached to drawer %d", ErrBinMissing, attachment.BinID, drawerID)
		}
		if err != nil {
			return BrowseDrawerDetail{}, fmt.Errorf("failed to load bin %d: %w", attachment.BinID, err)
		}
		contents, err := q.GetBinContents(ctx, sql.NullInt64{Int64: attachment.BinID, Valid: true})
		if err != nil {
			return BrowseDrawerDetail{}, fmt.Errorf("failed to load contents of bin %d: %w", attachment.BinID, err)
		}
		bins = append(bins, BrowseBin{
			BinID:        attachment.BinID,
			Name:         bin.Name,
			X:            attachment.X,
			Y:            attachment.Y,
			Width:        attachment.Width,
			Height:       attachment.Height,
			DisplayOrder: attachment.DisplayOrder,
			Contents:     newBrowseContents(contents),
		})
	}
	return BrowseDrawerDetail{Drawer: newBrowseDrawer(row), Bins: bins}, nil
}

// activeDrawers reads the cabinet's active drawers in deterministic layout
// order. It is shared by ListCabinets and GetCabinet so both surfaces agree on
// what the browsable layout contains.
func activeDrawers(ctx context.Context, q db.Querier, cabinetID int64) ([]BrowseDrawer, error) {
	rows, err := q.ListDrawersByCabinet(ctx, cabinetID)
	if err != nil {
		return nil, fmt.Errorf("failed to list drawers of cabinet %d: %w", cabinetID, err)
	}
	drawers := make([]BrowseDrawer, 0, len(rows))
	for _, row := range rows {
		drawers = append(drawers, newBrowseDrawer(row))
	}
	return drawers, nil
}

// newBrowseCabinet projects a stored cabinet row onto the read model.
func newBrowseCabinet(row db.Cabinet) BrowseCabinet {
	return BrowseCabinet{
		ID:                 row.ID,
		Name:               row.Name,
		ControllerID:       row.ControllerID,
		CoordinateSystem:   row.CoordinateSystem,
		LayoutRevision:     row.LayoutRevision,
		ElectricalRevision: row.ElectricalRevision,
		LayoutWidth:        row.LayoutWidth,
		LayoutHeight:       row.LayoutHeight,
		DisplayOrder:       row.DisplayOrder,
		Archived:           row.Archived,
		Drawers:            make([]BrowseDrawer, 0),
	}
}

// newBrowseDrawer projects a stored drawer row onto the read model.
func newBrowseDrawer(row db.Drawer) BrowseDrawer {
	return BrowseDrawer{
		ID:           row.ID,
		CabinetID:    row.CabinetID,
		Label:        row.Label,
		DisplayOrder: row.DisplayOrder,
		X:            row.X,
		Y:            row.Y,
		Width:        row.Width,
		Height:       row.Height,
		Archived:     row.Archived,
	}
}

// newBrowseContents projects one bin's stock rows onto the read model, keeping
// the stored part identity and quantity unchanged.
func newBrowseContents(rows []db.GetBinContentsRow) []BrowsePart {
	contents := make([]BrowsePart, 0, len(rows))
	for _, row := range rows {
		contents = append(contents, BrowsePart{
			PartID:     row.ID,
			Name:       row.Name,
			PartNumber: row.PartNumber,
			ImagePath:  row.ImagePath,
			Quantity:   row.Quantity,
		})
	}
	sort.Slice(contents, func(i, j int) bool {
		if contents[i].Name != contents[j].Name {
			return contents[i].Name < contents[j].Name
		}
		return contents[i].PartID < contents[j].PartID
	})
	return contents
}
