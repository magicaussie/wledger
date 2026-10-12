package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

// These tests exercise the additive migration
// sql/schema/018_cabinet_controllers.sql, which adds the cabinet_controllers
// membership table and rebuilds drawer_led_allocations so its
// (cabinet_id, controller_id) key references that membership table rather than
// cabinets(id, controller_id).
//
// Every table under test is written through raw SQL, so the tests do not depend
// on any sqlc-generated symbol for the new table. Fixtures always migrate to the
// last legacy version (17) first and capture that baseline before applying the
// new migration, so the assertions compare the migration's effect rather than
// assuming it exists.

// ccLegacyVersion is the last schema version before the cabinet_controllers
// migration.
const ccLegacyVersion int64 = 17

// ccVersion is the goose version assigned to 018_cabinet_controllers.sql.
const ccVersion int64 = 18

// cabinetControllerFixture is one isolated, file-backed SQLite database migrated
// to the last legacy schema version with foreign keys enforced.
type cabinetControllerFixture struct {
	t      *testing.T
	dbConn *sql.DB
	ctx    context.Context
}

// openCabinetControllerFixture opens a temporary file-backed database, enables
// foreign keys (via db.Open's DSN) and migrates it to ccLegacyVersion using the
// embedded Goose schema filesystem.
func openCabinetControllerFixture(t *testing.T, name string) cabinetControllerFixture {
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
	if err := goose.UpTo(dbConn, ".", ccLegacyVersion); err != nil {
		t.Fatalf("migrate fixture to legacy version %d: %v", ccLegacyVersion, err)
	}

	return cabinetControllerFixture{
		t:      t,
		dbConn: dbConn,
		ctx:    context.Background(),
	}
}

// migrateLatest applies the embedded migrations through 018 so these focused
// tests continue to assert the Task 048 schema independently of later migrations.
func (f cabinetControllerFixture) migrateLatest() {
	f.t.Helper()
	if err := goose.UpTo(f.dbConn, ".", ccVersion); err != nil {
		f.t.Fatalf("apply Task 048 migration: %v", err)
	}
}

// downToLegacy rolls the schema back to the last legacy version and returns the
// migration error, if any, rather than failing the test so a refusal can be
// asserted.
func (f cabinetControllerFixture) downToLegacy() error {
	f.t.Helper()
	return goose.DownTo(f.dbConn, ".", ccLegacyVersion)
}

func (f cabinetControllerFixture) mustExec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.dbConn.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

