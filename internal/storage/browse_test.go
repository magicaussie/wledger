package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/sql/schema"
)

// browseFixture is one isolated, file-backed SQLite database migrated to the
// newest schema (physical storage and nullable bin containers included) with
// foreign keys enforced, plus the read-only browsing service under test.
type browseFixture struct {
	t      *testing.T
	dbConn *sql.DB
	svc    *storage.BrowseService
	ctx    context.Context
}

func openBrowseFixture(t *testing.T, name string) *browseFixture {
	t.Helper()

	dbConn, err := db.Open("file:" + filepath.Join(t.TempDir(), name+".db"))
	if err != nil {
		t.Fatalf("open browse fixture database: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	goose.SetBaseFS(schema.Migrations)
	if err := goose.Up(dbConn, "."); err != nil {
		t.Fatalf("migrate browse fixture: %v", err)
	}

	return &browseFixture{
		t:      t,
		dbConn: dbConn,
		svc:    storage.NewBrowseService(db.NewStore(dbConn)),
		ctx:    context.Background(),
	}
}

func (f *browseFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.dbConn.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *browseFixture) count(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.dbConn.QueryRowContext(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// controller and container give a bin an optional legacy electrical
// association. Attaching a bin physically never requires one.
func (f *browseFixture) controller(id int64) {
	f.t.Helper()
	f.exec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (?, ?, ?, ?, ?)`,
		id, "Controller", "192.168.1.10", 80, 300)
}

func (f *browseFixture) container(id, controllerID int64) {
	f.t.Helper()
	f.exec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (?, ?, ?, ?, ?, ?)`,
		id, "String", controllerID, 0, 0, 300)
}

// cabinet inserts a cabinet. controllerID is nil for a draft cabinet.
func (f *browseFixture) cabinet(id int64, name string, controllerID any, displayOrder, archived int64) {
	f.t.Helper()
	f.exec(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, controllerID, "grid", 0, 0, displayOrder, archived)
}

// drawer inserts a drawer with a 10x5 cabinet-local rectangle.
func (f *browseFixture) drawer(id, cabinetID int64, label string, displayOrder, archived int64) {
	f.t.Helper()
	f.exec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, cabinetID, label, displayOrder, 0.0, 0.0, 10.0, 5.0, archived)
}

// bin inserts a bin. containerID is nil for a bin with no legacy electrical
// association. Geometry columns keep their schema defaults so a test can prove
// physical placement comes from the attachment rather than the bin row.
func (f *browseFixture) bin(id int64, name string, containerID, ledIndex any) {
	f.t.Helper()
	f.exec(`INSERT INTO bins (id, name, container_id, led_index) VALUES (?, ?, ?, ?)`,
		id, name, containerID, ledIndex)
}

func (f *browseFixture) part(id int64, name string, partNumber, imagePath any) {
	f.t.Helper()
	f.exec(`INSERT INTO parts (id, name, part_number, image_path) VALUES (?, ?, ?, ?)`,
		id, name, partNumber, imagePath)
}

func (f *browseFixture) assignment(partID, binID, quantity int64) {
	f.t.Helper()
	f.exec(`INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES (?, ?, ?)`,
		partID, binID, quantity)
}

func (f *browseFixture) attach(binID, drawerID int64, x, y, w, h float64, displayOrder int64) {
	f.t.Helper()
	f.exec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		binID, drawerID, x, y, w, h, displayOrder)
}

type pickerSnapshotHookStore struct {
	db.Store
	afterAttachments func()
}

func (s *pickerSnapshotHookStore) ExecTx(ctx context.Context, fn func(db.Querier) error) error {
	return s.Store.ExecTx(ctx, func(q db.Querier) error {
		return fn(&pickerSnapshotHookQuerier{Querier: q, afterAttachments: s.afterAttachments})
	})
}

type pickerSnapshotHookQuerier struct {
	db.Querier
	afterAttachments func()
}

func (q *pickerSnapshotHookQuerier) ListBinDrawerAttachments(ctx context.Context) ([]db.BinDrawerAttachment, error) {
	rows, err := q.Querier.ListBinDrawerAttachments(ctx)
	if err == nil && q.afterAttachments != nil {
		q.afterAttachments()
	}
	return rows, err
}

func cabinetIDs(cabinets []storage.BrowseCabinet) []int64 {
	ids := make([]int64, 0, len(cabinets))
	for _, c := range cabinets {
		ids = append(ids, c.ID)
	}
	return ids
}

func pickerBinIDs(bins []storage.BinPickerBin) []int64 {
	ids := make([]int64, 0, len(bins))
	for _, bin := range bins {
		ids = append(ids, bin.BinID)
	}
	return ids
}

func drawerIDs(drawers []storage.BrowseDrawer) []int64 {
	ids := make([]int64, 0, len(drawers))
	for _, d := range drawers {
		ids = append(ids, d.ID)
	}
	return ids
}

func binIDs(bins []storage.BrowseBin) []int64 {
	ids := make([]int64, 0, len(bins))
	for _, b := range bins {
		ids = append(ids, b.BinID)
	}
	return ids
}

func assertIDs(t *testing.T, label string, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

// TestBrowse_ListCabinetsHierarchyOrdering requires active cabinets and their
// active drawers to come back in deterministic layout order (display_order then
// id at both levels), with archived rows excluded, regardless of insertion
// order.
func TestBrowse_ListCabinetsHierarchyOrdering(t *testing.T) {
	f := openBrowseFixture(t, "list_hierarchy")

	// Inserted out of layout order; cabinet 5 precedes cabinet 3 so an id
	// tiebreak (not insertion order) decides the display_order 1 pair.
	f.cabinet(1, "Cabinet One", nil, 2, 0)
	f.cabinet(4, "Cabinet Archived", nil, 0, 1)
	f.cabinet(2, "Cabinet Two", nil, 0, 0)
	f.cabinet(5, "Cabinet Five", nil, 1, 0)
	f.cabinet(3, "Cabinet Three", nil, 1, 0)

	// Cabinet 1 drawers, inserted out of layout order, plus an archived drawer
	// and a drawer belonging to a different cabinet.
	f.drawer(10, 1, "Drawer Ten", 2, 0)
	f.drawer(13, 1, "Drawer Archived", 0, 1)
	f.drawer(11, 1, "Drawer Eleven", 0, 0)
	f.drawer(14, 1, "Drawer Fourteen", 1, 0)
	f.drawer(12, 1, "Drawer Twelve", 0, 0)
	f.drawer(20, 2, "Drawer Twenty", 0, 0)

	cabinets, err := f.svc.ListCabinets(f.ctx)
	if err != nil {
		t.Fatalf("ListCabinets: %v", err)
	}
	if cabinets == nil {
		t.Fatal("ListCabinets returned nil, want an empty-capable slice")
	}
	assertIDs(t, "cabinet order", cabinetIDs(cabinets), []int64{2, 3, 5, 1})

	for _, c := range cabinets {
		if c.Archived != 0 {
			t.Errorf("cabinet %d archived = %d, want 0 in the active listing", c.ID, c.Archived)
		}
		if c.Drawers == nil {
			t.Errorf("cabinet %d drawers = nil, want a non-nil slice", c.ID)
		}
	}

	assertIDs(t, "cabinet 1 drawer order", drawerIDs(cabinets[3].Drawers), []int64{11, 12, 14, 10})
	assertIDs(t, "cabinet 2 drawer order", drawerIDs(cabinets[0].Drawers), []int64{20})
	assertIDs(t, "cabinet 3 drawer order", drawerIDs(cabinets[1].Drawers), []int64{})

	for _, d := range cabinets[3].Drawers {
		if d.Archived != 0 {
			t.Errorf("drawer %d archived = %d, want 0 in the active layout", d.ID, d.Archived)
		}
		if d.CabinetID != 1 {
			t.Errorf("drawer %d cabinet = %d, want 1", d.ID, d.CabinetID)
		}
	}

	// One cabinet uses the same read model and the same drawer ordering.
	one, err := f.svc.GetCabinet(f.ctx, 1)
	if err != nil {
		t.Fatalf("GetCabinet: %v", err)
	}
	if one.Name != "Cabinet One" || one.DisplayOrder != 2 || one.Archived != 0 {
		t.Errorf("GetCabinet(1) = %+v, want Cabinet One at order 2, active", one)
	}
	assertIDs(t, "GetCabinet(1) drawer order", drawerIDs(one.Drawers), []int64{11, 12, 14, 10})

	// An archived cabinet is returned with its flag exposed rather than hidden.
	archived, err := f.svc.GetCabinet(f.ctx, 4)
	if err != nil {
		t.Fatalf("GetCabinet(4): %v", err)
	}
	if archived.Archived != 1 {
		t.Errorf("archived cabinet flag = %d, want 1", archived.Archived)
	}
	if archived.Drawers == nil {
		t.Error("archived cabinet drawers = nil, want a non-nil slice")
	}
}

func TestBrowse_BinPickerKeepsInactiveAttachmentsDistinctFromUnplaced(t *testing.T) {
	f := openBrowseFixture(t, "picker_inactive")

	f.cabinet(1, "Archived Cabinet", nil, 0, 1)
	f.drawer(1, 1, "Archived Cabinet Drawer", 0, 0)
	f.cabinet(2, "Active Cabinet", nil, 1, 0)
	f.drawer(2, 2, "Archived Drawer", 0, 1)
	f.bin(1, "Cabinet-attached bin", nil, nil)
	f.bin(2, "Drawer-attached bin", nil, nil)
	f.bin(3, "Actually unplaced bin", nil, nil)
	f.attach(1, 1, 0, 0, 1, 1, 0)
	f.attach(2, 2, 0, 0, 1, 1, 0)

	snapshot, err := f.svc.BinPicker(f.ctx)
	if err != nil {
		t.Fatalf("BinPicker: %v", err)
	}
	assertIDs(t, "inactive bins", pickerBinIDs(snapshot.Inactive), []int64{1, 2})
	assertIDs(t, "unplaced bins", pickerBinIDs(snapshot.Unplaced), []int64{3})
	if len(snapshot.Cabinets) != 1 || len(snapshot.Cabinets[0].Drawers) != 0 {
		t.Fatalf("active cabinet groups = %+v, want one active cabinet with no active drawers", snapshot.Cabinets)
	}
}

func TestBrowse_BinPickerUsesOneConsistentReadSnapshot(t *testing.T) {
	f := openBrowseFixture(t, "picker_snapshot")
	f.cabinet(1, "Cabinet", nil, 0, 0)
	f.drawer(1, 1, "Drawer", 0, 0)
	f.bin(1, "Already placed", nil, nil)
	f.bin(2, "Concurrent attachment", nil, nil)
	f.attach(1, 1, 0, 0, 1, 1, 0)

	store := db.NewStore(f.dbConn)
	writerCommitted := false
	hooked := &pickerSnapshotHookStore{
		Store: store,
		afterAttachments: func() {
			if writerCommitted {
				return
			}
			writerCommitted = true
			if err := store.CreateBinDrawerAttachment(f.ctx, db.CreateBinDrawerAttachmentParams{
				BinID:        2,
				DrawerID:     1,
				X:            1,
				Y:            0,
				Width:        1,
				Height:       1,
				DisplayOrder: 1,
			}); err != nil {
				t.Fatalf("commit concurrent attachment: %v", err)
			}
		},
	}

	svc := storage.NewBrowseService(hooked)
	snapshot, err := svc.BinPicker(f.ctx)
	if err != nil {
		t.Fatalf("BinPicker: %v", err)
	}
	if !writerCommitted {
		t.Fatal("concurrent writer hook did not run")
	}
	assertIDs(t, "snapshot unplaced bins", pickerBinIDs(snapshot.Unplaced), []int64{2})
	if got := binIDs(snapshot.Cabinets[0].Drawers[0].Bins); len(got) != 1 || got[0] != 1 {
		t.Fatalf("snapshot drawer bins = %v, want [1]", got)
	}

	nextSnapshot, err := f.svc.BinPicker(f.ctx)
	if err != nil {
		t.Fatalf("BinPicker after writer commit: %v", err)
	}
	if len(nextSnapshot.Unplaced) != 0 {
		t.Fatalf("next snapshot unplaced bins = %v, want none", pickerBinIDs(nextSnapshot.Unplaced))
	}
	if got := binIDs(nextSnapshot.Cabinets[0].Drawers[0].Bins); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("next snapshot drawer bins = %v, want [1 2]", got)
	}
}

func TestBrowse_DrawerContentsOrderUsesPartIDForNameTies(t *testing.T) {
	f := openBrowseFixture(t, "contents_name_tie")
	f.cabinet(1, "Cabinet", nil, 0, 0)
	f.drawer(1, 1, "Drawer", 0, 0)
	f.bin(1, "Bin", nil, nil)
	f.part(2, "Same name", nil, nil)
	f.part(1, "Same name", nil, nil)
	f.assignment(2, 1, 2)
	f.assignment(1, 1, 1)
	f.attach(1, 1, 0, 0, 1, 1, 0)

	detail, err := f.svc.GetDrawer(f.ctx, 1)
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	if got := detail.Bins[0].Contents; len(got) != 2 || got[0].PartID != 1 || got[1].PartID != 2 {
		t.Fatalf("same-named contents = %+v, want part ids [1 2]", got)
	}
}

// TestBrowse_DrawerPhysicalAttachmentsWithNullContainers requires a drawer to
// expose exactly the bins physically attached to it, in attachment order, with
// the attachment's geometry and the bin's contents. A bin whose legacy
// container_id is NULL is fully visible, and no bin's location is chosen by
// container_id: two bins sharing one container land in different drawers.
func TestBrowse_DrawerPhysicalAttachmentsWithNullContainers(t *testing.T) {
	f := openBrowseFixture(t, "drawer_null_containers")

	f.controller(1)
	f.container(1, 1)
	f.cabinet(1, "Cabinet", int64(1), 0, 0)
	f.drawer(1, 1, "Drawer A", 0, 0)
	f.drawer(2, 1, "Drawer B", 1, 0)

	f.bin(1, "Null Container Bin", nil, nil)
	f.bin(2, "Mapped Bin", int64(1), int64(0))
	f.bin(3, "Second Null Bin", nil, nil)
	f.bin(4, "Other Drawer Bin", int64(1), int64(1))

	f.part(1, "Alpha", "A-1", "/img/alpha.png")
	f.part(2, "Beta", nil, nil)
	f.part(3, "Gamma", nil, nil)
	f.part(4, "Delta", nil, nil)

	f.assignment(1, 1, 5)
	f.assignment(2, 1, 0)
	f.assignment(3, 3, 7)
	f.assignment(4, 4, 3)

	// Bin 2 is attached first and last in display order so the bin_id tiebreak
	// decides the two order-0 entries.
	f.attach(2, 1, 2.0, 0.5, 1.5, 2.5, 1)
	f.attach(1, 1, 0.0, 0.0, 1.0, 1.0, 0)
	f.attach(3, 1, 1.0, 0.0, 1.0, 1.0, 0)
	f.attach(4, 2, 0.0, 0.0, 2.0, 2.0, 0)

	detail, err := f.svc.GetDrawer(f.ctx, 1)
	if err != nil {
		t.Fatalf("GetDrawer(1): %v", err)
	}
	if detail.Drawer.ID != 1 || detail.Drawer.Label != "Drawer A" || detail.Drawer.CabinetID != 1 {
		t.Errorf("drawer = %+v, want Drawer A in cabinet 1", detail.Drawer)
	}
	if detail.Bins == nil {
		t.Fatal("drawer bins = nil, want a non-nil slice")
	}
	assertIDs(t, "drawer 1 bin order", binIDs(detail.Bins), []int64{1, 3, 2})

	wantNames := map[int64]string{1: "Null Container Bin", 2: "Mapped Bin", 3: "Second Null Bin"}
	for _, b := range detail.Bins {
		if b.Name != wantNames[b.BinID] {
			t.Errorf("bin %d name = %q, want %q", b.BinID, b.Name, wantNames[b.BinID])
		}
		if b.Contents == nil {
			t.Errorf("bin %d contents = nil, want a non-nil slice", b.BinID)
		}
	}

	// The mapped bin carries the attachment geometry, not any legacy bin column.
	var mapped storage.BrowseBin
	for _, b := range detail.Bins {
		if b.BinID == 2 {
			mapped = b
		}
	}
	if mapped.X != 2.0 || mapped.Y != 0.5 || mapped.Width != 1.5 || mapped.Height != 2.5 || mapped.DisplayOrder != 1 {
		t.Errorf("bin 2 placement = %+v, want the attachment row (2.0,0.5,1.5,2.5,order 1)", mapped)
	}

	// The NULL-container bin's contents are visible and complete.
	nullBin := detail.Bins[0]
	if len(nullBin.Contents) != 2 {
		t.Fatalf("null-container bin contents = %+v, want two parts", nullBin.Contents)
	}
	if nullBin.Contents[0].Name != "Alpha" || nullBin.Contents[0].Quantity != 5 {
		t.Errorf("contents[0] = %+v, want Alpha with quantity 5", nullBin.Contents[0])
	}
	if nullBin.Contents[1].Name != "Beta" || nullBin.Contents[1].Quantity != 0 {
		t.Errorf("contents[1] = %+v, want Beta with quantity 0", nullBin.Contents[1])
	}

	// Bin 4 shares its legacy container with bin 2 but is physically elsewhere,
	// so it must not appear in drawer 1 and must appear in drawer 2.
	for _, b := range detail.Bins {
		if b.BinID == 4 {
			t.Error("bin 4 appeared in drawer 1; physical location must come from the attachment")
		}
	}
	other, err := f.svc.GetDrawer(f.ctx, 2)
	if err != nil {
		t.Fatalf("GetDrawer(2): %v", err)
	}
	assertIDs(t, "drawer 2 bin order", binIDs(other.Bins), []int64{4})
}

// TestBrowse_DrawerContentsPreserveIdentitiesAndQuantities requires part
// identities, per-location quantities (including zero) and part metadata to
// survive a browse read unchanged, and requires an attached bin with no stock
// to render as an empty, non-nil contents list. It also checks that browsing
// writes nothing.
func TestBrowse_DrawerContentsPreserveIdentitiesAndQuantities(t *testing.T) {
	f := openBrowseFixture(t, "contents_preserved")

	f.cabinet(1, "Cabinet", nil, 0, 0)
	f.drawer(1, 1, "Drawer", 0, 0)
	f.bin(1, "Bin One", nil, nil)
	f.bin(2, "Bin Two", nil, nil)
	f.bin(3, "Empty Bin", nil, nil)
	f.attach(1, 1, 0.0, 0.0, 1.0, 1.0, 0)
	f.attach(2, 1, 1.0, 0.0, 1.0, 1.0, 1)
	f.attach(3, 1, 2.0, 0.0, 1.0, 1.0, 2)

	f.part(1, "Widget", "W-1", "/img/widget.png")
	f.part(2, "Gadget", nil, nil)
	f.assignment(1, 1, 4)
	f.assignment(1, 2, 9)
	f.assignment(2, 2, 0)

	beforeAssignments := f.count(`SELECT COUNT(*) FROM part_assignments`)
	beforeAttachments := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`)
	beforeBins := f.count(`SELECT COUNT(*) FROM bins`)

	detail, err := f.svc.GetDrawer(f.ctx, 1)
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	assertIDs(t, "bin order", binIDs(detail.Bins), []int64{1, 2, 3})

	first := detail.Bins[0].Contents
	if len(first) != 1 {
		t.Fatalf("bin 1 contents = %+v, want one part", first)
	}
	if first[0].PartID != 1 || first[0].Name != "Widget" || first[0].Quantity != 4 {
		t.Errorf("bin 1 content = %+v, want part 1 Widget quantity 4", first[0])
	}
	if first[0].PartNumber.String != "W-1" || !first[0].PartNumber.Valid {
		t.Errorf("part number = %+v, want W-1", first[0].PartNumber)
	}
	if first[0].ImagePath.String != "/img/widget.png" || !first[0].ImagePath.Valid {
		t.Errorf("image path = %+v, want /img/widget.png", first[0].ImagePath)
	}

	// The same part identity appears in two bins with independent quantities,
	// and the zero-quantity assignment is preserved rather than dropped.
	second := detail.Bins[1].Contents
	if len(second) != 2 {
		t.Fatalf("bin 2 contents = %+v, want two parts", second)
	}
	if second[0].Name != "Gadget" || second[0].PartID != 2 || second[0].Quantity != 0 {
		t.Errorf("contents[0] = %+v, want Gadget quantity 0", second[0])
	}
	if second[1].Name != "Widget" || second[1].PartID != 1 || second[1].Quantity != 9 {
		t.Errorf("contents[1] = %+v, want Widget quantity 9", second[1])
	}

	if empty := detail.Bins[2].Contents; empty == nil || len(empty) != 0 {
		t.Errorf("empty bin contents = %+v, want a non-nil empty slice", empty)
	}

	if got := f.count(`SELECT COUNT(*) FROM part_assignments`); got != beforeAssignments {
		t.Errorf("assignments = %d after browsing, want %d (read-only)", got, beforeAssignments)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != beforeAttachments {
		t.Errorf("attachments = %d after browsing, want %d (read-only)", got, beforeAttachments)
	}
	if got := f.count(`SELECT COUNT(*) FROM bins`); got != beforeBins {
		t.Errorf("bins = %d after browsing, want %d (read-only)", got, beforeBins)
	}
}

// TestBrowse_MissingCabinetAndDrawerErrors requires an absent id to be reported
// with the machine-matchable sentinel and no partial read model.
func TestBrowse_MissingCabinetAndDrawerErrors(t *testing.T) {
	f := openBrowseFixture(t, "missing")

	// A populated database: an existing sibling must not satisfy a missing id.
	f.cabinet(1, "Cabinet", nil, 0, 0)
	f.drawer(1, 1, "Drawer", 0, 0)
	f.bin(1, "Bin", nil, nil)
	f.attach(1, 1, 0.0, 0.0, 1.0, 1.0, 0)

	cabinet, err := f.svc.GetCabinet(f.ctx, 999)
	if !errors.Is(err, storage.ErrCabinetNotFound) {
		t.Fatalf("GetCabinet(999) err = %v, want ErrCabinetNotFound", err)
	}
	if cabinet.ID != 0 || cabinet.Drawers != nil {
		t.Errorf("GetCabinet(999) = %+v, want the zero model on error", cabinet)
	}

	drawer, err := f.svc.GetDrawer(f.ctx, 999)
	if !errors.Is(err, storage.ErrDrawerNotFound) {
		t.Fatalf("GetDrawer(999) err = %v, want ErrDrawerNotFound", err)
	}
	if drawer.Drawer.ID != 0 || drawer.Bins != nil {
		t.Errorf("GetDrawer(999) = %+v, want the zero model on error", drawer)
	}

	// The existing rows still read normally.
	if _, err := f.svc.GetCabinet(f.ctx, 1); err != nil {
		t.Fatalf("GetCabinet(1): %v", err)
	}
	if _, err := f.svc.GetDrawer(f.ctx, 1); err != nil {
		t.Fatalf("GetDrawer(1): %v", err)
	}
}

// TestBrowse_EmptyDatabase requires an empty migrated database to list nothing
// while still returning a non-nil slice, and to report both missing reads.
func TestBrowse_EmptyDatabase(t *testing.T) {
	f := openBrowseFixture(t, "empty")

	cabinets, err := f.svc.ListCabinets(f.ctx)
	if err != nil {
		t.Fatalf("ListCabinets: %v", err)
	}
	if cabinets == nil || len(cabinets) != 0 {
		t.Fatalf("ListCabinets on an empty database = %+v, want a non-nil empty slice", cabinets)
	}

	if _, err := f.svc.GetCabinet(f.ctx, 1); !errors.Is(err, storage.ErrCabinetNotFound) {
		t.Fatalf("GetCabinet err = %v, want ErrCabinetNotFound", err)
	}
	if _, err := f.svc.GetDrawer(f.ctx, 1); !errors.Is(err, storage.ErrDrawerNotFound) {
		t.Fatalf("GetDrawer err = %v, want ErrDrawerNotFound", err)
	}
}

// TestBrowse_DanglingAttachmentIsReported requires an attachment that
// references a missing bins row to be reported rather than silently skipped or
// repaired. The dangling row is created with foreign keys disabled on a single
// dedicated connection, then enforcement is restored.
func TestBrowse_DanglingAttachmentIsReported(t *testing.T) {
	f := openBrowseFixture(t, "dangling")

	f.cabinet(1, "Cabinet", nil, 0, 0)
	f.drawer(1, 1, "Drawer", 0, 0)
	f.bin(1, "Bin", nil, nil)
	f.attach(1, 1, 0.0, 0.0, 1.0, 1.0, 0)

	conn, err := f.dbConn.Conn(f.ctx)
	if err != nil {
		t.Fatalf("check out connection: %v", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(f.ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := conn.ExecContext(f.ctx, "DELETE FROM bins WHERE id = ?", int64(1)); err != nil {
		t.Fatalf("delete bin with foreign keys disabled: %v", err)
	}
	if _, err := conn.ExecContext(f.ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("restore foreign keys: %v", err)
	}

	detail, err := f.svc.GetDrawer(f.ctx, 1)
	if !errors.Is(err, storage.ErrBinMissing) {
		t.Fatalf("GetDrawer err = %v, want ErrBinMissing", err)
	}
	if detail.Drawer.ID != 0 || detail.Bins != nil {
		t.Errorf("GetDrawer = %+v, want the zero model on error", detail)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 1 {
		t.Errorf("attachments = %d after the failed read, want the dangling row preserved", got)
	}
}

// TestBrowse_ReadDoesNotTakeWriteReservation requires browsing to be a plain
// read: it must succeed from the last committed snapshot while another
// connection holds the SQLite write reservation, and it must not mutate data.
func TestBrowse_ReadDoesNotTakeWriteReservation(t *testing.T) {
	f := openBrowseFixture(t, "read_snapshot")

	f.cabinet(1, "Cabinet", nil, 0, 0)
	f.drawer(1, 1, "Drawer", 0, 0)
	f.bin(1, "Bin", nil, nil)
	f.attach(1, 1, 0.0, 0.0, 1.0, 1.0, 0)

	conn, err := f.dbConn.Conn(f.ctx)
	if err != nil {
		t.Fatalf("check out connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(f.ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("acquire write reservation: %v", err)
	}

	detail, err := f.svc.GetDrawer(f.ctx, 1)
	if err != nil {
		t.Fatalf("GetDrawer while a write reservation is held: %v", err)
	}
	assertIDs(t, "bin order during held reservation", binIDs(detail.Bins), []int64{1})

	cabinets, err := f.svc.ListCabinets(f.ctx)
	if err != nil {
		t.Fatalf("ListCabinets while a write reservation is held: %v", err)
	}
	assertIDs(t, "cabinet order during held reservation", cabinetIDs(cabinets), []int64{1})

	if _, err := conn.ExecContext(f.ctx, "ROLLBACK"); err != nil {
		t.Fatalf("release write reservation: %v", err)
	}

	if got := f.count(`SELECT COUNT(*) FROM cabinets`); got != 1 {
		t.Errorf("cabinets = %d after browsing, want 1 (read-only)", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 1 {
		t.Errorf("attachments = %d after browsing, want 1 (read-only)", got)
	}
}
