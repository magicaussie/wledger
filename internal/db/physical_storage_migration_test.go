package db_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/sql/schema"
)

// These tests exercise the additive migration
// sql/schema/011_physical_storage.sql, which introduces the cabinets, drawers
// and bin_drawer_attachments tables without touching the legacy schema.
//
// Every table under test is written through raw SQL, so the tests do not depend
// on any sqlc-generated symbol for the new tables. Fixtures always migrate to
// the last legacy version (10) first and capture the M1 compatibility baseline
// before applying the new migration, so the assertions compare the migration's
// effect rather than assuming it exists.

// physicalLegacyVersion is the last schema version before the additive physical
// storage migration.
const physicalLegacyVersion int64 = 10

// physicalStorageVersion is the goose version assigned to
// 011_physical_storage.sql.
const physicalStorageVersion int64 = 11

// Raw insert statements for the new tables. Columns that carry timestamps are
// deliberately omitted so their defaults apply.
const (
	physicalCabinetInsert    = `INSERT INTO cabinets (name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (?, ?, ?, ?, ?, ?, ?)`
	physicalDrawerInsert     = `INSERT INTO drawers (cabinet_id, label, display_order, x, y, width, height, archived) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	physicalAttachmentInsert = `INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (?, ?, ?, ?, ?, ?, ?)`
)

// physicalFixture is one isolated, file-backed SQLite database migrated to the
// last legacy schema version with foreign keys enforced.
type physicalFixture struct {
	t      *testing.T
	dbConn *sql.DB
	store  db.Store
	ctx    context.Context
}

// openPhysicalFixture opens a temporary file-backed database, enables foreign
// keys (via db.Open's DSN) and migrates it to the last legacy version using the
// embedded Goose schema filesystem.
func openPhysicalFixture(t *testing.T, name string) physicalFixture {
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
	if err := goose.UpTo(dbConn, ".", physicalLegacyVersion); err != nil {
		t.Fatalf("migrate fixture to legacy version %d: %v", physicalLegacyVersion, err)
	}

	return physicalFixture{
		t:      t,
		dbConn: dbConn,
		store:  db.NewStore(dbConn),
		ctx:    context.Background(),
	}
}

// migrateLatest applies the embedded migrations up to the newest available
// version, including the additive physical storage migration.
func (f physicalFixture) migrateLatest() {
	f.t.Helper()
	if err := goose.Up(f.dbConn, "."); err != nil {
		f.t.Fatalf("apply latest migrations: %v", err)
	}
}

// downToLegacy rolls the schema back to the last legacy version and returns the
// migration error, if any, rather than failing the test so a refusal can be
// asserted.
func (f physicalFixture) downToLegacy() error {
	f.t.Helper()
	return goose.DownTo(f.dbConn, ".", physicalLegacyVersion)
}

func (f physicalFixture) mustExec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.dbConn.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

// insert runs one insert, fails the test on error and returns the new row id.
func (f physicalFixture) insert(query string, args ...any) int64 {
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

// insertErr runs one insert and returns its error without failing the test.
func (f physicalFixture) insertErr(query string, args ...any) error {
	f.t.Helper()
	_, err := f.dbConn.ExecContext(f.ctx, query, args...)
	return err
}

func (f physicalFixture) count(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.dbConn.QueryRowContext(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (f physicalFixture) tableExists(name string) bool {
	f.t.Helper()
	var got string
	err := f.dbConn.QueryRowContext(f.ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&got)
	return err == nil && got == name
}

// capture reads one M1 compatibility baseline. It performs no writes.
func (f physicalFixture) capture() hardware.CompatibilityBaseline {
	f.t.Helper()
	baseline, err := hardware.CaptureCompatibilityBaseline(f.ctx, f.store)
	if err != nil {
		f.t.Fatalf("capture compatibility baseline: %v", err)
	}
	return baseline
}

// physicalLegacyIDs are the row ids seeded by seedPhysicalLegacyData.
type physicalLegacyIDs struct {
	controllers []int64
	containers  []int64
	bins        []int64
	parts       []int64
	assignments []int64
}

// seedPhysicalLegacyData inserts a legacy graph that covers the compatibility
// cases the migration must preserve: mapped and NULL-width/NULL-index bins, a
// part in multiple locations, a NULL-location assignment, a zero-quantity
// assignment and the coordinate/migration flags.
func seedPhysicalLegacyData(f physicalFixture) physicalLegacyIDs {
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

	// Part 1 in two locations (multi-location), part 2 with a NULL location and
	// a zero-quantity assignment.
	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (1, 1, 1, 5)`)
	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (2, 1, 2, 7)`)
	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (3, 2, NULL, 3)`)
	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (4, 2, 4, 0)`)

	f.mustExec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, ledspace.FlagKey, ledspace.Segment)
	f.mustExec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, hardware.Migration005FlagKey, "true")
	f.mustExec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, hardware.DrawerAllocationBackfilledFlagKey, "true")

	return physicalLegacyIDs{
		controllers: []int64{1, 2},
		containers:  []int64{1, 2},
		bins:        []int64{1, 2, 3, 4},
		parts:       []int64{1, 2},
		assignments: []int64{1, 2, 3, 4},
	}
}

// assertPhysicalFingerprintsEqual requires all three M1 fingerprints to match.
func assertPhysicalFingerprintsEqual(t *testing.T, label string, before, after hardware.CompatibilityBaseline) {
	t.Helper()
	if before.InventoryFingerprint != after.InventoryFingerprint {
		t.Errorf("%s: inventory fingerprint changed: before=%s after=%s",
			label, before.InventoryFingerprint, after.InventoryFingerprint)
	}
	if before.MappingFingerprint != after.MappingFingerprint {
		t.Errorf("%s: mapping fingerprint changed: before=%s after=%s",
			label, before.MappingFingerprint, after.MappingFingerprint)
	}
	if before.TargetsFingerprint != after.TargetsFingerprint {
		t.Errorf("%s: targets fingerprint changed: before=%s after=%s",
			label, before.TargetsFingerprint, after.TargetsFingerprint)
	}
}

// assertPhysicalRawRetained requires the captured legacy rows and flags to be
// byte-for-byte identical between two baselines.
func assertPhysicalRawRetained(t *testing.T, label string, before, after hardware.CompatibilityBaseline) {
	t.Helper()
	if !reflect.DeepEqual(before.Raw, after.Raw) {
		t.Errorf("%s: captured legacy rows changed", label)
	}
	if before.Flags != after.Flags {
		t.Errorf("%s: compatibility flags changed: before=%+v after=%+v", label, before.Flags, after.Flags)
	}
}

// assertPhysicalLegacyRows checks the seeded ids, quantities and SQL NULL
// validity are still present after the migration lifecycle.
func assertPhysicalLegacyRows(t *testing.T, f physicalFixture) {
	t.Helper()

	checks := []struct {
		name  string
		query string
		want  int64
	}{
		{"controllers retained", `SELECT COUNT(*) FROM controllers WHERE id IN (1, 2)`, 2},
		{"containers retained", `SELECT COUNT(*) FROM containers WHERE id IN (1, 2)`, 2},
		{"bins retained", `SELECT COUNT(*) FROM bins WHERE id IN (1, 2, 3, 4)`, 4},
		{"assignments retained", `SELECT COUNT(*) FROM part_assignments WHERE id IN (1, 2, 3, 4)`, 4},
		{"NULL bin led_index retained", `SELECT COUNT(*) FROM bins WHERE id = 3 AND led_index IS NULL`, 1},
		{"NULL bin width retained", `SELECT COUNT(*) FROM bins WHERE id = 4 AND width IS NULL`, 1},
		{"NULL assignment location retained", `SELECT COUNT(*) FROM part_assignments WHERE id = 3 AND bin_id IS NULL`, 1},
		{"zero quantity retained", `SELECT COUNT(*) FROM part_assignments WHERE id = 4 AND quantity = 0`, 1},
	}
	for _, c := range checks {
		if got := f.count(c.query); got != c.want {
			t.Errorf("%s: count=%d, want %d", c.name, got, c.want)
		}
	}

	var qty int64
	if err := f.dbConn.QueryRowContext(f.ctx, `SELECT quantity FROM part_assignments WHERE id = 2`).Scan(&qty); err != nil {
		t.Fatalf("read assignment 2 quantity: %v", err)
	}
	if qty != 7 {
		t.Errorf("assignment 2 quantity = %d, want 7", qty)
	}
}

// TestPhysicalStorageMigration_LegacyPreservedAndNewTablesEmpty applies the new
// migration over a populated legacy database and requires every M1 fingerprint,
// raw row and flag to be unchanged, the new tables to start empty and a repeat
// run to be a no-op.
func TestPhysicalStorageMigration_LegacyPreservedAndNewTablesEmpty(t *testing.T) {
	f := openPhysicalFixture(t, "physical_legacy_preservation")
	seedPhysicalLegacyData(f)

	before := f.capture()
	if f.tableExists("cabinets") {
		t.Fatalf("cabinets table already exists before migration %d is applied", physicalStorageVersion)
	}

	f.migrateLatest()

	// New tables must exist and start empty: no inferred cabinets, no legacy
	// conversion and no 69-row default layout.
	newTables := []string{"cabinets", "drawers", "bin_drawer_attachments"}
	for _, table := range newTables {
		if !f.tableExists(table) {
			t.Fatalf("table %q missing after migration %d", table, physicalStorageVersion)
		}
		if got := f.count("SELECT COUNT(*) FROM " + table); got != 0 {
			t.Errorf("%s has %d rows after migration, want 0 (no backfill/defaults)", table, got)
		}
	}

	after := f.capture()
	assertPhysicalFingerprintsEqual(t, "migration", before, after)
	assertPhysicalRawRetained(t, "migration", before, after)
	assertPhysicalLegacyRows(t, f)

	// Re-running the migration must not add rows or change the fingerprints.
	f.migrateLatest()
	for _, table := range newTables {
		if got := f.count("SELECT COUNT(*) FROM " + table); got != 0 {
			t.Errorf("%s has %d rows after repeat migration, want 0", table, got)
		}
	}
	repeated := f.capture()
	assertPhysicalFingerprintsEqual(t, "repeat migration", before, repeated)
	assertPhysicalRawRetained(t, "repeat migration", after, repeated)

	if got := f.count(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`, physicalStorageVersion); got != 1 {
		t.Errorf("goose recorded version %d %d times, want 1", physicalStorageVersion, got)
	}
}