// insert runs one insert, fails the test on error and returns the new row id.
func (f cabinetControllerFixture) insert(query string, args ...any) int64 {
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
func (f cabinetControllerFixture) insertErr(query string, args ...any) error {
	f.t.Helper()
	_, err := f.dbConn.ExecContext(f.ctx, query, args...)
	return err
}

func (f cabinetControllerFixture) count(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.dbConn.QueryRowContext(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func (f cabinetControllerFixture) tableExists(name string) bool {
	f.t.Helper()
	var got string
	err := f.dbConn.QueryRowContext(f.ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&got)
	return err == nil && got == name
}

// foreignKeyTables returns the set of tables referenced by the given table's
// foreign keys, read from the live schema.
func (f cabinetControllerFixture) foreignKeyTables(table string) map[string]bool {
	f.t.Helper()
	rows, err := f.dbConn.QueryContext(f.ctx, "PRAGMA foreign_key_list("+table+")")
	if err != nil {
		f.t.Fatalf("pragma foreign_key_list(%s): %v", table, err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var id, seq int64
		var parent, from string
		var to sql.NullString
		var onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			f.t.Fatalf("scan foreign_key_list(%s): %v", table, err)
		}
		out[parent] = true
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("foreign_key_list(%s): %v", table, err)
	}
	return out
}

// queryRows renders every row of a query as a single deterministic string, so
// before/after snapshots can be compared with reflect.DeepEqual.
func (f cabinetControllerFixture) queryRows(query string) []string {
	f.t.Helper()
	rows, err := f.dbConn.QueryContext(f.ctx, query)
	if err != nil {
		f.t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		f.t.Fatalf("columns for %q: %v", query, err)
	}

	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			f.t.Fatalf("scan %q: %v", query, err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = ccFormat(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		f.t.Fatalf("rows %q: %v", query, err)
	}
	return out
}

func ccFormat(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(x)
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// ccState is a full snapshot of every legacy/inventory row plus the allocation
// rows, used to prove the migration changes nothing it should not.
type ccState struct {
	controllers []string
	cabinets    []string
	drawers     []string
	containers  []string
	bins        []string
	parts       []string
	assignments []string
	attachments []string
	allocations []string
}

func captureCCState(f cabinetControllerFixture) ccState {
	return ccState{
		controllers: f.queryRows(`SELECT id, name, ip_address, port, led_count FROM controllers ORDER BY id`),
		cabinets:    f.queryRows(`SELECT id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived FROM cabinets ORDER BY id`),
		drawers:     f.queryRows(`SELECT id, cabinet_id, label, display_order, x, y, width, height, archived FROM drawers ORDER BY id`),
		containers:  f.queryRows(`SELECT id, name, controller_id, segment_id, led_start, led_count FROM containers ORDER BY id`),
		bins:        f.queryRows(`SELECT id, name, container_id, led_index, width, grid_x, grid_y FROM bins ORDER BY id`),
		parts:       f.queryRows(`SELECT id, name FROM parts ORDER BY id`),
		assignments: f.queryRows(`SELECT id, part_id, bin_id, quantity FROM part_assignments ORDER BY id`),
		attachments: f.queryRows(`SELECT bin_id, drawer_id, x, y, width, height, display_order FROM bin_drawer_attachments ORDER BY bin_id`),
		allocations: f.queryRows(`SELECT id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at FROM drawer_led_allocations ORDER BY id`),
	}
}

const (
	ccAllocationInsert = `INSERT INTO drawer_led_allocations (cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order) VALUES (?, ?, ?, ?, ?, ?)`
	ccMembershipInsert = `INSERT INTO cabinet_controllers (cabinet_id, controller_id, slot) VALUES (?, ?, ?)`
)

// seed populates a representative single-controller installation: two cabinets
// (one owning a controller, one a NULL draft), three drawers, a container with
// two bins, parts with quantities, bin attachments and three allocation ranges
// held against the legacy primary controller with explicit timestamps.
func (f cabinetControllerFixture) seed() {
	f.t.Helper()

	f.mustExec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (1, 'Ctrl Primary', '192.168.1.10', 80, 300)`)
	f.mustExec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (2, 'Ctrl Second', '192.168.1.11', 80, 300)`)
	f.mustExec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (3, 'Ctrl Third', '192.168.1.12', 80, 300)`)
	f.mustExec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (4, 'Ctrl Fourth', '192.168.1.13', 80, 300)`)
	f.mustExec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (5, 'Ctrl Fifth', '192.168.1.14', 80, 300)`)
	f.mustExec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (6, 'Ctrl Sixth', '192.168.1.15', 80, 300)`)

	f.mustExec(`INSERT INTO cabinets (id, name, controller_id) VALUES (1, 'Cabinet One', 1)`)
	f.mustExec(`INSERT INTO cabinets (id, name, controller_id) VALUES (2, 'Cabinet Draft', NULL)`)

	f.mustExec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height) VALUES (1, 1, 'Drawer One', 0, 0.0, 0.0, 10.0, 5.0)`)
	f.mustExec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height) VALUES (2, 1, 'Drawer Two', 1, 10.0, 0.0, 10.0, 5.0)`)
	f.mustExec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height) VALUES (3, 2, 'Drawer Three', 0, 0.0, 0.0, 4.0, 4.0)`)

	f.mustExec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (1, 'String One', 1, 0, 0, 300)`)

	f.mustExec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (1, 'Bin One', 1, 0, 4, 0, 0)`)
	f.mustExec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (2, 'Bin Two', 1, 10, 2, 1, 0)`)

	f.mustExec(`INSERT INTO parts (id, name) VALUES (1, 'Part One')`)
	f.mustExec(`INSERT INTO parts (id, name) VALUES (2, 'Part Two')`)

	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (1, 1, 1, 5)`)
	f.mustExec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (2, 2, 2, 7)`)

	f.mustExec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (1, 1, 0.0, 0.0, 4.0, 2.0, 0)`)
	f.mustExec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (2, 2, 0.0, 0.0, 4.0, 2.0, 0)`)

	f.mustExec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at) VALUES (1, 1, 1, 1, 97, 8, 0, '2024-01-11 12:13:14', '2024-01-11 12:13:14')`)
	f.mustExec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at) VALUES (2, 1, 1, 1, 150, 4, 1, '2024-01-12 13:14:15', '2024-01-12 13:14:15')`)
	f.mustExec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at) VALUES (3, 1, 2, 1, 1, 10, 0, '2024-01-13 14:15:16', '2024-01-13 14:15:16')`)

	// Raise the AUTOINCREMENT high-water mark above the live ids, then remove the
	// probe so reusing a deleted id would be observable after the rebuild.
	f.mustExec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order) VALUES (40, 1, 1, 1, 500, 1, 9)`)
	f.mustExec(`DELETE FROM drawer_led_allocations WHERE id = 40`)
}

