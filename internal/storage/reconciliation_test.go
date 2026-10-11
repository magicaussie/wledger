package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/sql/schema"
)

// fixture is one isolated, file-backed SQLite database migrated to the newest
// schema (M3 physical storage included) with foreign keys enforced.
type fixture struct {
	t      *testing.T
	dbConn *sql.DB
	store  db.Store
	svc    *storage.Service
	ctx    context.Context
}

func openFixture(t *testing.T, name string) *fixture {
	t.Helper()

	dbConn, err := db.Open("file:" + filepath.Join(t.TempDir(), name+".db"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	goose.SetBaseFS(schema.Migrations)
	if err := goose.Up(dbConn, "."); err != nil {
		t.Fatalf("migrate fixture: %v", err)
	}

	store := db.NewStore(dbConn)
	return &fixture{
		t:      t,
		dbConn: dbConn,
		store:  store,
		svc:    storage.NewService(store),
		ctx:    context.Background(),
	}
}

// openFixtureWithBusy opens a fixture whose connections use a custom SQLite
// busy timeout, so lock-contention tests need not wait out the 5s default.
func openFixtureWithBusy(t *testing.T, name string, busyMS int) *fixture {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=%d",
		filepath.Join(t.TempDir(), name+".db"), busyMS)
	dbConn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	goose.SetBaseFS(schema.Migrations)
	if err := goose.Up(dbConn, "."); err != nil {
		t.Fatalf("migrate fixture: %v", err)
	}

	store := db.NewStore(dbConn)
	return &fixture{
		t:      t,
		dbConn: dbConn,
		store:  store,
		svc:    storage.NewService(store),
		ctx:    context.Background(),
	}
}

func (f *fixture) mustExec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.dbConn.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *fixture) insert(query string, args ...any) int64 {
	f.t.Helper()
	res, err := f.dbConn.ExecContext(f.ctx, query, args...)
	if err != nil {
		f.t.Fatalf("insert %q: %v", query, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		f.t.Fatalf("last insert id for %q: %v", query, err)
	}
	return id
}

func (f *fixture) count(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.dbConn.QueryRowContext(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (f *fixture) capture() hardware.CompatibilityBaseline {
	f.t.Helper()
	baseline, err := hardware.CaptureCompatibilityBaseline(f.ctx, f.store)
	if err != nil {
		f.t.Fatalf("capture compatibility baseline: %v", err)
	}
	return baseline
}

// storageFixtureIDs are the ids seeded by seedLegacy.
type storageFixtureIDs struct {
	controllers []int64
	containers  []int64
	bins        []int64
	parts       []int64
	assignments []int64
	cabinets    []int64
	drawers     []int64
}

// seedLegacy inserts the legacy electrical graph the reconciliation must never
// touch: two controllers, two containers/strings, four bins (mapped, NULL
// index, NULL width) and part assignments that include a NULL location and a
// zero quantity.
func seedLegacy(f *fixture) storageFixtureIDs {
	f.t.Helper()

	f.mustExec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (1, 'Legacy A', '192.168.1.10', 80, 300)`)
	f.mustExec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (2, 'Legacy B', '192.168.1.11', 80, 150)`)

	f.mustExec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (1, 'String 1', 1, 0, 0, 300)`)
	f.mustExec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (2, 'String 2', 2, 0, 0, 150)`)

	f.mustExec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (1, 'Bin Mapped', 1, 0, 4, 0, 0)`)
	f.mustExec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (2, 'Bin Mapped Second', 1, 10, 2, 1, 0)`)
	f.mustExec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (3, 'Bin Null Index', 1, NULL, 1, 2, 0)`)
	f.mustExec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (4, 'Bin Null Width', 2, 50, NULL, 0, 0)`)

	f.mustExec(`INSERT INTO parts (id, name) VALUES (1, 'Part One')`)
	f.mustExec(`INSERT INTO parts (id, name) VALUES (2, 'Part Two')`)

	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (1, 1, 1, 5)`)
	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (2, 1, 2, 7)`)
	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (3, 2, NULL, 3)`)
	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (4, 2, 4, 0)`)

	f.mustExec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, ledspace.FlagKey, ledspace.Segment)
	f.mustExec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, hardware.Migration005FlagKey, "true")
	f.mustExec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, hardware.DrawerAllocationBackfilledFlagKey, "true")

	return storageFixtureIDs{
		controllers: []int64{1, 2},
		containers:  []int64{1, 2},
		bins:        []int64{1, 2, 3, 4},
		parts:       []int64{1, 2},
		assignments: []int64{1, 2, 3, 4},
	}
}

