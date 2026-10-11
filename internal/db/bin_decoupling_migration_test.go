package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

// Migration tests for sql/schema/012_bin_container_nullable.sql, the M5 step
// that makes bins.container_id optional.
//
// Each fixture migrates to the last version before 012, seeds a legacy graph
// with raw SQL - mapped and unmapped bins, multi-location, zero quantity and
// NULL location assignments, a drawer attachment and the compatibility flags -
// and then applies or rolls back 012, so the assertions compare the migration's
// effect instead of assuming it exists. Bins rows are read with raw SQL so these
// tests do not depend on generated model types, and the fixture imports no other
// package. No production database, hardware or Locate path is touched.

const (
	binNullableLegacyVersion int64 = 11
	binNullableVersion       int64 = 12
)

// Flag keys the fixture seeds. They mirror ledspace.FlagKey and the migration /
// allocation flag keys in internal/hardware, spelled out here so this migration
// test needs no other package for its fixture.
const (
	binNullableSpaceFlag     = "led_coordinate_space"
	binNullableMigrationFlag = "migration_005_applied"
	binNullableBackfillFlag  = "drawer_allocation_backfilled"
)

// binNullableFixture is one isolated, file-backed SQLite database migrated to
// the last schema version before the nullable-container migration.
type binNullableFixture struct {
	t      *testing.T
	dbConn *sql.DB
	path   string
	ctx    context.Context
}

func openBinNullableFixture(t *testing.T, name string) binNullableFixture {
	t.Helper()

	path := filepath.Join(t.TempDir(), name+".db")
	dbConn, err := db.Open("file:" + path)
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })

	// Pinning the pool to a single connection keeps the migration and every
	// assertion on the same connection, so the pragma effects of the rebuild are
	// deterministic instead of depending on which pooled connection is handed out.
	dbConn.SetMaxOpenConns(1)

	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	goose.SetBaseFS(schema.Migrations)
	if err := goose.UpTo(dbConn, ".", binNullableLegacyVersion); err != nil {
		t.Fatalf("migrate fixture to version %d: %v", binNullableLegacyVersion, err)
	}

	return binNullableFixture{t: t, dbConn: dbConn, path: path, ctx: context.Background()}
}

func (f binNullableFixture) migrateToNullable() {
	f.t.Helper()
	if err := goose.Up(f.dbConn, "."); err != nil {
		f.t.Fatalf("apply migration %d: %v", binNullableVersion, err)
	}
}

// rollbackToLegacy returns the migration error instead of failing the test, so a
// refusal can be asserted.
func (f binNullableFixture) rollbackToLegacy() error {
	f.t.Helper()
	return goose.DownTo(f.dbConn, ".", binNullableLegacyVersion)
}