// TestCabinetControllersMigration_ForwardPreservesStateAndBackfills applies the
// new migration over populated legacy data and requires the single-controller
// state, every id/quantity and the AUTOINCREMENT high-water mark to survive while
// exactly one membership is backfilled per non-NULL cabinet.controller_id and the
// allocation foreign key moves to the membership table.
func TestCabinetControllersMigration_ForwardPreservesStateAndBackfills(t *testing.T) {
	f := openCabinetControllerFixture(t, "cc_forward")
	f.seed()

	before := captureCCState(f)

	f.migrateLatest()

	if !f.tableExists("cabinet_controllers") {
		t.Fatalf("cabinet_controllers missing after migration %d", ccVersion)
	}

	// Exactly one backfilled membership: slot 0 for the only non-NULL cabinet
	// controller pointer. The NULL draft cabinet receives none.
	if got := f.count(`SELECT COUNT(*) FROM cabinet_controllers`); got != 1 {
		t.Errorf("cabinet_controllers rows = %d, want 1", got)
	}
	var cabinetID, controllerID, slot int64
	if err := f.dbConn.QueryRowContext(f.ctx,
		`SELECT cabinet_id, controller_id, slot FROM cabinet_controllers`).Scan(&cabinetID, &controllerID, &slot); err != nil {
		t.Fatalf("read backfilled membership: %v", err)
	}
	if cabinetID != 1 || controllerID != 1 || slot != 0 {
		t.Errorf("backfilled membership = (cabinet=%d controller=%d slot=%d), want (1,1,0)", cabinetID, controllerID, slot)
	}
	if got := f.count(`SELECT COUNT(*) FROM cabinet_controllers WHERE cabinet_id = 2`); got != 0 {
		t.Errorf("draft cabinet memberships = %d, want 0", got)
	}

	// The legacy/primary pointer keeps its exact values.
	var primary, draft sql.NullInt64
	if err := f.dbConn.QueryRowContext(f.ctx, `SELECT controller_id FROM cabinets WHERE id = 1`).Scan(&primary); err != nil {
		t.Fatalf("read cabinet 1 controller: %v", err)
	}
	if err := f.dbConn.QueryRowContext(f.ctx, `SELECT controller_id FROM cabinets WHERE id = 2`).Scan(&draft); err != nil {
		t.Fatalf("read cabinet 2 controller: %v", err)
	}
	if !primary.Valid || primary.Int64 != 1 {
		t.Errorf("cabinet 1 controller_id = %+v, want 1", primary)
	}
	if draft.Valid {
		t.Errorf("cabinet 2 controller_id = %+v, want NULL", draft)
	}

	// Every legacy/inventory row, allocation field and timestamp is preserved.
	after := captureCCState(f)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("legacy/allocation state changed across migration:\nbefore=%#v\nafter=%#v", before, after)
	}

	// The allocation foreign key now targets the membership table, not cabinets.
	parents := f.foreignKeyTables("drawer_led_allocations")
	if !parents["cabinet_controllers"] {
		t.Errorf("drawer_led_allocations foreign keys = %v, want a cabinet_controllers parent", parents)
	}
	if parents["cabinets"] {
		t.Errorf("drawer_led_allocations still references cabinets: %v", parents)
	}

	// The AUTOINCREMENT high-water mark survives the rebuild.
	if seq := f.count(`SELECT seq FROM sqlite_sequence WHERE name = 'drawer_led_allocations'`); seq < 40 {
		t.Errorf("allocation high-water mark = %d, want >= 40", seq)
	}
	newID := f.insert(ccAllocationInsert, int64(1), int64(1), int64(1), int64(200), int64(3), int64(9))
	if newID <= 40 {
		t.Errorf("new allocation id = %d, want > 40 (high-water mark not preserved)", newID)
	}
}