// physical fixture ids.
const (
	cabinetCtrl1   int64 = 1 // configured cabinet owning controller 1
	cabinetDraft   int64 = 2 // NULL-controller draft cabinet
	cabinetArchive int64 = 3 // archived cabinet

	drawerA             int64 = 1 // cabinetCtrl1, active
	drawerDraft         int64 = 2 // cabinetDraft, active
	drawerArchived      int64 = 3 // cabinetCtrl1, archived drawer
	drawerArchivedCabin int64 = 4 // cabinetArchive, active drawer
)

// seedPhysical creates the cabinets and drawers the candidates attach to.
func seedPhysical(f *fixture) {
	f.t.Helper()

	f.insert(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (?,?,?,?,?,?,?,?)`,
		cabinetCtrl1, "Cabinet Ctrl1", int64(1), "grid", int64(0), int64(0), int64(0), int64(0))
	f.insert(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (?,?,?,?,?,?,?,?)`,
		cabinetDraft, "Cabinet Draft", nil, "grid", int64(0), int64(0), int64(0), int64(0))
	f.insert(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (?,?,?,?,?,?,?,?)`,
		cabinetArchive, "Cabinet Archived", nil, "grid", int64(0), int64(0), int64(0), int64(1))

	f.insert(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (?,?,?,?,?,?,?,?,?)`,
		drawerA, cabinetCtrl1, "Drawer A", int64(0), 0.0, 0.0, 10.0, 5.0, int64(0))
	f.insert(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (?,?,?,?,?,?,?,?,?)`,
		drawerDraft, cabinetDraft, "Drawer Draft", int64(0), 0.0, 0.0, 20.0, 10.0, int64(0))
	f.insert(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (?,?,?,?,?,?,?,?,?)`,
		drawerArchived, cabinetCtrl1, "Drawer Archived", int64(1), 0.0, 0.0, 10.0, 5.0, int64(1))
	f.insert(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (?,?,?,?,?,?,?,?,?)`,
		drawerArchivedCabin, cabinetArchive, "Drawer Archived Cabin", int64(0), 0.0, 0.0, 10.0, 5.0, int64(0))
}

// newFixture seeds the legacy graph and the physical cabinets/drawers.
func newFixture(t *testing.T, name string) *fixture {
	t.Helper()
	f := openFixture(t, name)
	seedLegacy(f)
	seedPhysical(f)
	return f
}

// pendingCandidate is a valid candidate: bin 1 (legacy container 1, controller
// 1) into drawer A (cabinet owning controller 1), reviewed as unattached.
func pendingCandidate() storage.AttachmentCandidate {
	return storage.AttachmentCandidate{
		BinID:        1,
		DrawerID:     drawerA,
		X:            1.25,
		Y:            0.25,
		Width:        3.75,
		Height:       4.5,
		DisplayOrder: 2,
	}
}

func assertFingerprintsEqual(t *testing.T, label string, before, after hardware.CompatibilityBaseline) {
	t.Helper()
	if before.InventoryFingerprint != after.InventoryFingerprint {
		t.Errorf("%s: inventory fingerprint changed", label)
	}
	if before.MappingFingerprint != after.MappingFingerprint {
		t.Errorf("%s: mapping fingerprint changed", label)
	}
	if before.TargetsFingerprint != after.TargetsFingerprint {
		t.Errorf("%s: targets fingerprint changed", label)
	}
	if !reflect.DeepEqual(before.Raw, after.Raw) {
		t.Errorf("%s: captured legacy rows changed", label)
	}
	if before.Flags != after.Flags {
		t.Errorf("%s: compatibility flags changed", label)
	}
}

// TestPreview_ReadOnlyAndNoBackfill requires Preview to classify a valid
// candidate as pending while writing nothing and leaving every M1 fingerprint,
// raw legacy row and attachment count untouched.
func TestPreview_ReadOnlyAndNoBackfill(t *testing.T) {
	f := newFixture(t, "preview_readonly")
	before := f.capture()

	entries, err := f.svc.Preview(f.ctx, []storage.AttachmentCandidate{pendingCandidate()})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Status != storage.StatusPending || entries[0].Reason != storage.ReasonNone {
		t.Fatalf("entry = %+v, want pending/none", entries[0])
	}
	if entries[0].CabinetID != cabinetCtrl1 {
		t.Errorf("cabinet id = %d, want %d", entries[0].CabinetID, cabinetCtrl1)
	}

	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 0 {
		t.Errorf("attachments = %d after Preview, want 0 (no backfill)", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM cabinets`); got != 3 {
		t.Errorf("cabinets = %d after Preview, want 3", got)
	}
	if got := f.count(`SELECT layout_revision FROM cabinets WHERE id = ?`, cabinetCtrl1); got != 0 {
		t.Errorf("layout_revision = %d after Preview, want 0", got)
	}

	assertFingerprintsEqual(t, "preview", before, f.capture())
}