func (f binNullableFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.dbConn.ExecContext(f.ctx, query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

// execErr runs one statement and returns its error without failing the test.
func (f binNullableFixture) execErr(query string, args ...any) error {
	f.t.Helper()
	_, err := f.dbConn.ExecContext(f.ctx, query, args...)
	return err
}

// execScript runs several statements in one Exec, which the SQLite driver
// executes on a single connection: a pragma, an insert and its restore cannot be
// split across pooled connections.
func (f binNullableFixture) execScript(script string) {
	f.t.Helper()
	if _, err := f.dbConn.ExecContext(f.ctx, script); err != nil {
		f.t.Fatalf("exec script %q: %v", script, err)
	}
}

func (f binNullableFixture) insert(query string, args ...any) int64 {
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

func (f binNullableFixture) count(query string, args ...any) int64 {
	f.t.Helper()
	var n int64
	if err := f.dbConn.QueryRowContext(f.ctx, query, args...).Scan(&n); err != nil {
		f.t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func tableExists(t *testing.T, conn *sql.DB, name string) bool {
	t.Helper()
	var got string
	err := conn.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&got)
	return err == nil && got == name
}

// binsColumnShape renders one entry per bins column: name, declared type, NOT
// NULL flag, default and primary-key position.
func binsColumnShape(t *testing.T, conn *sql.DB) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, '<none>'), pk FROM pragma_table_info('bins') ORDER BY cid`)
	if err != nil {
		t.Fatalf("read bins columns: %v", err)
	}
	defer rows.Close()

	var shape []string
	for rows.Next() {
		var name, typ, dflt string
		var notNull, pk int
		if err := rows.Scan(&name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan bins column: %v", err)
		}
		shape = append(shape, fmt.Sprintf("%s|%s|notnull=%d|default=%s|pk=%d", name, typ, notNull, dflt, pk))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read bins columns: %v", err)
	}
	return shape
}

// binContainerNotNull reports the NOT NULL flag of bins.container_id: 1 while
// the pre-M5 schema holds, 0 once the column is optional.
func binContainerNotNull(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var notNull int
	if err := conn.QueryRow(`SELECT "notnull" FROM pragma_table_info('bins') WHERE name = 'container_id'`).Scan(&notNull); err != nil {
		t.Fatalf("read bins.container_id nullability: %v", err)
	}
	return notNull
}

// binsIndexShape renders every index on bins with its unique flag, origin and
// indexed columns, so a lost, extra or reshaped index is visible.
func binsIndexShape(t *testing.T, conn *sql.DB) []string {
	t.Helper()

	type indexEntry struct {
		name   string
		unique int
		origin string
	}
	var entries []indexEntry
	rows, err := conn.Query(`SELECT name, "unique", origin FROM pragma_index_list('bins')`)
	if err != nil {
		t.Fatalf("read bins indexes: %v", err)
	}
	for rows.Next() {
		var e indexEntry
		if err := rows.Scan(&e.name, &e.unique, &e.origin); err != nil {
			rows.Close()
			t.Fatalf("scan bins index: %v", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("read bins indexes: %v", err)
	}
	rows.Close()

	shape := make([]string, 0, len(entries))
	for _, e := range entries {
		cols, err := indexColumns(conn, e.name)
		if err != nil {
			t.Fatalf("read index %q columns: %v", e.name, err)
		}
		shape = append(shape, fmt.Sprintf("%s|unique=%d|origin=%s|columns=%s", e.name, e.unique, e.origin, strings.Join(cols, ",")))
	}
	sort.Strings(shape)
	return shape
}

func indexColumns(conn *sql.DB, index string) ([]string, error) {
	rows, err := conn.Query(fmt.Sprintf("SELECT COALESCE(name, '<expression>') FROM pragma_index_info('%s') ORDER BY seqno", index))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

// binsForeignKeyShape renders every foreign key declared on bins.
func binsForeignKeyShape(t *testing.T, conn *sql.DB) []string {
	t.Helper()
	rows, err := conn.Query(`SELECT "table", "from", COALESCE("to", '<implicit>'), on_update, on_delete FROM pragma_foreign_key_list('bins')`)
	if err != nil {
		t.Fatalf("read bins foreign keys: %v", err)
	}
	defer rows.Close()

	var shape []string
	for rows.Next() {
		var table, from, to, onUpdate, onDelete string
		if err := rows.Scan(&table, &from, &to, &onUpdate, &onDelete); err != nil {
			t.Fatalf("scan bins foreign key: %v", err)
		}
		shape = append(shape, fmt.Sprintf("%s|%s|%s|update=%s|delete=%s", table, from, to, onUpdate, onDelete))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read bins foreign keys: %v", err)
	}
	sort.Strings(shape)
	return shape
}

// referencedTables lists the tables one table's foreign keys point at.
func referencedTables(t *testing.T, conn *sql.DB, table string) []string {
	t.Helper()
	rows, err := conn.Query(fmt.Sprintf("SELECT DISTINCT \"table\" FROM pragma_foreign_key_list('%s') ORDER BY \"table\"", table))
	if err != nil {
		t.Fatalf("read %s foreign keys: %v", table, err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan %s foreign key: %v", table, err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s foreign keys: %v", table, err)
	}
	return tables
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type binNullableBinRow struct {
	ID          int64
	Name        string
	ContainerID sql.NullInt64
	LedIndex    sql.NullInt64
	Width       sql.NullInt64
	GridX       sql.NullInt64
	GridY       sql.NullInt64
}

func binsRows(t *testing.T, conn *sql.DB) []binNullableBinRow {
	t.Helper()
	rows, err := conn.Query(`SELECT id, name, container_id, led_index, width, grid_x, grid_y FROM bins ORDER BY id`)
	if err != nil {
		t.Fatalf("read bins rows: %v", err)
	}
	defer rows.Close()

	var out []binNullableBinRow
	for rows.Next() {
		var row binNullableBinRow
		if err := rows.Scan(&row.ID, &row.Name, &row.ContainerID, &row.LedIndex, &row.Width, &row.GridX, &row.GridY); err != nil {
			t.Fatalf("scan bins row: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read bins rows: %v", err)
	}
	return out
}

type binNullableAssignmentRow struct {
	ID       int64
	PartID   int64
	BinID    sql.NullInt64
	Quantity int64
}

func assignmentRows(t *testing.T, conn *sql.DB) []binNullableAssignmentRow {
	t.Helper()
	rows, err := conn.Query(`SELECT id, part_id, bin_id, quantity FROM part_assignments ORDER BY id`)
	if err != nil {
		t.Fatalf("read part assignments: %v", err)
	}
	defer rows.Close()

	var out []binNullableAssignmentRow
	for rows.Next() {
		var row binNullableAssignmentRow
		if err := rows.Scan(&row.ID, &row.PartID, &row.BinID, &row.Quantity); err != nil {
			t.Fatalf("scan part assignment: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read part assignments: %v", err)
	}
	return out
}

type binNullableAttachmentRow struct {
	BinID    int64
	DrawerID int64
	X        float64
	Y        float64
	Width    float64
	Height   float64
}

func attachmentRows(t *testing.T, conn *sql.DB) []binNullableAttachmentRow {
	t.Helper()
	rows, err := conn.Query(`SELECT bin_id, drawer_id, x, y, width, height FROM bin_drawer_attachments ORDER BY bin_id`)
	if err != nil {
		t.Fatalf("read bin drawer attachments: %v", err)
	}
	defer rows.Close()

	var out []binNullableAttachmentRow
	for rows.Next() {
		var row binNullableAttachmentRow
		if err := rows.Scan(&row.BinID, &row.DrawerID, &row.X, &row.Y, &row.Width, &row.Height); err != nil {
			t.Fatalf("scan bin drawer attachment: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read bin drawer attachments: %v", err)
	}
	return out
}

type binNullableContainerRow struct {
	ID           int64
	Name         string
	ControllerID int64
	SegmentID    int64
	LedStart     int64
	LedCount     int64
}

func containerRows(t *testing.T, conn *sql.DB) []binNullableContainerRow {
	t.Helper()
	rows, err := conn.Query(`SELECT id, name, controller_id, segment_id, led_start, led_count FROM containers ORDER BY id`)
	if err != nil {
		t.Fatalf("read containers: %v", err)
	}
	defer rows.Close()

	var out []binNullableContainerRow
	for rows.Next() {
		var row binNullableContainerRow
		if err := rows.Scan(&row.ID, &row.Name, &row.ControllerID, &row.SegmentID, &row.LedStart, &row.LedCount); err != nil {
			t.Fatalf("scan container: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read containers: %v", err)
	}
	return out
}

func flagRows(t *testing.T, conn *sql.DB) [][2]string {
	t.Helper()
	rows, err := conn.Query(`SELECT key, value FROM system_flags ORDER BY key`)
	if err != nil {
		t.Fatalf("read system flags: %v", err)
	}
	defer rows.Close()

	var out [][2]string
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("scan system flag: %v", err)
		}
		out = append(out, [2]string{key, value})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read system flags: %v", err)
	}
	return out
}

type binNullableSeed struct {
	containers []int64
	bins       []int64
	retired    int64
	cabinet    int64
	drawer     int64
}

// seedBinNullableGraph inserts the legacy graph the migration must preserve: two
// controllers with a container each, four placed bins plus one retired bin, a
// multi-location part, a NULL-location assignment, a zero-quantity assignment, a
// dangling assignment of the kind the legacy database carries, one drawer
// attachment and the compatibility flags.
func seedBinNullableGraph(f binNullableFixture) binNullableSeed {
	f.t.Helper()

	f.exec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (1, 'Legacy A', '192.168.1.10', 80, 300)`)
	f.exec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (2, 'Legacy B', '192.168.1.11', 80, 150)`)
	f.exec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (1, 'String 1', 1, 0, 0, 300)`)
	f.exec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (2, 'String 2', 2, 0, 0, 150)`)
	f.exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (1, 'Bin A1', 1, 0, 4, 0, 0)`)
	f.exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (2, 'Bin A2', 1, 10, 2, 1, 0)`)
	f.exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (3, 'Bin A3', 1, NULL, 1, 2, 0)`)
	f.exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (4, 'Bin B1', 2, 0, 4, 0, 0)`)
	f.exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (5, 'Bin Retired', 1, 200, 1, 3, 0)`)
	f.exec(`INSERT INTO parts (id, name) VALUES (1, 'Part One')`)
	f.exec(`INSERT INTO parts (id, name) VALUES (2, 'Part Two')`)
	f.exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (1, 1, 1, 5)`)
	f.exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (2, 1, 2, 7)`)
	f.exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (3, 2, NULL, 3)`)
	f.exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (4, 2, 4, 0)`)
	f.execScript("PRAGMA foreign_keys = OFF;\n" +
		"INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (5, 2, 4242, 11);\n" +
		"PRAGMA foreign_keys = ON;")
	f.exec(`INSERT INTO system_flags (key, value) VALUES (?, 'segment')`, binNullableSpaceFlag)
	f.exec(`INSERT INTO system_flags (key, value) VALUES (?, 'true')`, binNullableMigrationFlag)
	f.exec(`INSERT INTO system_flags (key, value) VALUES (?, 'true')`, binNullableBackfillFlag)

	cabinet := f.insert(`INSERT INTO cabinets (name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES ('Cabinet A', 1, 'segment', 1, 0, 0, 0)`)
	drawer := f.insert(`INSERT INTO drawers (cabinet_id, label, display_order, x, y, width, height, archived) VALUES (?, 'Drawer A', 0, 0.0, 0.0, 10.0, 5.0, 0)`, cabinet)
	f.exec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (1, ?, 0.5, 0.5, 3.0, 2.0, 0)`, drawer)

	return binNullableSeed{
		containers: []int64{1, 2},
		bins:       []int64{1, 2, 3, 4, 5},
		retired:    5,
		cabinet:    cabinet,
		drawer:     drawer,
	}
}

// TestBinContainerNullableMigration_ForwardPreservesIdentityAndMapping applies
// the migration over a populated legacy database and requires every id,
// quantity, mapping, attachment and flag to survive unchanged, only
// container_id's nullability to change, the AUTOINCREMENT high-water mark to be
// carried and a repeat run to be a no-op.
func TestBinContainerNullableMigration_ForwardPreservesIdentityAndMapping(t *testing.T) {
	f := openBinNullableFixture(t, "bin_nullable_forward")
	seed := seedBinNullableGraph(f)

	if tableExists(t, f.dbConn, "bins_rebuild") {
		t.Fatalf("bins_rebuild exists before migration %d is applied", binNullableVersion)
	}
	if got := binContainerNotNull(t, f.dbConn); got != 1 {
		t.Fatalf("container_id NOT NULL flag = %d before the migration, want 1", got)
	}

	beforeColumns := binsColumnShape(t, f.dbConn)
	beforeIndexes := binsIndexShape(t, f.dbConn)
	beforeForeignKeys := binsForeignKeyShape(t, f.dbConn)
	beforeBins := binsRows(t, f.dbConn)
	beforeAssignments := assignmentRows(t, f.dbConn)
	beforeAttachments := attachmentRows(t, f.dbConn)
	beforeContainers := containerRows(t, f.dbConn)
	beforeFlags := flagRows(t, f.dbConn)
	beforeCabinets := f.count(`SELECT COUNT(*) FROM cabinets`)
	beforeDrawers := f.count(`SELECT COUNT(*) FROM drawers`)

	// Retire the highest-id bin so the high-water mark sits above the surviving
	// max: the rebuild must carry it, or the retired id would be handed out again.
	f.exec(`DELETE FROM bins WHERE id = ?`, seed.retired)
	surviving := beforeBins[:len(beforeBins)-1]

	f.migrateToNullable()

	if !tableExists(t, f.dbConn, "bins") {
		t.Fatalf("bins table missing after migration %d", binNullableVersion)
	}
	if tableExists(t, f.dbConn, "bins_rebuild") {
		t.Errorf("migration %d left bins_rebuild behind", binNullableVersion)
	}
	if got := f.count(`SELECT COUNT(*) FROM bins WHERE id = ?`, seed.retired); got != 0 {
		t.Errorf("retired bin %d reappeared after the migration", seed.retired)
	}

	// The migration's whole schema effect is container_id's nullability: every
	// other column definition, index and foreign key must match the captured
	// pre-migration shape.
	if got := binContainerNotNull(t, f.dbConn); got != 0 {
		t.Errorf("container_id NOT NULL flag = %d after the migration, want 0", got)
	}
	wantColumns := append([]string(nil), beforeColumns...)
	for i, column := range wantColumns {
		if strings.HasPrefix(column, "container_id|") {
			wantColumns[i] = strings.Replace(column, "notnull=1", "notnull=0", 1)
		}
	}
	if got := binsColumnShape(t, f.dbConn); !reflect.DeepEqual(wantColumns, got) {
		t.Errorf("bins columns after the migration = %v, want %v", got, wantColumns)
	}
	if got := binsIndexShape(t, f.dbConn); !reflect.DeepEqual(beforeIndexes, got) {
		t.Errorf("bins indexes after the migration = %v, want %v", got, beforeIndexes)
	}
	if got := binsForeignKeyShape(t, f.dbConn); !reflect.DeepEqual(beforeForeignKeys, got) {
		t.Errorf("bins foreign keys after the migration = %v, want %v", got, beforeForeignKeys)
	}
	for _, child := range []string{"part_assignments", "bin_drawer_attachments"} {
		if tables := referencedTables(t, f.dbConn, child); !contains(tables, "bins") {
			t.Errorf("%s no longer references bins: %v", child, tables)
		}
	}

	if got := binsRows(t, f.dbConn); !reflect.DeepEqual(surviving, got) {
		t.Errorf("bins rows after the migration = %+v, want %+v", got, surviving)
	}
	if got := assignmentRows(t, f.dbConn); !reflect.DeepEqual(beforeAssignments, got) {
		t.Errorf("part assignments after the migration = %+v, want %+v", got, beforeAssignments)
	}
	if got := attachmentRows(t, f.dbConn); !reflect.DeepEqual(beforeAttachments, got) {
		t.Errorf("bin drawer attachments after the migration = %+v, want %+v", got, beforeAttachments)
	}
	if got := containerRows(t, f.dbConn); !reflect.DeepEqual(beforeContainers, got) {
		t.Errorf("containers after the migration = %+v, want %+v", got, beforeContainers)
	}
	if got := flagRows(t, f.dbConn); !reflect.DeepEqual(beforeFlags, got) {
		t.Errorf("system flags after the migration = %v, want %v", got, beforeFlags)
	}

	// Nothing physical is inferred: no cabinet, drawer or attachment is created.
	if got := f.count(`SELECT COUNT(*) FROM cabinets`); got != beforeCabinets {
		t.Errorf("cabinets after the migration = %d, want the %d that existed", got, beforeCabinets)
	}
	if got := f.count(`SELECT COUNT(*) FROM drawers`); got != beforeDrawers {
		t.Errorf("drawers after the migration = %d, want the %d that existed", got, beforeDrawers)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != int64(len(beforeAttachments)) {
		t.Errorf("attachments after the migration = %d, want the %d that existed", got, len(beforeAttachments))
	}

	// The high-water mark is carried onto the rebuilt table, and an unplaced bin
	// is representable.
	newBin := f.insert(`INSERT INTO bins (name) VALUES ('Unplaced')`)
	if newBin != seed.retired+1 {
		t.Errorf("new bin id = %d, want %d: the retired id %d must never be reused", newBin, seed.retired+1, seed.retired)
	}
	var containerID, width sql.NullInt64
	if err := f.dbConn.QueryRowContext(f.ctx, `SELECT container_id, width FROM bins WHERE id = ?`, newBin).Scan(&containerID, &width); err != nil {
		t.Fatalf("read the new unplaced bin: %v", err)
	}
	if containerID.Valid {
		t.Errorf("unplaced bin container_id = %d, want NULL", containerID.Int64)
	}
	if !width.Valid || width.Int64 != 1 {
		t.Errorf("unplaced bin width = %+v, want the preserved DEFAULT 1", width)
	}

	// A repeat run changes nothing.
	repeatedBins := binsRows(t, f.dbConn)
	repeatedAssignments := assignmentRows(t, f.dbConn)
	f.migrateToNullable()
	if got := binsRows(t, f.dbConn); !reflect.DeepEqual(repeatedBins, got) {
		t.Errorf("bins rows after a repeat migration = %+v, want %+v", got, repeatedBins)
	}
	if got := assignmentRows(t, f.dbConn); !reflect.DeepEqual(repeatedAssignments, got) {
		t.Errorf("part assignments after a repeat migration = %+v, want %+v", got, repeatedAssignments)
	}
	if got := binContainerNotNull(t, f.dbConn); got != 0 {
		t.Errorf("container_id NOT NULL flag = %d after a repeat migration, want 0", got)
	}
	if tableExists(t, f.dbConn, "bins_rebuild") {
		t.Errorf("a repeat migration left bins_rebuild behind")
	}
	if got := f.count(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`, binNullableVersion); got != 1 {
		t.Errorf("goose recorded version %d %d times, want 1", binNullableVersion, got)
	}
}