// TestCabinetControllersMigration_MembershipRules requires a cabinet to accept a
// second controller while rejecting duplicate controller ownership and any sixth
// membership.
func TestCabinetControllersMigration_MembershipRules(t *testing.T) {
	f := openCabinetControllerFixture(t, "cc_membership")
	f.seed()
	f.migrateLatest()

	// The backfilled primary occupies slot 0; four more slots bring the cabinet to
	// the maximum of five memberships.
	for i, ctrl := range []int64{2, 3, 4, 5} {
		f.insert(ccMembershipInsert, int64(1), ctrl, int64(i+1))
	}
	if got := f.count(`SELECT COUNT(*) FROM cabinet_controllers WHERE cabinet_id = 1`); got != 5 {
		t.Fatalf("cabinet 1 memberships = %d, want 5", got)
	}

	// A sixth membership is impossible: slot 5 breaks the bound and reusing an
	// occupied slot breaks per-cabinet uniqueness.
	if err := f.insertErr(ccMembershipInsert, int64(1), int64(6), int64(5)); err == nil {
		t.Errorf("membership with slot 5 was accepted, want slot bound violation")
	}
	if err := f.insertErr(ccMembershipInsert, int64(1), int64(6), int64(0)); err == nil {
		t.Errorf("membership reusing slot 0 was accepted, want uniqueness violation")
	}

	// A controller may be owned by at most one cabinet.
	if err := f.insertErr(ccMembershipInsert, int64(2), int64(1), int64(0)); err == nil {
		t.Errorf("second cabinet claiming controller 1 was accepted, want ownership violation")
	}

	if got := f.count(`SELECT COUNT(*) FROM cabinet_controllers WHERE cabinet_id = 1`); got != 5 {
		t.Errorf("cabinet 1 memberships = %d after rejected inserts, want 5", got)
	}
}

// TestCabinetControllersMigration_AllocationsRequireOwnedMembership requires an
// allocation to reference a (cabinet_id, controller_id) membership that actually
// exists, while the drawer/cabinet composite key remains enforced.
func TestCabinetControllersMigration_AllocationsRequireOwnedMembership(t *testing.T) {
	f := openCabinetControllerFixture(t, "cc_allocations")
	f.seed()
	f.migrateLatest()

	// A controller that is not a member of the cabinet cannot receive ranges.
	if err := f.insertErr(ccAllocationInsert, int64(1), int64(1), int64(2), int64(300), int64(3), int64(2)); err == nil {
		t.Errorf("allocation against non-member controller 2 was accepted")
	}
	// A controller owned by no cabinet cannot receive ranges.
	if err := f.insertErr(ccAllocationInsert, int64(1), int64(1), int64(3), int64(400), int64(3), int64(3)); err == nil {
		t.Errorf("allocation against unowned controller 3 was accepted")
	}
	// A cabinet with no memberships cannot receive ranges even for an owned
	// controller.
	if err := f.insertErr(ccAllocationInsert, int64(2), int64(3), int64(1), int64(500), int64(3), int64(0)); err == nil {
		t.Errorf("allocation against cabinet without memberships was accepted")
	}
	// The composite drawer/cabinet key is still enforced.
	if err := f.insertErr(ccAllocationInsert, int64(1), int64(3), int64(1), int64(600), int64(3), int64(4)); err == nil {
		t.Errorf("allocation pairing drawer 3 with cabinet 1 was accepted")
	}

	// Adding the membership makes the second controller's range valid on a
	// different drawer. A drawer cannot mix controllers even within one cabinet.
	f.insert(ccMembershipInsert, int64(1), int64(2), int64(1))
	drawerFour := f.insert(`INSERT INTO drawers (cabinet_id, label, display_order, x, y, width, height) VALUES (1, 'Drawer Four', 3, 0, 10, 10, 5)`)
	if _, err := f.dbConn.ExecContext(f.ctx, ccAllocationInsert,
		int64(1), drawerFour, int64(2), int64(300), int64(3), int64(0)); err != nil {
		t.Fatalf("allocation against newly owned controller 2 rejected: %v", err)
	}
	if err := f.insertErr(ccAllocationInsert, int64(1), int64(1), int64(2), int64(300), int64(3), int64(2)); err == nil {
		t.Errorf("second controller allocation for a drawer already on controller 1 was accepted")
	}
	// The same numerical range is valid on a different controller, but overlap on
	// the same controller is rejected regardless of drawer.
	if _, err := f.dbConn.ExecContext(f.ctx, ccAllocationInsert,
		int64(1), int64(2), int64(1), int64(300), int64(5), int64(0)); err != nil {
		t.Fatalf("same pixel numbers on separate controllers should be allowed: %v", err)
	}
	if err := f.insertErr(ccAllocationInsert, int64(1), int64(2), int64(1), int64(302), int64(2), int64(1)); err == nil {
		t.Errorf("same-controller overlapping allocation was accepted")
	}
}