// TestPreview_BlockedReasons requires every missing, ambiguous, invalid, stale
// or occupied case to be reported with the exact machine reason.
func TestPreview_BlockedReasons(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(f *fixture)
		cand    storage.AttachmentCandidate
		want    storage.ReconciliationReason
	}{
		{"bin missing", nil,
			storage.AttachmentCandidate{BinID: 9999, DrawerID: drawerA, Width: 1, Height: 1}, storage.ReasonBinMissing},
		{"drawer missing", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: 9999, Width: 1, Height: 1}, storage.ReasonDrawerMissing},
		{"drawer archived", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerArchived, Width: 1, Height: 1}, storage.ReasonDrawerArchived},
		{"cabinet archived", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerArchivedCabin, Width: 1, Height: 1}, storage.ReasonCabinetArchived},
		{"negative x", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, X: -1, Width: 1, Height: 1}, storage.ReasonGeometryInvalid},
		{"negative y", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Y: -1, Width: 1, Height: 1}, storage.ReasonGeometryInvalid},
		{"zero width", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: 0, Height: 1}, storage.ReasonGeometryInvalid},
		{"negative height", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: 1, Height: -0.5}, storage.ReasonGeometryInvalid},
		{"NaN width", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: nan(), Height: 1}, storage.ReasonGeometryInvalid},
		{"infinite width", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: math.Inf(1), Height: 1}, storage.ReasonGeometryInvalid},
		{"width exceeds drawer", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: 11, Height: 1}, storage.ReasonOutOfBounds},
		{"height exceeds drawer", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: 1, Height: 6}, storage.ReasonOutOfBounds},
		{"x origin overruns drawer", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, X: 9, Width: 3.75, Height: 1}, storage.ReasonOutOfBounds},
		{"y origin overruns drawer", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Y: 4, Width: 1, Height: 4.5}, storage.ReasonOutOfBounds},
		{"cross controller", nil,
			storage.AttachmentCandidate{BinID: 4, DrawerID: drawerA, Width: 1, Height: 1}, storage.ReasonControllerMismatch},
		{"stale review reviewed attached", nil,
			storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: 1, Height: 1,
				ExpectedCurrent: storage.ExpectedAttachment{Attached: true, DrawerID: drawerA}}, storage.ReasonStaleReview},
		{"stale review reviewed detached", func(f *fixture) {
			f.insert(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (1,?,?,?,?,?,?)`,
				drawerA, 1.0, 1.0, 1.0, 1.0, int64(0))
		}, storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: 1, Height: 1}, storage.ReasonStaleReview},
		{"occupied different drawer", func(f *fixture) {
			f.insert(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (1,?,?,?,?,?,?)`,
				drawerDraft, 1.0, 1.0, 1.0, 1.0, int64(0))
		}, storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, Width: 1, Height: 1,
			ExpectedCurrent: storage.ExpectedAttachment{Attached: true, DrawerID: drawerDraft}}, storage.ReasonOccupied},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "preview_blocked_"+sanitize(tc.name))
			if tc.prepare != nil {
				tc.prepare(f)
			}
			entries, err := f.svc.Preview(f.ctx, []storage.AttachmentCandidate{tc.cand})
			if err != nil {
				t.Fatalf("Preview: %v", err)
			}
			if len(entries) != 1 {
				t.Fatalf("entries = %d, want 1", len(entries))
			}
			if entries[0].Status != storage.StatusBlocked {
				t.Fatalf("status = %q, want blocked", entries[0].Status)
			}
			if entries[0].Reason != tc.want {
				t.Fatalf("reason = %q, want %q", entries[0].Reason, tc.want)
			}
		})
	}
}