// TestBinContainerNullableMigration_AcceptsUnplacedBinsAndKeepsConstraints
// requires an unplaced bin to be representable while the unique constraint, the
// container default, the foreign key and its delete actions keep working.
func TestBinContainerNullableMigration_AcceptsUnplacedBinsAndKeepsConstraints(t *testing.T) {
	f := openBinNullableFixture(t, "bin_nullable_constraints")
	seedBinNullableGraph(f)

	f.migrateToNullable()

	unplaced := f.insert(`INSERT INTO bins (name, container_id, led_index) VALUES ('Unplaced', NULL, NULL)`)
	f.insert(`INSERT INTO bins (name, container_id, led_index) VALUES ('Unplaced Second', NULL, NULL)`)
	if got := f.count(`SELECT COUNT(*) FROM bins WHERE container_id IS NULL`); got != 2 {
		t.Errorf("unplaced bins = %d, want 2 (NULL container ids are independent)", got)
	}

	// The rebuild turns foreign key enforcement off to drop the old table, and
	// must turn it back on: this reads the connection that ran the migration.
	var foreignKeys int
	if err := f.dbConn.QueryRowContext(f.ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read PRAGMA foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("PRAGMA foreign_keys = %d after the migration, want 1", foreignKeys)
	}

	var containerID, ledIndex, width sql.NullInt64
	if err := f.dbConn.QueryRowContext(f.ctx, `SELECT container_id, led_index, width FROM bins WHERE id = ?`, unplaced).Scan(&containerID, &ledIndex, &width); err != nil {
		t.Fatalf("read the unplaced bin: %v", err)
	}
	if containerID.Valid || ledIndex.Valid {
		t.Errorf("unplaced bin = (container %+v, led %+v), want both NULL", containerID, ledIndex)
	}
	if !width.Valid || width.Int64 != 1 {
		t.Errorf("unplaced bin width = %+v, want the DEFAULT 1", width)
	}

	// The unique constraint and the container foreign key apply to the rebuilt
	// table exactly as they did before.
	if err := f.execErr(`INSERT INTO bins (name, container_id, led_index) VALUES ('Duplicate', 1, 0)`); err == nil {
		t.Errorf("duplicate (container_id, led_index) = (1, 0) was accepted, want a unique violation")
	}
	if err := f.execErr(`INSERT INTO bins (name, container_id, led_index) VALUES ('Orphan', 999999, NULL)`); err == nil {
		t.Errorf("a bin referencing a missing container was accepted, want a foreign key violation")
	}
	if got := binsForeignKeyShape(t, f.dbConn); !contains(got, "containers|container_id|id|update=NO ACTION|delete=CASCADE") {
		t.Errorf("bins foreign keys after the migration = %v, want the container reference with ON DELETE CASCADE", got)
	}

	// UNIQUE(container_id, led_index) keeps the legacy NULL semantics: rows with
	// a NULL led_index are distinct.
	f.insert(`INSERT INTO bins (name, container_id, led_index) VALUES ('No Led One', 1, NULL)`)
	f.insert(`INSERT INTO bins (name, container_id, led_index) VALUES ('No Led Two', 1, NULL)`)
	if got := f.count(`SELECT COUNT(*) FROM bins WHERE container_id = 1 AND led_index IS NULL`); got != 3 {
		t.Errorf("bins with (container 1, NULL led_index) = %d, want 3: legacy NULL semantics are preserved", got)
	}

	// An assignment on an unplaced bin is representable and keeps its quantity.
	f.exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (10, 1, ?, 4)`, unplaced)
	if got := f.count(`SELECT COUNT(*) FROM part_assignments WHERE bin_id = ? AND quantity = 4`, unplaced); got != 1 {
		t.Errorf("assignment on the unplaced bin is missing, want one row with quantity 4")
	}

	// The rebuilt table's delete actions are: deleting container 2 cascades to its
	// bin, and the assignment that pointed at that bin keeps its row with a NULL
	// location.
	f.exec(`DELETE FROM containers WHERE id = ?`, 2)
	if got := f.count(`SELECT COUNT(*) FROM bins WHERE id = 4`); got != 0 {
		t.Errorf("bin 4 survived the cascade delete of its container, want it removed")
	}
	if got := f.count(`SELECT COUNT(*) FROM part_assignments WHERE id = 4 AND bin_id IS NULL AND quantity = 0`); got != 1 {
		t.Errorf("the assignment of a cascaded bin = %d, want it kept with a NULL location and its quantity", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bins WHERE container_id IS NULL`); got != 2 {
		t.Errorf("unplaced bins after deleting a container = %d, want 2", got)
	}

	// A bin attachment restricts the deletion of the container that owns its bin.
	containerOneBins := f.count(`SELECT COUNT(*) FROM bins WHERE container_id = 1`)
	if err := f.execErr(`DELETE FROM containers WHERE id = ?`, 1); err == nil {
		t.Errorf("deleting container 1 was accepted although its bin has an attachment, want a foreign key violation")
	}
	if got := f.count(`SELECT COUNT(*) FROM containers WHERE id = ?`, 1); got != 1 {
		t.Errorf("container 1 rows after a refused delete = %d, want 1", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bins WHERE container_id = 1`); got != containerOneBins {
		t.Errorf("bins of container 1 after a refused delete = %d, want %d", got, containerOneBins)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments`); got != 1 {
		t.Errorf("attachments after refused deletes = %d, want 1", got)
	}
}

