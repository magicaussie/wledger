package hardware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// These tests exercise the legacy identity guards in SaveGridInSpace and
// ImportConfig. When a controller's legacy bins carry reconciled physical
// attachments (bin_drawer_attachments), a legacy grid save or an import into that
// controller must be refused before any mutation, with the exact M1 fingerprints,
// stock rows, flags and attachment rows left untouched.
//
// Everything runs against a real file-backed SQLite fixture migrated through the
// physical storage schema; no fake store bypasses the immediate transaction. The
// only wrapper is the standard embedded-Store pattern that injects a single read
// failure while the write transaction and rollback stay real.

// createGridBin inserts a bin with explicit grid coordinates so the legacy grid
// writer's position-based identity matching can find it.
func createGridBin(t *testing.T, store db.Store, ctx context.Context, contID int64, name string, ledIndex, width, x, y int64) int64 {
	t.Helper()
	id, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        name,
		ContainerID: sql.NullInt64{Int64: contID, Valid: true},
		LedIndex:    sql.NullInt64{Int64: ledIndex, Valid: true},
		Width:       sql.NullInt64{Int64: width, Valid: true},
		GridX:       sql.NullInt64{Int64: x, Valid: true},
		GridY:       sql.NullInt64{Int64: y, Valid: true},
	})
	if err != nil {
		t.Fatalf("create grid bin %s: %v", name, err)
	}
	return id
}

// attachBinToDrawer creates a cabinet, a drawer and one bin_drawer_attachments
// row through raw SQL, mimicking a reconciled physical placement.
func attachBinToDrawer(t *testing.T, dbConn *sql.DB, ctx context.Context, controllerID, binID int64) {
	t.Helper()
	res, err := dbConn.ExecContext(ctx,
		`INSERT INTO cabinets (name, controller_id, coordinate_system) VALUES (?, ?, 'segment')`,
		"Cabinet", controllerID)
	if err != nil {
		t.Fatalf("insert cabinet: %v", err)
	}
	cabinetID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("cabinet id: %v", err)
	}
	res, err = dbConn.ExecContext(ctx,
		`INSERT INTO drawers (cabinet_id, label, x, y, width, height) VALUES (?, ?, 0, 0, 10, 5)`,
		cabinetID, "Drawer")
	if err != nil {
		t.Fatalf("insert drawer: %v", err)
	}
	drawerID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("drawer id: %v", err)
	}
	if _, err := dbConn.ExecContext(ctx,
		`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height) VALUES (?, ?, 0, 0, 4, 2)`,
		binID, drawerID); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
}

// guardFixture is one controller with two containers, mapped bins and stocked
// stock. The single attachment (when present) is under the second container, so
// the guard must not stop at the first container.
type guardFixture struct {
	svc          Service
	store        db.Store
	dbConn       *sql.DB
	ctx          context.Context
	controllerID int64
	containerA   int64
	containerB   int64
	binA         int64
	binB         int64
}