// TestPreview_DuplicateBinBlocked requires that a batch which names the same bin
// twice is ambiguous and blocks both entries.
func TestPreview_DuplicateBinBlocked(t *testing.T) {
	f := newFixture(t, "preview_duplicate")

	a := pendingCandidate()
	b := pendingCandidate()
	b.DrawerID = drawerDraft
	b.ExpectedCurrent = storage.ExpectedAttachment{}

	entries, err := f.svc.Preview(f.ctx, []storage.AttachmentCandidate{a, b})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	for i, e := range entries {
		if e.Status != storage.StatusBlocked || e.Reason != storage.ReasonDuplicateBin {
			t.Errorf("entry %d = %s/%s, want blocked/duplicate_bin", i, e.Status, e.Reason)
		}
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 0 {
		t.Errorf("attachments = %d, want 0", got)
	}
}

// TestPreview_LegitimateDraft requires a NULL-controller draft cabinet to attach
// a physical bin of any legacy controller without an electrical claim.
func TestPreview_LegitimateDraft(t *testing.T) {
	f := newFixture(t, "preview_draft")

	cand := storage.AttachmentCandidate{BinID: 4, DrawerID: drawerDraft, X: 1, Y: 1, Width: 2, Height: 2}
	entries, err := f.svc.Preview(f.ctx, []storage.AttachmentCandidate{cand})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if entries[0].Status != storage.StatusPending {
		t.Fatalf("status = %q reason = %q, want pending", entries[0].Status, entries[0].Reason)
	}
	if entries[0].CabinetID != cabinetDraft {
		t.Errorf("cabinet id = %d, want %d", entries[0].CabinetID, cabinetDraft)
	}
}

// TestApply_CreatesAndPreservesLegacy requires ApplyReviewed to create exactly
// the reviewed attachment, advance the cabinet layout revision once, and leave
// every legacy row, fingerprint and stock quantity (including zero) intact.
func TestApply_CreatesAndPreservesLegacy(t *testing.T) {
	f := newFixture(t, "apply_success")
	before := f.capture()

	cand := pendingCandidate()
	res, err := f.svc.ApplyReviewed(f.ctx, []storage.AttachmentCandidate{cand})
	if err != nil {
		t.Fatalf("ApplyReviewed: %v", err)
	}
	if res.Created != 1 || res.AlreadyAttached != 0 {
		t.Fatalf("created=%d already=%d, want 1/0", res.Created, res.AlreadyAttached)
	}

	drawerID, x, y, w, h, order := f.attachment(cand.BinID)
	if drawerID != drawerA || x != cand.X || y != cand.Y || w != cand.Width || h != cand.Height || order != cand.DisplayOrder {
		t.Fatalf("stored attachment = (drawer=%d x=%v y=%v w=%v h=%v order=%d), want candidate values", drawerID, x, y, w, h, order)
	}

	// Physical geometry is independent of the legacy bin columns: bin 1 keeps
	// width 4, grid_x 0, grid_y 0 even though its attachment is 3.75x4.5 at (1.25,2.5).
	if got := f.count(`SELECT width FROM bins WHERE id = 1`); got != 4 {
		t.Errorf("legacy bin width = %d, want 4 (unchanged)", got)
	}
	if got := f.count(`SELECT grid_x FROM bins WHERE id = 1`); got != 0 {
		t.Errorf("legacy grid_x = %d, want 0 (unchanged)", got)
	}

	if got := f.count(`SELECT layout_revision FROM cabinets WHERE id = ?`, cabinetCtrl1); got != 1 {
		t.Errorf("layout_revision = %d, want 1", got)
	}
	if got := f.count(`SELECT layout_revision FROM cabinets WHERE id = ?`, cabinetDraft); got != 0 {
		t.Errorf("unaffected cabinet layout_revision = %d, want 0", got)
	}

	// Stock quantities, including the zero-quantity assignment, are untouched.
	if got := f.count(`SELECT quantity FROM part_assignments WHERE id = 4`); got != 0 {
		t.Errorf("zero-quantity assignment = %d, want 0", got)
	}
	if got := f.count(`SELECT quantity FROM part_assignments WHERE id = 2`); got != 7 {
		t.Errorf("assignment 2 quantity = %d, want 7", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM part_assignments`); got != 4 {
		t.Errorf("assignments = %d, want 4", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bins`); got != 4 {
		t.Errorf("bins = %d, want 4", got)
	}

	assertFingerprintsEqual(t, "apply success", before, f.capture())
}

// TestApply_IdempotentAlreadyMatching requires replaying the very same reviewed
// candidate (originally reviewed as unattached) to be an explicit
// already_attached no-op that leaves ids, geometry and timestamps unchanged and
// does not bump the layout revision again.
func TestApply_IdempotentAlreadyMatching(t *testing.T) {
	f := newFixture(t, "apply_idempotent")

	candidate := pendingCandidate()
	if _, err := f.svc.ApplyReviewed(f.ctx, []storage.AttachmentCandidate{candidate}); err != nil {
		t.Fatalf("first ApplyReviewed: %v", err)
	}
	stampBefore := f.attachmentStamp(1)
	revBefore := f.count(`SELECT layout_revision FROM cabinets WHERE id = ?`, cabinetCtrl1)

	entries, err := f.svc.Preview(f.ctx, []storage.AttachmentCandidate{candidate})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if entries[0].Status != storage.StatusAlreadyAttached {
		t.Fatalf("status = %q, want already_attached", entries[0].Status)
	}

	res, err := f.svc.ApplyReviewed(f.ctx, []storage.AttachmentCandidate{candidate})
	if err != nil {
		t.Fatalf("second ApplyReviewed: %v", err)
	}
	if res.Created != 0 || res.AlreadyAttached != 1 {
		t.Fatalf("created=%d already=%d, want 0/1", res.Created, res.AlreadyAttached)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 1 {
		t.Errorf("attachments = %d, want 1", got)
	}
	if got := f.attachmentStamp(1); got != stampBefore {
		t.Errorf("attachment timestamps changed: before=%q after=%q", stampBefore, got)
	}
	if got := f.count(`SELECT layout_revision FROM cabinets WHERE id = ?`, cabinetCtrl1); got != revBefore {
		t.Errorf("layout_revision = %d, want %d (unchanged)", got, revBefore)
	}
}

// TestPreview_ExactEdgeWithinDrawer requires a rectangle that exactly fills the
// target drawer (including a boundary origin) to be accepted as pending.
func TestPreview_ExactEdgeWithinDrawer(t *testing.T) {
	f := newFixture(t, "preview_exact_edge")

	// Drawer A is 10x5; this rectangle touches every edge exactly.
	cand := storage.AttachmentCandidate{BinID: 1, DrawerID: drawerA, X: 6.25, Y: 0.5, Width: 3.75, Height: 4.5}
	entries, err := f.svc.Preview(f.ctx, []storage.AttachmentCandidate{cand})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if entries[0].Status != storage.StatusPending {
		t.Fatalf("status = %q reason = %q, want pending", entries[0].Status, entries[0].Reason)
	}
}

// TestApply_RevisionFailureZeroResult forces the cabinet layout-revision bump to
// fail (via a real trigger) after the attachment insert, and requires the whole
// transaction to roll back: a zero ApplyResult and an exactly unchanged
// database, attachments and fingerprints.
func TestApply_RevisionFailureZeroResult(t *testing.T) {
	f := newFixture(t, "apply_revision_failure")
	before := f.capture()

	f.mustExec(`CREATE TRIGGER fail_layout_revision BEFORE UPDATE OF layout_revision ON cabinets
		BEGIN SELECT RAISE(ABORT, 'injected revision failure'); END`)

	res, err := f.svc.ApplyReviewed(f.ctx, []storage.AttachmentCandidate{pendingCandidate()})
	if err == nil {
		t.Fatal("ApplyReviewed succeeded despite the injected revision failure, want error")
	}
	if !reflect.DeepEqual(res, storage.ApplyResult{}) {
		t.Fatalf("result = %+v, want zero ApplyResult on error", res)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 0 {
		t.Errorf("attachments = %d after rollback, want 0", got)
	}
	if got := f.count(`SELECT layout_revision FROM cabinets WHERE id = ?`, cabinetCtrl1); got != 0 {
		t.Errorf("layout_revision = %d after rollback, want 0", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM cabinets`); got != 3 {
		t.Errorf("cabinets = %d after rollback, want 3", got)
	}

	assertFingerprintsEqual(t, "revision failure rollback", before, f.capture())
}

// TestApply_WholeBatchRollback requires a single blocked candidate to refuse the
// entire batch and roll back every pending write, leaving no partial state.
func TestApply_WholeBatchRollback(t *testing.T) {
	f := newFixture(t, "apply_rollback")

	// The occupied bin: attached to drawerDraft, reviewed as such, then proposed
	// into drawerA, which is a refusal.
	f.insert(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (2,?,?,?,?,?,?)`,
		drawerDraft, 1.0, 1.0, 1.0, 1.0, int64(0))
	occupied := storage.AttachmentCandidate{BinID: 2, DrawerID: drawerA, Width: 1, Height: 1,
		ExpectedCurrent: storage.ExpectedAttachment{Attached: true, DrawerID: drawerDraft}}

	res, err := f.svc.ApplyReviewed(f.ctx, []storage.AttachmentCandidate{pendingCandidate(), occupied})
	if err == nil {
		t.Fatal("ApplyReviewed succeeded with a blocked candidate, want refusal")
	}
	if !errors.Is(err, storage.ErrReconciliationBlocked) {
		t.Fatalf("err = %v, want ErrReconciliationBlocked", err)
	}
	var blocked *storage.BatchBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want *BatchBlockedError", err)
	}
	if len(blocked.Entries) != 1 || blocked.Entries[0].Reason != storage.ReasonOccupied {
		t.Fatalf("blocked entries = %+v, want one occupied", blocked.Entries)
	}

	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 1 {
		t.Errorf("attachments = %d after rollback, want 1 (only the pre-existing)", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments WHERE bin_id = 1`); got != 0 {
		t.Errorf("bin 1 attachment rows = %d after rollback, want 0", got)
	}
	if got := f.count(`SELECT layout_revision FROM cabinets WHERE id = ?`, cabinetCtrl1); got != 0 {
		t.Errorf("layout_revision = %d after rollback, want 0", got)
	}
	if res.Created != 0 {
		t.Errorf("created = %d after rollback, want 0", res.Created)
	}
}

// TestApply_StaleReviewRejected requires a candidate whose reviewed current
// state no longer matches to be refused before any mutation.
func TestApply_StaleReviewRejected(t *testing.T) {
	f := newFixture(t, "apply_stale")

	stale := pendingCandidate()
	stale.ExpectedCurrent = storage.ExpectedAttachment{Attached: true, DrawerID: drawerA}

	_, err := f.svc.ApplyReviewed(f.ctx, []storage.AttachmentCandidate{stale})
	if !errors.Is(err, storage.ErrReconciliationBlocked) {
		t.Fatalf("err = %v, want ErrReconciliationBlocked", err)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 0 {
		t.Errorf("attachments = %d after stale refusal, want 0", got)
	}
}

// TestApplyReviewed_WriteReservationSerializes proves the apply takes a write
// reservation: while another connection holds BEGIN IMMEDIATE, an apply bounded
// by a short context fails without mutating, and the same apply succeeds once
// the reservation is released. The verdict is driven by a genuinely held lock,
// not by timing.
func TestApplyReviewed_WriteReservationSerializes(t *testing.T) {
	f := openFixtureWithBusy(t, "write_reservation", 250)
	seedLegacy(f)
	seedPhysical(f)
	cand := pendingCandidate()

	conn, err := f.dbConn.Conn(f.ctx)
	if err != nil {
		t.Fatalf("check out connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(f.ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("acquire write reservation: %v", err)
	}

	shortCtx, cancel := context.WithTimeout(f.ctx, 2*time.Second)
	defer cancel()
	if _, err := f.svc.ApplyReviewed(shortCtx, []storage.AttachmentCandidate{cand}); err == nil {
		t.Fatal("ApplyReviewed succeeded while another connection held the write reservation")
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 0 {
		t.Fatalf("attachments = %d while reservation held, want 0", got)
	}

	if _, err := conn.ExecContext(f.ctx, "ROLLBACK"); err != nil {
		t.Fatalf("release write reservation: %v", err)
	}

	res, err := f.svc.ApplyReviewed(f.ctx, []storage.AttachmentCandidate{cand})
	if err != nil {
		t.Fatalf("ApplyReviewed after release: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("created = %d, want 1", res.Created)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 1 {
		t.Errorf("attachments = %d, want 1", got)
	}
}

// attachment reads the stored geometry of a bin's attachment.
func (f *fixture) attachment(binID int64) (drawerID int64, x, y, w, h float64, order int64) {
	f.t.Helper()
	err := f.dbConn.QueryRowContext(f.ctx,
		`SELECT drawer_id, x, y, width, height, display_order FROM bin_drawer_attachments WHERE bin_id = ?`,
		binID).Scan(&drawerID, &x, &y, &w, &h, &order)
	if err != nil {
		f.t.Fatalf("read attachment for bin %d: %v", binID, err)
	}
	return drawerID, x, y, w, h, order
}

// attachmentStamp returns the created_at/updated_at pair of a bin's attachment.
func (f *fixture) attachmentStamp(binID int64) string {
	f.t.Helper()
	var created, updated string
	if err := f.dbConn.QueryRowContext(f.ctx,
		`SELECT created_at, updated_at FROM bin_drawer_attachments WHERE bin_id = ?`, binID).
		Scan(&created, &updated); err != nil {
		f.t.Fatalf("read attachment timestamps for bin %d: %v", binID, err)
	}
	return created + "|" + updated
}

func nan() float64 {
	var zero float64
	return zero / zero
}

// sanitize turns a subtest name into a filesystem-safe database name.
func sanitize(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r == ' ' {
			out = append(out, '_')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