// TestPhysicalStorageMigration_DownEmptyTablesPreservesLegacy rolls the new,
// empty tables back and requires the down migration to succeed while the legacy
// fingerprints remain unchanged.
func TestPhysicalStorageMigration_DownEmptyTablesPreservesLegacy(t *testing.T) {
	f := openPhysicalFixture(t, "physical_down_empty")
	seedPhysicalLegacyData(f)

	before := f.capture()
	f.migrateLatest()

	if err := f.downToLegacy(); err != nil {
		t.Fatalf("down to legacy version %d with empty new tables: %v", physicalLegacyVersion, err)
	}

	for _, table := range []string{"cabinets", "drawers", "bin_drawer_attachments"} {
		if f.tableExists(table) {
			t.Errorf("table %q survived an empty down migration", table)
		}
	}

	after := f.capture()
	assertPhysicalFingerprintsEqual(t, "empty down", before, after)
	assertPhysicalRawRetained(t, "empty down", before, after)
	assertPhysicalLegacyRows(t, f)
}

// TestPhysicalStorageMigration_DownRejectsPopulatedTables requires the down
// migration to refuse to drop populated new tables and to leave both the new
// rows and the legacy graph intact.
func TestPhysicalStorageMigration_DownRejectsPopulatedTables(t *testing.T) {
	f := openPhysicalFixture(t, "physical_down_populated")
	ids := seedPhysicalLegacyData(f)

	before := f.capture()
	f.migrateLatest()

	cabinet := f.insert(physicalCabinetInsert, "Populated Cabinet", ids.controllers[0], "segment", int64(1), int64(0), int64(0), int64(0))
	drawer := f.insert(physicalDrawerInsert, cabinet, "Populated Drawer", int64(0), 0.0, 0.0, 10.0, 5.0, int64(0))
	f.insert(physicalAttachmentInsert, ids.bins[0], drawer, 0.0, 0.0, 4.0, 2.0, int64(0))

	if err := f.downToLegacy(); err == nil {
		t.Fatalf("down to legacy version %d succeeded with populated physical tables, want refusal", physicalLegacyVersion)
	}

	// The refused down must not discard the rows it refused to drop.
	if got := f.count(`SELECT COUNT(*) FROM cabinets WHERE id = ?`, cabinet); got != 1 {
		t.Errorf("cabinet %d rows = %d after refused down, want 1", cabinet, got)
	}
	if got := f.count(`SELECT COUNT(*) FROM drawers WHERE id = ?`, drawer); got != 1 {
		t.Errorf("drawer %d rows = %d after refused down, want 1", drawer, got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments WHERE bin_id = ?`, ids.bins[0]); got != 1 {
		t.Errorf("attachment for bin %d rows = %d after refused down, want 1", ids.bins[0], got)
	}

	after := f.capture()
	assertPhysicalFingerprintsEqual(t, "refused down", before, after)
	assertPhysicalRawRetained(t, "refused down", before, after)
	assertPhysicalLegacyRows(t, f)
}

// TestPhysicalStorageMigration_ExclusiveControllerOwnership requires a
// controller to be claimed by at most one cabinet while leaving nullable drafts
// unconstrained.
func TestPhysicalStorageMigration_ExclusiveControllerOwnership(t *testing.T) {
	f := openPhysicalFixture(t, "physical_ownership")
	ids := seedPhysicalLegacyData(f)
	f.migrateLatest()

	f.insert(physicalCabinetInsert, "Cabinet A", ids.controllers[0], "segment", int64(1), int64(0), int64(0), int64(0))
	if err := f.insertErr(physicalCabinetInsert, "Cabinet B", ids.controllers[0], "segment", int64(1), int64(0), int64(0), int64(0)); err == nil {
		t.Errorf("second cabinet claiming controller %d was accepted, want unique-ownership violation", ids.controllers[0])
	}

	// NULL controller_id means an unassigned draft; multiple drafts are allowed.
	f.insert(physicalCabinetInsert, "Draft One", nil, "segment", int64(1), int64(0), int64(0), int64(0))
	f.insert(physicalCabinetInsert, "Draft Two", nil, "segment", int64(1), int64(0), int64(0), int64(0))
	if got := f.count(`SELECT COUNT(*) FROM cabinets WHERE controller_id IS NULL`); got != 2 {
		t.Errorf("draft cabinets = %d, want 2", got)
	}
	if got := f.count(`SELECT COUNT(DISTINCT controller_id) FROM cabinets WHERE controller_id IS NOT NULL`); got != 1 {
		t.Errorf("distinct owning controllers = %d, want 1", got)
	}
}

// TestPhysicalStorageMigration_GeometryChecksAndForeignKeys requires the new
// tables to reject invalid geometry and dangling references.
func TestPhysicalStorageMigration_GeometryChecksAndForeignKeys(t *testing.T) {
	f := openPhysicalFixture(t, "physical_constraints")
	ids := seedPhysicalLegacyData(f)
	f.migrateLatest()

	cabinet := f.insert(physicalCabinetInsert, "Constraint Cabinet", ids.controllers[0], "segment", int64(1), int64(0), int64(0), int64(0))
	drawer := f.insert(physicalDrawerInsert, cabinet, "Constraint Drawer", int64(0), 0.0, 0.0, 10.0, 5.0, int64(0))

	rejections := []struct {
		name  string
		query string
		args  []any
	}{
		{"drawer negative x", physicalDrawerInsert, []any{cabinet, "neg x", int64(0), -1.0, 0.0, 10.0, 5.0, int64(0)}},
		{"drawer negative y", physicalDrawerInsert, []any{cabinet, "neg y", int64(0), 0.0, -1.0, 10.0, 5.0, int64(0)}},
		{"drawer zero width", physicalDrawerInsert, []any{cabinet, "zero width", int64(0), 0.0, 0.0, 0.0, 5.0, int64(0)}},
		{"drawer negative height", physicalDrawerInsert, []any{cabinet, "neg height", int64(0), 0.0, 0.0, 10.0, -1.0, int64(0)}},
		{"drawer missing cabinet", physicalDrawerInsert, []any{int64(999999), "orphan drawer", int64(0), 0.0, 0.0, 10.0, 5.0, int64(0)}},
		{"attachment negative x", physicalAttachmentInsert, []any{ids.bins[0], drawer, -1.0, 0.0, 4.0, 2.0, int64(0)}},
		{"attachment negative y", physicalAttachmentInsert, []any{ids.bins[0], drawer, 0.0, -1.0, 4.0, 2.0, int64(0)}},
		{"attachment zero width", physicalAttachmentInsert, []any{ids.bins[0], drawer, 0.0, 0.0, 0.0, 2.0, int64(0)}},
		{"attachment negative height", physicalAttachmentInsert, []any{ids.bins[0], drawer, 0.0, 0.0, 4.0, -1.0, int64(0)}},
		{"attachment missing drawer", physicalAttachmentInsert, []any{ids.bins[0], int64(999999), 0.0, 0.0, 4.0, 2.0, int64(0)}},
		{"attachment missing bin", physicalAttachmentInsert, []any{int64(999999), drawer, 0.0, 0.0, 4.0, 2.0, int64(0)}},
	}
	for _, r := range rejections {
		if err := f.insertErr(r.query, r.args...); err == nil {
			t.Errorf("%s was accepted, want a check/foreign-key violation", r.name)
		}
	}

	// Control: a well-formed attachment is accepted.
	f.insert(physicalAttachmentInsert, ids.bins[0], drawer, 0.0, 0.0, 4.0, 2.0, int64(0))
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments WHERE bin_id = ?`, ids.bins[0]); got != 1 {
		t.Errorf("valid attachment rows = %d, want 1", got)
	}
}

// TestPhysicalStorageMigration_DeletionRestrictionsProtectStock requires that
// deleting any node still referenced by the physical graph is refused and does
// not disturb legacy stock.
func TestPhysicalStorageMigration_DeletionRestrictionsProtectStock(t *testing.T) {
	f := openPhysicalFixture(t, "physical_delete")
	ids := seedPhysicalLegacyData(f)
	f.migrateLatest()

	// A controller used only by a cabinet, so the cabinet's restrictive foreign
	// key is the sole reason its deletion can fail.
	physicalController := f.insert(
		`INSERT INTO controllers (name, ip_address, port, led_count) VALUES (?, ?, 80, 120)`,
		"Physical Only", "192.168.1.50")
	cabinet := f.insert(physicalCabinetInsert, "Delete Cabinet", physicalController, "segment", int64(1), int64(0), int64(0), int64(0))
	drawer := f.insert(physicalDrawerInsert, cabinet, "Delete Drawer", int64(0), 0.0, 0.0, 10.0, 5.0, int64(0))

	// Bin 2 carries part assignment 2 (quantity 7).
	attachedBin := ids.bins[1]
	f.insert(physicalAttachmentInsert, attachedBin, drawer, 0.0, 0.0, 4.0, 2.0, int64(0))

	rejections := []struct {
		name  string
		query string
		args  []any
	}{
		{"controller referenced by cabinet", `DELETE FROM controllers WHERE id = ?`, []any{physicalController}},
		{"cabinet referenced by drawer", `DELETE FROM cabinets WHERE id = ?`, []any{cabinet}},
		{"drawer referenced by attachment", `DELETE FROM drawers WHERE id = ?`, []any{drawer}},
		{"bin referenced by attachment", `DELETE FROM bins WHERE id = ?`, []any{attachedBin}},
	}
	for _, r := range rejections {
		if _, err := f.dbConn.ExecContext(f.ctx, r.query, r.args...); err == nil {
			t.Errorf("delete of %s succeeded, want rejection", r.name)
		} else {
			t.Logf("delete of %s rejected: %v", r.name, err)
		}
	}

	// The refused deletes must leave the reference graph and stock untouched.
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments WHERE bin_id = ?`, attachedBin); got != 1 {
		t.Errorf("attachment rows = %d after refused deletes, want 1", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bins WHERE id = ?`, attachedBin); got != 1 {
		t.Errorf("bin rows = %d after refused deletes, want 1", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM drawers WHERE id = ?`, drawer); got != 1 {
		t.Errorf("drawer rows = %d after refused deletes, want 1", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM cabinets WHERE id = ?`, cabinet); got != 1 {
		t.Errorf("cabinet rows = %d after refused deletes, want 1", got)
	}

	var qty int64
	if err := f.dbConn.QueryRowContext(f.ctx, `SELECT quantity FROM part_assignments WHERE id = 2`).Scan(&qty); err != nil {
		t.Fatalf("read assignment 2 quantity: %v", err)
	}
	if qty != 7 {
		t.Errorf("assignment 2 quantity = %d after refused deletes, want 7", qty)
	}
}

// TestPhysicalStorageMigration_MultipleDrawersKeepLegacyMapping requires several
// independent, variable-sized drawers and attachments to coexist while the
// legacy bin mapping metadata stays unchanged.
func TestPhysicalStorageMigration_MultipleDrawersKeepLegacyMapping(t *testing.T) {
	f := openPhysicalFixture(t, "physical_multiple")
	ids := seedPhysicalLegacyData(f)

	before := f.capture()
	f.migrateLatest()

	cabinet := f.insert(physicalCabinetInsert, "Multi Cabinet", ids.controllers[0], "segment", int64(1), int64(0), int64(0), int64(0))
	small := f.insert(physicalDrawerInsert, cabinet, "Small Drawer", int64(0), 1.5, 2.0, 4.0, 3.0, int64(0))
	large := f.insert(physicalDrawerInsert, cabinet, "Large Drawer", int64(1), 10.0, 0.0, 24.0, 12.0, int64(0))

	f.insert(physicalAttachmentInsert, ids.bins[0], small, 0.5, 0.5, 3.0, 2.0, int64(0))
	f.insert(physicalAttachmentInsert, ids.bins[1], large, 1.0, 1.0, 8.0, 6.0, int64(0))

	// Each drawer keeps its own geometry.
	assertPhysicalDrawerGeometry(t, f, small, 1.5, 2.0, 4.0, 3.0)
	assertPhysicalDrawerGeometry(t, f, large, 10.0, 0.0, 24.0, 12.0)
	if got := f.count(`SELECT COUNT(*) FROM drawers WHERE cabinet_id = ?`, cabinet); got != 2 {
		t.Errorf("drawers in cabinet = %d, want 2", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 2 {
		t.Errorf("attachments = %d, want 2", got)
	}

	// Legacy LED mapping metadata is still bound to the original bin rows.
	var containerID, ledIndex, width int64
	if err := f.dbConn.QueryRowContext(f.ctx,
		`SELECT container_id, led_index, width FROM bins WHERE id = ?`, ids.bins[0]).Scan(&containerID, &ledIndex, &width); err != nil {
		t.Fatalf("read legacy bin %d mapping: %v", ids.bins[0], err)
	}
	if containerID != ids.containers[0] || ledIndex != 0 || width != 4 {
		t.Errorf("legacy bin %d mapping = (container=%d led_index=%d width=%d), want (container=%d led_index=0 width=4)",
			ids.bins[0], containerID, ledIndex, width, ids.containers[0])
	}

	after := f.capture()
	assertPhysicalFingerprintsEqual(t, "physical associations", before, after)
	assertPhysicalRawRetained(t, "physical associations", before, after)
}

// assertPhysicalDrawerGeometry reads a drawer's stored geometry and compares it.
func assertPhysicalDrawerGeometry(t *testing.T, f physicalFixture, drawerID int64, wantX, wantY, wantWidth, wantHeight float64) {
	t.Helper()
	var x, y, width, height float64
	if err := f.dbConn.QueryRowContext(f.ctx,
		`SELECT x, y, width, height FROM drawers WHERE id = ?`, drawerID).Scan(&x, &y, &width, &height); err != nil {
		t.Fatalf("read drawer %d geometry: %v", drawerID, err)
	}
	if x != wantX || y != wantY || width != wantWidth || height != wantHeight {
		t.Errorf("drawer %d geometry = (x=%v y=%v width=%v height=%v), want (x=%v y=%v width=%v height=%v)",
			drawerID, x, y, width, height, wantX, wantY, wantWidth, wantHeight)
	}
}