// TestCabinetControllersMigration_DownProtectsLossyRollback requires the down
// migration to refuse whenever a secondary membership or any allocation would be
// lost, and to restore the pre-018 allocation foreign key when it is safe.
func TestCabinetControllersMigration_DownProtectsLossyRollback(t *testing.T) {
	t.Run("refuses_secondary_membership", func(t *testing.T) {
		f := openCabinetControllerFixture(t, "cc_down_membership")
		f.seed()
		f.migrateLatest()
		f.insert(ccMembershipInsert, int64(1), int64(2), int64(1))

		if err := f.downToLegacy(); err == nil {
			t.Fatalf("down to legacy version %d succeeded with a secondary membership, want refusal", ccLegacyVersion)
		}
		if !f.tableExists("cabinet_controllers") {
			t.Errorf("cabinet_controllers dropped by a refused down")
		}
		if got := f.count(`SELECT COUNT(*) FROM cabinet_controllers`); got != 2 {
			t.Errorf("memberships = %d after refused down, want 2", got)
		}
		if got := f.count(`SELECT COUNT(*) FROM drawer_led_allocations`); got != 3 {
			t.Errorf("allocations = %d after refused down, want 3", got)
		}
	})

	t.Run("refuses_allocations", func(t *testing.T) {
		f := openCabinetControllerFixture(t, "cc_down_allocations")
		f.seed()
		f.migrateLatest()

		if err := f.downToLegacy(); err == nil {
			t.Fatalf("down to legacy version %d succeeded with allocations present, want refusal", ccLegacyVersion)
		}
		if got := f.count(`SELECT COUNT(*) FROM drawer_led_allocations`); got != 3 {
			t.Errorf("allocations = %d after refused down, want 3", got)
		}
		if !f.tableExists("cabinet_controllers") {
			t.Errorf("cabinet_controllers dropped by a refused down")
		}
	})

	t.Run("succeeds_on_primary_only_and_empty_allocations", func(t *testing.T) {
		f := openCabinetControllerFixture(t, "cc_down_clean")
		f.seed()
		f.mustExec(`DELETE FROM drawer_led_allocations`)

		before := captureCCState(f)
		f.migrateLatest()

		if err := f.downToLegacy(); err != nil {
			t.Fatalf("down to legacy version %d with only the primary membership: %v", ccLegacyVersion, err)
		}
		if f.tableExists("cabinet_controllers") {
			t.Errorf("cabinet_controllers survived a clean down")
		}
		if !f.tableExists("drawer_led_allocations") {
			t.Fatalf("drawer_led_allocations missing after clean down")
		}
		if parents := f.foreignKeyTables("drawer_led_allocations"); !parents["cabinets"] {
			t.Errorf("drawer_led_allocations foreign keys = %v after down, want a cabinets parent", parents)
		}

		after := captureCCState(f)
		if got := f.count(`SELECT COUNT(*) FROM cabinets`); got != 2 {
			t.Errorf("cabinets = %d after clean down, want 2", got)
		}
		if got := f.count(`SELECT COUNT(*) FROM drawers`); got != 3 {
			t.Errorf("drawers = %d after clean down, want 3", got)
		}
		if got := f.count(`SELECT COUNT(*) FROM part_assignments`); got != 2 {
			t.Errorf("assignments = %d after clean down, want 2", got)
		}
		if !reflect.DeepEqual(before.controllers, after.controllers) ||
			!reflect.DeepEqual(before.drawers, after.drawers) ||
			!reflect.DeepEqual(before.containers, after.containers) ||
			!reflect.DeepEqual(before.bins, after.bins) ||
			!reflect.DeepEqual(before.assignments, after.assignments) ||
			!reflect.DeepEqual(before.attachments, after.attachments) {
			t.Errorf("legacy rows changed across clean down:\nbefore=%#v\nafter=%#v", before, after)
		}
		if len(after.allocations) != 0 {
			t.Errorf("allocations after clean down = %v, want none", after.allocations)
		}
	})
}