// setupGuardFixture builds the shared fixture. When attached is true, the bin in
// the second container is bound to a physical drawer.
func setupGuardFixture(t *testing.T, name string, attached bool) guardFixture {
	t.Helper()
	svc, store, dbConn := setupAllocTest(t, name)
	ctx := context.Background()

	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":8}`, 0, 8)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)

	binA := createGridBin(t, store, ctx, a, "a1", 0, 1, 0, 0)
	binB := createGridBin(t, store, ctx, b, "b1", 10, 1, 0, 0)

	part, err := store.CreatePart(ctx, db.CreatePartParams{Name: "P"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   part,
		BinID:    sql.NullInt64{Int64: binB, Valid: true},
		Quantity: 7,
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	if attached {
		attachBinToDrawer(t, dbConn, ctx, ctrl.ID, binB)
	}

	return guardFixture{
		svc: svc, store: store, dbConn: dbConn, ctx: ctx,
		controllerID: ctrl.ID, containerA: a, containerB: b, binA: binA, binB: binB,
	}
}

// configForFixture serializes the fixture's two containers (by id) so a save
// updates them in place rather than recreating them.
func (f guardFixture) configForFixture() string {
	return fmt.Sprintf(
		`[{"id":%d,"name":"A","segment_id":0,"position_index":0,"led_start":0,"led_count":8,"config":{"type":"linear","total":8}},`+
			`{"id":%d,"name":"B","segment_id":0,"position_index":1,"led_start":10,"led_count":10,"config":{"type":"linear","total":10}}]`,
		f.containerA, f.containerB)
}

// guardBaselines captures the M1 compatibility fingerprints and stock rows, and
// the raw attachment rows, for before/after comparison.
type guardBaselines struct {
	compat      CompatibilityBaseline
	attachments []string
}

func captureGuardBaselines(t *testing.T, f guardFixture) guardBaselines {
	t.Helper()
	compat, err := CaptureCompatibilityBaseline(f.ctx, f.store)
	if err != nil {
		t.Fatalf("capture compatibility baseline: %v", err)
	}
	return guardBaselines{compat: compat, attachments: readGuardAttachments(t, f)}
}

// readGuardAttachments serializes every attachment row exactly, including the
// values a legacy write could reassign.
func readGuardAttachments(t *testing.T, f guardFixture) []string {
	t.Helper()
	rows, err := f.dbConn.QueryContext(f.ctx,
		`SELECT bin_id, drawer_id, x, y, width, height, display_order, CAST(created_at AS TEXT), CAST(updated_at AS TEXT) `+
			`FROM bin_drawer_attachments ORDER BY bin_id`)
	if err != nil {
		t.Fatalf("read attachments: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var binID, drawerID, displayOrder int64
		var x, y, width, height float64
		var created, updated sql.NullString
		if err := rows.Scan(&binID, &drawerID, &x, &y, &width, &height, &displayOrder, &created, &updated); err != nil {
			t.Fatalf("scan attachment row: %v", err)
		}
		out = append(out, fmt.Sprintf("%d|%d|%v|%v|%v|%v|%d|%v|%v",
			binID, drawerID, x, y, width, height, displayOrder, created, updated))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate attachments: %v", err)
	}
	return out
}

// assertGuardUnchanged requires the M1 fingerprints, complete legacy rows, flags
// and attachment rows to be byte-for-byte identical between two baselines.
func assertGuardUnchanged(t *testing.T, label string, before, after guardBaselines) {
	t.Helper()
	if before.compat.InventoryFingerprint != after.compat.InventoryFingerprint {
		t.Errorf("%s: inventory fingerprint changed: before=%s after=%s",
			label, before.compat.InventoryFingerprint, after.compat.InventoryFingerprint)
	}
	if before.compat.MappingFingerprint != after.compat.MappingFingerprint {
		t.Errorf("%s: mapping fingerprint changed: before=%s after=%s",
			label, before.compat.MappingFingerprint, after.compat.MappingFingerprint)
	}
	if before.compat.TargetsFingerprint != after.compat.TargetsFingerprint {
		t.Errorf("%s: targets fingerprint changed: before=%s after=%s",
			label, before.compat.TargetsFingerprint, after.compat.TargetsFingerprint)
	}
	if !reflect.DeepEqual(before.compat.Raw, after.compat.Raw) {
		t.Errorf("%s: captured legacy rows changed", label)
	}
	if before.compat.Flags != after.compat.Flags {
		t.Errorf("%s: compatibility flags changed: before=%+v after=%+v", label, before.compat.Flags, after.compat.Flags)
	}
	if !reflect.DeepEqual(before.attachments, after.attachments) {
		t.Errorf("%s: attachment rows changed:\nbefore=%v\nafter=%v", label, before.attachments, after.attachments)
	}
}

// TestSaveGridReconciledLayoutRefused verifies that a legacy grid save is refused
// with the sentinel when a bin under the controller's legacy containers is
// attached, whether the save would reassign its LED mapping, move its grid
// coordinate or delete it, and that nothing changes.
func TestSaveGridReconciledLayoutRefused(t *testing.T) {
	cases := []struct {
		name     string
		gridData string
	}{
		{
			// Same (x,y) as the stored attached bin, but a different LED index:
			// coordinate identity matching would UpdateBin the reconciled row.
			"reassign led index",
			`[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"a1"},` +
				`{"container_index":1,"x":0,"y":0,"led_index":12,"width":1,"name":"b1"}]`,
		},
		{
			// The attached bin moves: its old position is no longer submitted, so
			// it would be created anew and the reconciled row deleted.
			"move grid coordinate",
			`[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"a1"},` +
				`{"container_index":1,"x":1,"y":0,"led_index":10,"width":1,"name":"b1"}]`,
		},
		{
			// The attached bin disappears from the payload: it would be deleted.
			"remove attached bin",
			`[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"a1"}]`,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupGuardFixture(t, fmt.Sprintf("guard_savegrid_%d", i), true)
			defer f.dbConn.Close()

			before := captureGuardBaselines(t, f)

			_, err := f.svc.SaveGrid(f.ctx, f.controllerID, tc.gridData, f.configForFixture())
			if !errors.Is(err, ErrReconciledLayout) {
				t.Fatalf("SaveGrid error = %v, want ErrReconciledLayout", err)
			}

			after := captureGuardBaselines(t, f)
			assertGuardUnchanged(t, tc.name, before, after)
			if len(after.attachments) != 1 {
				t.Errorf("attachment rows = %d, want 1", len(after.attachments))
			}
		})
	}
}

// TestSaveGridUnreconciledUnaffected verifies the guard does not change valid
// SaveGrid behavior for a controller with no reconciled attachments, and that
// unmapped bins and coordinate flags keep working.
func TestSaveGridUnreconciledUnaffected(t *testing.T) {
	f := setupGuardFixture(t, "guard_savegrid_unreconciled", false)
	defer f.dbConn.Close()

	gridData := `[{"container_index":1,"x":0,"y":0,"led_index":12,"width":1,"name":"b1"}]`
	if _, err := f.svc.SaveGrid(f.ctx, f.controllerID, gridData, f.configForFixture()); err != nil {
		t.Fatalf("SaveGrid on unreconciled controller: %v", err)
	}

	bins, err := f.store.GetBinsByContainer(f.ctx, sql.NullInt64{Int64: f.containerB, Valid: true})
	if err != nil {
		t.Fatalf("get bins: %v", err)
	}
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 12 {
		t.Fatalf("expected bin b1 remapped to led_index 12, got %+v", bins)
	}
	if attachments := readGuardAttachments(t, f); len(attachments) != 0 {
		t.Errorf("unreconciled controller gained %d attachment rows", len(attachments))
	}
}

// importPayload builds a valid hardware config pointing at ip.
func importPayload(ip string) string {
	return fmt.Sprintf(
		`{"version":"1.0","controller":{"name":"Imported","ip_address":%q,"port":80},`+
			`"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":8,"config":{"type":"linear","total":8}}],`+
			`"bins":[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"i1"}]}`,
		ip)
}

// TestImportConfigReconciledLayoutRefused verifies an import that targets the
// address of an existing controller with reconciled attachments is refused with
// the sentinel and creates nothing.
func TestImportConfigReconciledLayoutRefused(t *testing.T) {
	f := setupGuardFixture(t, "guard_import_refused", true)
	defer f.dbConn.Close()

	before := captureGuardBaselines(t, f)
	controllersBefore, err := f.store.GetControllers(f.ctx)
	if err != nil {
		t.Fatalf("get controllers: %v", err)
	}

	// The controller's stored address is 1.1.1.1; point the import at it.
	_, err = f.svc.ImportConfig(f.ctx, "", "1.1.1.1", 0, []byte(importPayload("1.1.1.1")))
	if !errors.Is(err, ErrReconciledLayout) {
		t.Fatalf("ImportConfig error = %v, want ErrReconciledLayout", err)
	}

	controllersAfter, err := f.store.GetControllers(f.ctx)
	if err != nil {
		t.Fatalf("get controllers: %v", err)
	}
	if len(controllersAfter) != len(controllersBefore) {
		t.Fatalf("refused import changed controllers: before=%d after=%d", len(controllersBefore), len(controllersAfter))
	}

	after := captureGuardBaselines(t, f)
	assertGuardUnchanged(t, "import refused", before, after)
}

// TestImportConfigUnreconciledUnaffected verifies a new-controller import is
// allowed when its endpoint is unique, while duplicate endpoints are rejected.
func TestImportConfigUnreconciledUnaffected(t *testing.T) {
	f := setupGuardFixture(t, "guard_import_unreconciled", false)
	defer f.dbConn.Close()

	// A brand-new controller address: an unrelated new-controller import.
	newID, err := f.svc.ImportConfig(f.ctx, "", "9.9.9.9", 0, []byte(importPayload("9.9.9.9")))
	if err != nil {
		t.Fatalf("new-controller import: %v", err)
	}
	if newID == 0 {
		t.Fatal("new-controller import returned id 0")
	}

	// Re-importing the same address and port is rejected even though the first
	// imported controller is not reconciled to physical bins.
	if _, err := f.svc.ImportConfig(f.ctx, "", "9.9.9.9", 0, []byte(importPayload("9.9.9.9"))); !errors.Is(err, ErrDuplicateController) {
		t.Fatalf("duplicate endpoint import error = %v, want ErrDuplicateController", err)
	}

	if space, _ := ledspace.Current(f.ctx, f.store); space != ledspace.Segment {
		t.Errorf("coordinate space = %q after imports, want %q", space, ledspace.Segment)
	}
}

// guardFailStore wraps a real Store and injects one read failure inside the
// immediate transaction, so the guard's error propagation and the transaction's
// rollback are exercised against real behavior.
type guardFailStore struct {
	db.Store
	err error
}

func (g *guardFailStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	return g.Store.ExecImmediateTx(ctx, func(q db.Querier) error {
		return fn(&guardFailQuerier{LEDQuerier: q.(db.LEDQuerier), err: g.err})
	})
}

type guardFailQuerier struct {
	db.LEDQuerier
	err error
}

func (g *guardFailQuerier) GetBinDrawerAttachment(ctx context.Context, binID int64) (db.BinDrawerAttachment, error) {
	return db.BinDrawerAttachment{}, g.err
}

// TestReconciliationGuardQueryFailurePropagates verifies that a failed attachment
// read propagates as a wrapped error and aborts with zero side effects, on both
// the grid-save and import paths, and that the failed transaction leaves the
// coordinate space and all rows untouched.
func TestReconciliationGuardQueryFailurePropagates(t *testing.T) {
	injected := errors.New("injected attachment read failure")

	t.Run("save grid", func(t *testing.T) {
		f := setupGuardFixture(t, "guard_fail_savegrid", true)
		defer f.dbConn.Close()

		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		svc := NewService(&guardFailStore{Store: f.store, err: injected}, wled.NewClient(), logger)

		before := captureGuardBaselines(t, f)

		gridData := `[{"container_index":1,"x":0,"y":0,"led_index":12,"width":1,"name":"b1"}]`
		_, err := svc.SaveGrid(f.ctx, f.controllerID, gridData, f.configForFixture())
		if !errors.Is(err, injected) {
			t.Fatalf("SaveGrid error = %v, want it to wrap the injected failure", err)
		}

		after := captureGuardBaselines(t, f)
		assertGuardUnchanged(t, "save grid query failure", before, after)
	})

	t.Run("import config", func(t *testing.T) {
		f := setupGuardFixture(t, "guard_fail_import", true)
		defer f.dbConn.Close()

		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		svc := NewService(&guardFailStore{Store: f.store, err: injected}, wled.NewClient(), logger)

		before := captureGuardBaselines(t, f)

		_, err := svc.ImportConfig(f.ctx, "", "1.1.1.1", 0, []byte(importPayload("1.1.1.1")))
		if !errors.Is(err, injected) {
			t.Fatalf("ImportConfig error = %v, want it to wrap the injected failure", err)
		}

		after := captureGuardBaselines(t, f)
		assertGuardUnchanged(t, "import config query failure", before, after)
	})
}

// TestImportConfigDuplicateIPGuard verifies that an import targeting an address
// shared by several stored controllers is refused when any matching controller
// carries reconciled attachments, even when the first matching row is
// unattached. Legacy ip_address is not unique, so a single-row lookup could miss
// a reconciled controller with the same address.
func TestImportConfigDuplicateIPGuard(t *testing.T) {
	f := setupGuardFixture(t, "guard_import_duplicate_ip", false)
	defer f.dbConn.Close()

	// The fixture controller already uses address 1.1.1.1 and sorts first ("C").
	// Add a second controller with the same address and a reconciled attachment.
	second, err := f.store.CreateController(f.ctx, db.CreateControllerParams{Name: "Dup", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create duplicate-address controller: %v", err)
	}
	dupContainer := mkContainer(t, f.store, f.ctx, second.ID, "Dup", 0, `{"type":"linear","total":8}`, 0, 8)
	dupBin := createGridBin(t, f.store, f.ctx, dupContainer, "d1", 0, 1, 0, 0)
	attachBinToDrawer(t, f.dbConn, f.ctx, second.ID, dupBin)

	before := captureGuardBaselines(t, f)
	controllersBefore, err := f.store.GetControllers(f.ctx)
	if err != nil {
		t.Fatalf("get controllers: %v", err)
	}

	_, err = f.svc.ImportConfig(f.ctx, "", "1.1.1.1", 0, []byte(importPayload("1.1.1.1")))
	if !errors.Is(err, ErrReconciledLayout) {
		t.Fatalf("ImportConfig error = %v, want ErrReconciledLayout", err)
	}

	controllersAfter, err := f.store.GetControllers(f.ctx)
	if err != nil {
		t.Fatalf("get controllers: %v", err)
	}
	if len(controllersAfter) != len(controllersBefore) {
		t.Fatalf("refused import changed controllers: before=%d after=%d", len(controllersBefore), len(controllersAfter))
	}

	after := captureGuardBaselines(t, f)
	assertGuardUnchanged(t, "duplicate-address import refused", before, after)
}