// TestBinContainerNullableMigration_RollbackRestoresNotNullSchema rolls the
// migration back while every bin has a container and requires the pre-M5 schema
// and every row to come back unchanged.
func TestBinContainerNullableMigration_RollbackRestoresNotNullSchema(t *testing.T) {
	f := openBinNullableFixture(t, "bin_nullable_rollback")
	seed := seedBinNullableGraph(f)

	beforeColumns := binsColumnShape(t, f.dbConn)
	beforeIndexes := binsIndexShape(t, f.dbConn)
	beforeForeignKeys := binsForeignKeyShape(t, f.dbConn)
	beforeBins := binsRows(t, f.dbConn)
	beforeAssignments := assignmentRows(t, f.dbConn)
	beforeAttachments := attachmentRows(t, f.dbConn)
	beforeContainers := containerRows(t, f.dbConn)
	beforeFlags := flagRows(t, f.dbConn)

	f.exec(`DELETE FROM bins WHERE id = ?`, seed.retired)
	surviving := beforeBins[:len(beforeBins)-1]

	f.migrateToNullable()
	if err := f.rollbackToLegacy(); err != nil {
		t.Fatalf("roll migration %d back: %v", binNullableVersion, err)
	}

	if got := binContainerNotNull(t, f.dbConn); got != 1 {
		t.Errorf("container_id NOT NULL flag = %d after the rollback, want 1", got)
	}
	if got := binsColumnShape(t, f.dbConn); !reflect.DeepEqual(beforeColumns, got) {
		t.Errorf("bins columns after the rollback = %v, want the pre-migration shape %v", got, beforeColumns)
	}
	if got := binsIndexShape(t, f.dbConn); !reflect.DeepEqual(beforeIndexes, got) {
		t.Errorf("bins indexes after the rollback = %v, want %v", got, beforeIndexes)
	}
	if got := binsForeignKeyShape(t, f.dbConn); !reflect.DeepEqual(beforeForeignKeys, got) {
		t.Errorf("bins foreign keys after the rollback = %v, want %v", got, beforeForeignKeys)
	}
	if got := binsRows(t, f.dbConn); !reflect.DeepEqual(surviving, got) {
		t.Errorf("bins rows after the rollback = %+v, want %+v", got, surviving)
	}
	if got := assignmentRows(t, f.dbConn); !reflect.DeepEqual(beforeAssignments, got) {
		t.Errorf("part assignments after the rollback = %+v, want %+v", got, beforeAssignments)
	}
	if got := attachmentRows(t, f.dbConn); !reflect.DeepEqual(beforeAttachments, got) {
		t.Errorf("bin drawer attachments after the rollback = %+v, want %+v", got, beforeAttachments)
	}
	if got := containerRows(t, f.dbConn); !reflect.DeepEqual(beforeContainers, got) {
		t.Errorf("containers after the rollback = %+v, want %+v", got, beforeContainers)
	}
	if got := flagRows(t, f.dbConn); !reflect.DeepEqual(beforeFlags, got) {
		t.Errorf("system flags after the rollback = %v, want %v", got, beforeFlags)
	}
	if tableExists(t, f.dbConn, "bins_rebuild") {
		t.Errorf("the rollback left bins_rebuild behind")
	}
	if got := f.count(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`, binNullableVersion); got != 0 {
		t.Errorf("goose still records version %d after the rollback", binNullableVersion)
	}
	if got := f.count(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`, binNullableLegacyVersion); got != 1 {
		t.Errorf("goose records version %d %d times after the rollback, want 1", binNullableLegacyVersion, got)
	}

	// The rollback restores the NOT NULL constraint and carries the high-water
	// mark with it.
	if err := f.execErr(`INSERT INTO bins (name, container_id) VALUES ('Unplaced', NULL)`); err == nil {
		t.Errorf("NULL container_id was accepted after the rollback, want a NOT NULL violation")
	}
	if got := f.insert(`INSERT INTO bins (name, container_id, led_index) VALUES ('After Rollback', 1, 7)`); got != seed.retired+1 {
		t.Errorf("new bin id after the rollback = %d, want %d: the high-water mark must survive", got, seed.retired+1)
	}
}

// TestBinContainerNullableMigration_RollbackRefusesUnplacedBins requires the
// rollback to refuse while a bin has no container, to leave the database file
// untouched, and to succeed once that bin has a container again.
func TestBinContainerNullableMigration_RollbackRefusesUnplacedBins(t *testing.T) {
	f := openBinNullableFixture(t, "bin_nullable_refusal")
	seed := seedBinNullableGraph(f)

	f.migrateToNullable()

	f.exec(`CREATE TABLE _bin_decoupling_rollback_guard (marker TEXT NOT NULL)`)
	f.exec(`INSERT INTO _bin_decoupling_rollback_guard (marker) VALUES ('preserve')`)
	unplaced := f.insert(`INSERT INTO bins (name, container_id, led_index) VALUES ('Unplaced', NULL, NULL)`)
	f.exec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (?, ?, 0.0, 0.0, 2.0, 2.0, 1)`, unplaced, seed.drawer)
	f.exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (20, 1, ?, 9)`, unplaced)

	beforeBins := binsRows(t, f.dbConn)
	beforeAssignments := assignmentRows(t, f.dbConn)
	beforeAttachments := attachmentRows(t, f.dbConn)

	if err := f.rollbackToLegacy(); err == nil {
		t.Fatalf("rollback succeeded although bin %d has a NULL container_id, want a refusal", unplaced)
	}

	// A refused rollback must not change the schema or a single row, and that
	// must be visible from an independent connection as well, so pooled
	// connection state cannot hide a partial rewrite.
	second, err := db.Open("file:" + f.path)
	if err != nil {
		t.Fatalf("open a second connection: %v", err)
	}
	t.Cleanup(func() { second.Close() })

	check := func(label string, conn *sql.DB) {
		t.Helper()
		if !tableExists(t, conn, "bins") {
			t.Errorf("%s: bins table missing after a refused rollback", label)
		}
		if got := binContainerNotNull(t, conn); got != 0 {
			t.Errorf("%s: container_id NOT NULL flag = %d after a refused rollback, want 0", label, got)
		}
		if got := binsRows(t, conn); !reflect.DeepEqual(beforeBins, got) {
			t.Errorf("%s: bins rows after a refused rollback = %+v, want %+v", label, got, beforeBins)
		}
		if got := assignmentRows(t, conn); !reflect.DeepEqual(beforeAssignments, got) {
			t.Errorf("%s: part assignments after a refused rollback = %+v, want %+v", label, got, beforeAssignments)
		}
		if got := attachmentRows(t, conn); !reflect.DeepEqual(beforeAttachments, got) {
			t.Errorf("%s: bin drawer attachments after a refused rollback = %+v, want %+v", label, got, beforeAttachments)
		}
	}
	check("fixture connection", f.dbConn)
	check("independent connection", second)

	if tableExists(t, f.dbConn, "bins_rebuild") {
		t.Errorf("the refused rollback left bins_rebuild behind")
	}
	if got := f.count(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ?`, binNullableVersion); got != 1 {
		t.Errorf("goose records version %d %d times after a refused rollback, want 1", binNullableVersion, got)
	}
	if got := f.count(`SELECT COUNT(*) FROM main._bin_decoupling_rollback_guard WHERE marker = 'preserve'`); got != 1 {
		t.Errorf("unrelated main-schema rollback guard rows = %d after refusal, want 1", got)
	}

	// The refusal is data-driven, not permanent: once the unplaced bin has a
	// real container the same rollback succeeds and keeps every row.
	container := f.insert(`INSERT INTO containers (name, controller_id) VALUES ('String 3', 1)`)
	f.exec(`UPDATE bins SET container_id = ? WHERE id = ?`, container, unplaced)

	if err := f.rollbackToLegacy(); err != nil {
		t.Fatalf("roll migration %d back after placing every bin: %v", binNullableVersion, err)
	}
	if got := binContainerNotNull(t, f.dbConn); got != 1 {
		t.Errorf("container_id NOT NULL flag = %d after the rollback, want 1", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM bins WHERE id = ? AND container_id = ?`, unplaced, container); got != 1 {
		t.Errorf("bin %d was not preserved with container %d across the rollback", unplaced, container)
	}
	if got := f.count(`SELECT COUNT(*) FROM bin_drawer_attachments WHERE bin_id = ?`, unplaced); got != 1 {
		t.Errorf("the attachment of bin %d was not preserved across the rollback", unplaced)
	}
	if got := f.count(`SELECT COUNT(*) FROM part_assignments WHERE id = 20 AND bin_id = ? AND quantity = 9`, unplaced); got != 1 {
		t.Errorf("the assignment of bin %d was not preserved across the rollback", unplaced)
	}
	if got := f.count(`SELECT COUNT(*) FROM main._bin_decoupling_rollback_guard WHERE marker = 'preserve'`); got != 1 {
		t.Errorf("unrelated main-schema rollback guard rows = %d after success, want 1", got)
	}
}
