package db_test

import (
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

const cabinetWiringVersion int64 = 19

func openCabinetWiringFixture(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open("file:" + filepath.Join(t.TempDir(), "cabinet-wiring.db"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	goose.SetBaseFS(schema.Migrations)
	if err := goose.UpTo(conn, ".", 18); err != nil {
		t.Fatalf("migrate fixture to Task 048 schema: %v", err)
	}
	return conn
}

type migrationLocalAllocation struct {
	id, drawerID, controllerID, start, count, order int64
	createdAt, updatedAt                            string
}

func seedCabinetWiringMigration(t *testing.T, conn *sql.DB, capacities []int64, allocations []migrationLocalAllocation) {
	t.Helper()
	for i, capacity := range capacities {
		controllerID := int64(i + 1)
		if _, err := conn.Exec(`INSERT INTO controllers (id,name,ip_address,port,led_count) VALUES (?,?,?,80,?)`, controllerID, fmt.Sprintf("controller-%d", controllerID), fmt.Sprintf("127.0.0.%d", controllerID), capacity); err != nil {
			t.Fatalf("insert controller %d: %v", controllerID, err)
		}
	}
	if len(capacities) > 0 {
		if _, err := conn.Exec(`INSERT INTO cabinets (id,name,controller_id,coordinate_system) VALUES (1,'cabinet',1,'grid')`); err != nil {
			t.Fatalf("insert cabinet: %v", err)
		}
	}
	for i := range capacities {
		controllerID := int64(i + 1)
		if _, err := conn.Exec(`INSERT INTO cabinet_controllers (cabinet_id,controller_id,slot) VALUES (1,?,?)`, controllerID, i); err != nil {
			t.Fatalf("insert membership for controller %d: %v", controllerID, err)
		}
	}
	for _, allocation := range allocations {
		if _, err := conn.Exec(`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height) VALUES (?,1,?,0,0,0,10,10)`, allocation.drawerID, fmt.Sprintf("drawer-%d", allocation.drawerID)); err != nil {
			t.Fatalf("insert drawer %d: %v", allocation.drawerID, err)
		}
		if _, err := conn.Exec(`INSERT INTO drawer_led_allocations (id,cabinet_id,drawer_id,controller_id,start_pixel,pixel_count,display_order,created_at,updated_at) VALUES (?,1,?,?,?,?,?,?,?)`, allocation.id, allocation.drawerID, allocation.controllerID, allocation.start, allocation.count, allocation.order, allocation.createdAt, allocation.updatedAt); err != nil {
			t.Fatalf("insert allocation %d: %v", allocation.id, err)
		}
	}
}

func migrationRows(t *testing.T, conn *sql.DB, query string) []string {
	t.Helper()
	rows, err := conn.Query(query)
	if err != nil {
		t.Fatalf("query migration snapshot %q: %v", query, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read snapshot columns: %v", err)
	}
	var result []string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan migration snapshot: %v", err)
		}
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = fmt.Sprintf("%T:%v", value, value)
		}
		result = append(result, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migration snapshot: %v", err)
	}
	return result
}

type cabinetWiringSnapshot struct {
	schema, gooseVersions, controllers, cabinets, memberships, drawers, allocations, sequence []string
	foreignKeys                                                                               int
}

func captureCabinetWiringSnapshot(t *testing.T, conn *sql.DB) cabinetWiringSnapshot {
	t.Helper()
	var state cabinetWiringSnapshot
	state.schema = migrationRows(t, conn, `SELECT type,name,sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	state.gooseVersions = migrationRows(t, conn, `SELECT * FROM goose_db_version ORDER BY id`)
	state.controllers = migrationRows(t, conn, `SELECT id,name,ip_address,port,led_count FROM controllers ORDER BY id`)
	state.cabinets = migrationRows(t, conn, `SELECT id,name,controller_id,coordinate_system FROM cabinets ORDER BY id`)
	state.memberships = migrationRows(t, conn, `SELECT cabinet_id,controller_id,slot,created_at,updated_at FROM cabinet_controllers ORDER BY cabinet_id,slot`)
	state.drawers = migrationRows(t, conn, `SELECT id,cabinet_id,label,display_order FROM drawers ORDER BY id`)
	var hasControllerColumn int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('drawer_led_allocations') WHERE name='controller_id'`).Scan(&hasControllerColumn); err != nil {
		t.Fatalf("inspect allocation schema: %v", err)
	}
	if hasControllerColumn == 1 {
		state.allocations = migrationRows(t, conn, `SELECT id,cabinet_id,drawer_id,controller_id,start_pixel,pixel_count,display_order,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM drawer_led_allocations ORDER BY id`)
	} else {
		state.allocations = migrationRows(t, conn, `SELECT id,cabinet_id,drawer_id,start_pixel,pixel_count,display_order,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM drawer_led_allocations ORDER BY id`)
	}
	state.sequence = migrationRows(t, conn, `SELECT name,seq FROM sqlite_sequence ORDER BY name`)
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&state.foreignKeys); err != nil {
		t.Fatalf("read foreign-key state: %v", err)
	}
	return state
}

func assertCabinetWiringSnapshotEqual(t *testing.T, want, got cabinetWiringSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("persistent database state changed on refused migration/downgrade:\nwant=%+v\ngot=%+v", want, got)
	}
}

func TestCabinetWiringMigrationTranslatesAndPreservesOneThroughFiveControllers(t *testing.T) {
	for controllerCount := 1; controllerCount <= 5; controllerCount++ {
		t.Run(fmt.Sprintf("controllers_%d", controllerCount), func(t *testing.T) {
			conn := openCabinetWiringFixture(t)
			capacities := make([]int64, controllerCount)
			allocations := make([]migrationLocalAllocation, controllerCount)
			for i := range capacities {
				capacities[i] = int64(100 * (i + 1))
				allocations[i] = migrationLocalAllocation{
					id: int64(70 + i), drawerID: int64(20 + i), controllerID: int64(i + 1),
					start: 10, count: 5, order: int64(i + 3),
					createdAt: fmt.Sprintf("2024-01-%02d 03:04:05", i+1),
					updatedAt: fmt.Sprintf("2024-02-%02d 06:07:08", i+1),
				}

			}
			seedCabinetWiringMigration(t, conn, capacities, allocations)
			if err := goose.UpTo(conn, ".", cabinetWiringVersion); err != nil {
				t.Fatalf("migrate %d-controller fixture: %v", controllerCount, err)
			}

			rows, err := conn.Query(`SELECT id,cabinet_id,drawer_id,start_pixel,pixel_count,display_order,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM drawer_led_allocations ORDER BY id`)
			if err != nil {
				t.Fatalf("query migrated allocations: %v", err)
			}
			defer rows.Close()
			for i, original := range allocations {
				var id, cabinetID, drawerID, start, count, order int64
				var createdAt, updatedAt string
				if !rows.Next() {
					t.Fatalf("missing migrated allocation %d", original.id)
				}
				if err := rows.Scan(&id, &cabinetID, &drawerID, &start, &count, &order, &createdAt, &updatedAt); err != nil {
					t.Fatalf("scan migrated allocation: %v", err)
				}
				var expectedStart int64 = 10
				for prior := 0; prior < i; prior++ {
					expectedStart += capacities[prior]
				}
				if id != original.id || cabinetID != 1 || drawerID != original.drawerID || start != expectedStart || count != original.count || order != original.order || createdAt != original.createdAt || updatedAt != original.updatedAt {
					t.Errorf("allocation %d changed: got id/cabinet/drawer/start/count/order/times=(%d,%d,%d,%d,%d,%d,%q,%q), want (%d,1,%d,%d,%d,%d,%q,%q)", original.id, id, cabinetID, drawerID, start, count, order, createdAt, updatedAt, original.id, original.drawerID, expectedStart, original.count, original.order, original.createdAt, original.updatedAt)
				}
			}
			if rows.Next() {
				t.Fatal("migration produced extra allocations")
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("iterate migrated allocations: %v", err)
			}
		})
	}
}

func TestCabinetWiringMigrationUsesSavedControllerSlotOrder(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	allocations := []migrationLocalAllocation{
		{id: 1, drawerID: 1, controllerID: 1, start: 1, count: 1, order: 0, createdAt: "2024-01-01", updatedAt: "2024-01-01"},
		{id: 2, drawerID: 2, controllerID: 2, start: 1, count: 1, order: 0, createdAt: "2024-01-02", updatedAt: "2024-01-02"},
		{id: 3, drawerID: 3, controllerID: 3, start: 1, count: 1, order: 0, createdAt: "2024-01-03", updatedAt: "2024-01-03"},
	}
	seedCabinetWiringMigration(t, conn, []int64{100, 200, 300}, allocations)
	for _, update := range []string{
		`UPDATE cabinet_controllers SET slot=3 WHERE cabinet_id=1 AND controller_id=1`,
		`UPDATE cabinet_controllers SET slot=0 WHERE cabinet_id=1 AND controller_id=3`,
		`UPDATE cabinet_controllers SET slot=2 WHERE cabinet_id=1 AND controller_id=2`,
		`UPDATE cabinet_controllers SET slot=1 WHERE cabinet_id=1 AND controller_id=1`,
	} {
		if _, err := conn.Exec(update); err != nil {
			t.Fatalf("reorder cabinet controllers: %v", err)
		}
	}
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err != nil {
		t.Fatalf("migrate reordered cabinet: %v", err)
	}
	rows := migrationRows(t, conn, `SELECT drawer_id,start_pixel FROM drawer_led_allocations ORDER BY drawer_id`)
	want := []string{"int64:1|int64:301", "int64:2|int64:401", "int64:3|int64:1"}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("saved slot order translation=%v, want %v", rows, want)
	}
}

func TestCabinetWiringMigrationRejectsHistoricalOverlapWithoutMutation(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	for _, trigger := range []string{"drawer_led_allocations_no_overlap_insert", "drawer_led_allocations_no_overlap_update"} {
		if _, err := conn.Exec(`DROP TRIGGER ` + trigger); err != nil {
			t.Fatalf("drop historical overlap trigger %s: %v", trigger, err)
		}
	}
	allocations := []migrationLocalAllocation{
		{id: 1, drawerID: 1, controllerID: 1, start: 20, count: 10, order: 0, createdAt: "2024-01-01", updatedAt: "2024-01-01"},
		{id: 2, drawerID: 2, controllerID: 1, start: 25, count: 5, order: 0, createdAt: "2024-01-02", updatedAt: "2024-01-02"},
	}
	seedCabinetWiringMigration(t, conn, []int64{100}, allocations)
	before := captureCabinetWiringSnapshot(t, conn)
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err == nil {
		t.Fatal("migration accepted overlapping historical controller-local allocations")
	}
	assertCabinetWiringSnapshotEqual(t, before, captureCabinetWiringSnapshot(t, conn))
}

func TestCabinetWiringMigrationRejectsNegativePrecedingCapacityWithoutMutation(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	allocations := []migrationLocalAllocation{{id: 1, drawerID: 1, controllerID: 2, start: 2, count: 1, order: 0, createdAt: "2024-01-01", updatedAt: "2024-01-01"}}
	seedCabinetWiringMigration(t, conn, []int64{-1, 100}, allocations)
	before := captureCabinetWiringSnapshot(t, conn)
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err == nil {
		t.Fatal("migration accepted a negative capacity in a preceding controller")
	}
	assertCabinetWiringSnapshotEqual(t, before, captureCabinetWiringSnapshot(t, conn))
}

func TestCabinetWiringMigrationZeroCapacityBoundary(t *testing.T) {
	t.Run("unused_zero_capacity_controller_is_valid", func(t *testing.T) {
		conn := openCabinetWiringFixture(t)
		allocation := migrationLocalAllocation{id: 1, drawerID: 1, controllerID: 2, start: 1, count: 1, order: 0, createdAt: "2024-01-01", updatedAt: "2024-01-01"}
		seedCabinetWiringMigration(t, conn, []int64{0, 100}, []migrationLocalAllocation{allocation})
		if err := goose.UpTo(conn, ".", cabinetWiringVersion); err != nil {
			t.Fatalf("zero-capacity unused predecessor should not shift pixels: %v", err)
		}
		var got int64
		if err := conn.QueryRow(`SELECT start_pixel FROM drawer_led_allocations WHERE id=1`).Scan(&got); err != nil || got != 1 {
			t.Fatalf("translated start=%d, err=%v; want 1", got, err)
		}
	})

	t.Run("zero_capacity_controller_cannot_own_allocation", func(t *testing.T) {
		conn := openCabinetWiringFixture(t)
		allocation := migrationLocalAllocation{id: 1, drawerID: 1, controllerID: 1, start: 1, count: 1, order: 0, createdAt: "2024-01-01", updatedAt: "2024-01-01"}
		seedCabinetWiringMigration(t, conn, []int64{0, 100}, []migrationLocalAllocation{allocation})
		before := captureCabinetWiringSnapshot(t, conn)
		if err := goose.UpTo(conn, ".", cabinetWiringVersion); err == nil {
			t.Fatal("migration accepted an allocation on a zero-capacity controller")
		}
		assertCabinetWiringSnapshotEqual(t, before, captureCabinetWiringSnapshot(t, conn))
	})
}

func TestCabinetWiringMigrationExtremeCapacityBoundaries(t *testing.T) {
	t.Run("maximum_representable_cabinet_pixel", func(t *testing.T) {
		conn := openCabinetWiringFixture(t)
		allocation := migrationLocalAllocation{id: 1, drawerID: 1, controllerID: 2, start: 10, count: 1, order: 0, createdAt: "2024-01-01", updatedAt: "2024-01-01"}
		seedCabinetWiringMigration(t, conn, []int64{math.MaxInt64 - 10, 10}, []migrationLocalAllocation{allocation})
		if err := goose.UpTo(conn, ".", cabinetWiringVersion); err != nil {
			t.Fatalf("exact MaxInt64 cabinet boundary should be representable: %v", err)
		}
		var got int64
		if err := conn.QueryRow(`SELECT start_pixel FROM drawer_led_allocations WHERE id=1`).Scan(&got); err != nil || got != math.MaxInt64 {
			t.Fatalf("translated start=%d, err=%v; want MaxInt64", got, err)
		}
	})

	t.Run("sum_overflow_refuses_without_mutation", func(t *testing.T) {
		conn := openCabinetWiringFixture(t)
		seedCabinetWiringMigration(t, conn, []int64{math.MaxInt64, 1}, nil)
		before := captureCabinetWiringSnapshot(t, conn)
		if err := goose.UpTo(conn, ".", cabinetWiringVersion); err == nil {
			t.Fatal("migration accepted controller capacities whose SQLite SUM overflows")
		}
		assertCabinetWiringSnapshotEqual(t, before, captureCabinetWiringSnapshot(t, conn))
	})
}

func TestCabinetWiringMigrationRejectsMissingMembershipWithoutMutation(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	conn.SetMaxOpenConns(1)
	seedCabinetWiringMigration(t, conn, []int64{100, 100}, nil)
	if _, err := conn.Exec(`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height) VALUES (1,1,'drawer',0,0,0,10,10)`); err != nil {
		t.Fatalf("insert drawer: %v", err)
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatalf("disable foreign keys for corrupted historical fixture: %v", err)
	}
	if _, err := conn.Exec(`DELETE FROM cabinet_controllers WHERE cabinet_id=1 AND controller_id=2`); err != nil {
		t.Fatalf("remove historical controller membership: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO drawer_led_allocations (id,cabinet_id,drawer_id,controller_id,start_pixel,pixel_count,display_order) VALUES (1,1,1,2,1,1,0)`); err != nil {
		t.Fatalf("insert historical allocation missing its membership: %v", err)
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("restore foreign-key enforcement: %v", err)
	}
	before := captureCabinetWiringSnapshot(t, conn)
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err == nil {
		t.Fatal("migration accepted an allocation without a matching cabinet membership")
	}
	assertCabinetWiringSnapshotEqual(t, before, captureCabinetWiringSnapshot(t, conn))
}

func TestCabinetWiringMigrationSafeMultiControllerDowngradeRestoresLocalRanges(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	capacities := []int64{100, 200, 300}
	allocations := []migrationLocalAllocation{
		{id: 11, drawerID: 1, controllerID: 1, start: 7, count: 3, order: 2, createdAt: "2024-01-01", updatedAt: "2024-02-01"},
		{id: 12, drawerID: 2, controllerID: 2, start: 17, count: 4, order: 3, createdAt: "2024-01-02", updatedAt: "2024-02-02"},
		{id: 13, drawerID: 3, controllerID: 3, start: 27, count: 5, order: 4, createdAt: "2024-01-03", updatedAt: "2024-02-03"},
	}
	seedCabinetWiringMigration(t, conn, capacities, allocations)
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err != nil {
		t.Fatalf("apply migration before safe downgrade: %v", err)
	}
	if err := goose.DownTo(conn, ".", 18); err != nil {
		t.Fatalf("safe multi-controller downgrade: %v", err)
	}
	rows, err := conn.Query(`SELECT id,drawer_id,controller_id,start_pixel,pixel_count,display_order,CAST(created_at AS TEXT),CAST(updated_at AS TEXT) FROM drawer_led_allocations ORDER BY id`)
	if err != nil {
		t.Fatalf("query downgraded allocations: %v", err)
	}
	defer rows.Close()
	for _, original := range allocations {
		var id, drawerID, controllerID, start, count, order int64
		var createdAt, updatedAt string
		if !rows.Next() {
			t.Fatalf("missing downgraded allocation %d", original.id)
		}
		if err := rows.Scan(&id, &drawerID, &controllerID, &start, &count, &order, &createdAt, &updatedAt); err != nil {
			t.Fatalf("scan downgraded allocation: %v", err)
		}
		if id != original.id || drawerID != original.drawerID || controllerID != original.controllerID || start != original.start || count != original.count || order != original.order || createdAt != original.createdAt || updatedAt != original.updatedAt {
			t.Errorf("downgraded allocation %d changed: got (%d,%d,%d,%d,%d,%d,%q,%q), want (%d,%d,%d,%d,%d,%d,%q,%q)", original.id, id, drawerID, controllerID, start, count, order, createdAt, updatedAt, original.id, original.drawerID, original.controllerID, original.start, original.count, original.order, original.createdAt, original.updatedAt)
		}
	}
	if rows.Next() {
		t.Fatal("downgrade produced extra allocations")
	}
}

func TestCabinetWiringMigrationUnsafeDowngradeRefusesWithoutMutation(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	seedCabinetWiringMigration(t, conn, []int64{100, 100}, nil)
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err != nil {
		t.Fatalf("apply migration before unsafe downgrade: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height) VALUES (1,1,'drawer',0,0,0,10,10)`); err != nil {
		t.Fatalf("insert drawer: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO drawer_led_allocations (id,cabinet_id,drawer_id,start_pixel,pixel_count,display_order) VALUES (1,1,1,99,3,0)`); err != nil {
		t.Fatalf("insert cross-controller cabinet-wide allocation: %v", err)
	}
	before := captureCabinetWiringSnapshot(t, conn)
	if err := goose.DownTo(conn, ".", 18); err == nil {
		t.Fatal("downgrade accepted a range crossing a controller boundary")
	}
	assertCabinetWiringSnapshotEqual(t, before, captureCabinetWiringSnapshot(t, conn))
}

func TestCabinetWiringMigrationConvertsControllerLocalRangesToCabinetPixels(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	statements := []string{
		`INSERT INTO controllers (id,name,ip_address,port,led_count) VALUES (1,'first','127.0.0.1',80,100)`,
		`INSERT INTO controllers (id,name,ip_address,port,led_count) VALUES (2,'second','127.0.0.2',80,50)`,
		`INSERT INTO cabinets (id,name,controller_id,coordinate_system) VALUES (1,'cabinet',1,'grid')`,
		`INSERT INTO cabinet_controllers (cabinet_id,controller_id,slot) VALUES (1,1,0)`,
		`INSERT INTO cabinet_controllers (cabinet_id,controller_id,slot) VALUES (1,2,1)`,
		`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height) VALUES (1,1,'first drawer',0,0,0,10,10)`,
		`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height) VALUES (2,1,'second drawer',1,10,0,10,10)`,
		`INSERT INTO drawer_led_allocations (id,cabinet_id,drawer_id,controller_id,start_pixel,pixel_count,display_order,created_at,updated_at) VALUES (7,1,1,1,81,10,0,'2024-01-01','2024-01-01')`,
		`INSERT INTO drawer_led_allocations (id,cabinet_id,drawer_id,controller_id,start_pixel,pixel_count,display_order,created_at,updated_at) VALUES (8,1,2,2,21,10,0,'2024-01-02','2024-01-02')`,
		`INSERT INTO physical_locate_sessions (controller_id,controller_address,controller_port,session_id,drawer_id,bin_id,owner_id,state,ranges_json,commands_json,fingerprint,color,brightness,last_operation,last_operation_at,last_successful_operation,last_successful_at,last_error) VALUES (1,'127.0.0.1',80,'legacy-session',1,1,1,'partial','[{"start":81,"count":10}]','[{"segment_id":10,"start":80,"stop":90}]','fingerprint','#ff0000',128,'partial',CURRENT_TIMESTAMP,'','', 'pending')`,
	}
	for _, statement := range statements {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("seed Task 048 fixture: %v\n%s", err, statement)
		}
	}

	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err != nil {
		t.Fatalf("apply cabinet wiring migration: %v", err)
	}

	rows, err := conn.Query(`SELECT id,cabinet_id,drawer_id,start_pixel,pixel_count,display_order FROM drawer_led_allocations ORDER BY id`)
	if err != nil {
		t.Fatalf("query migrated allocations: %v", err)
	}
	defer rows.Close()
	var got [][6]int64
	for rows.Next() {
		var row [6]int64
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4], &row[5]); err != nil {
			t.Fatalf("scan migrated allocation: %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read migrated allocations: %v", err)
	}
	want := [][6]int64{{7, 1, 1, 81, 10, 0}, {8, 1, 2, 121, 10, 0}}
	if len(got) != len(want) {
		t.Fatalf("migrated allocations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("migrated allocation[%d] = %v, want %v", i, got[i], want[i])
		}
	}

	var hasControllerColumn int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('drawer_led_allocations') WHERE name='controller_id'`).Scan(&hasControllerColumn); err != nil {
		t.Fatal(err)
	}
	if hasControllerColumn != 0 {
		t.Errorf("cabinet-wide allocations still have a controller_id column")
	}
	var targetCount, mayHaveReceived int
	if err := conn.QueryRow(`SELECT COUNT(*),COALESCE(MAX(may_have_received),0) FROM physical_locate_session_targets WHERE session_id='legacy-session'`).Scan(&targetCount, &mayHaveReceived); err != nil {
		t.Fatalf("read migrated Locate targets: %v", err)
	}
	if targetCount != 1 || mayHaveReceived != 1 {
		t.Errorf("migrated Locate target count/attempt marker = %d/%d, want 1/1", targetCount, mayHaveReceived)
	}
	var drawers, strings int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM drawers`).Scan(&drawers); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM cabinet_led_strings`).Scan(&strings); err != nil {
		t.Fatal(err)
	}
	if drawers != 2 || strings != 0 {
		t.Errorf("migration changed drawer identities or invented physical strings: drawers=%d strings=%d", drawers, strings)
	}
	var fkViolations int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&fkViolations); err != nil {
		t.Fatal(err)
	}
	if fkViolations != 0 {
		t.Errorf("migration left %d foreign-key violations", fkViolations)
	}
}

func TestCabinetWiringMigrationSafeSingleControllerDowngradeRestoresLocalRange(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	for _, statement := range []string{
		`INSERT INTO controllers (id,name,ip_address,port,led_count) VALUES (1,'first','127.0.0.1',80,100)`,
		`INSERT INTO cabinets (id,name,controller_id,coordinate_system) VALUES (1,'cabinet',1,'grid')`,
		`INSERT INTO cabinet_controllers (cabinet_id,controller_id,slot) VALUES (1,1,0)`,
		`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height) VALUES (1,1,'drawer',0,0,0,10,10)`,
		`INSERT INTO drawer_led_allocations (cabinet_id,drawer_id,controller_id,start_pixel,pixel_count,display_order) VALUES (1,1,1,81,10,0)`,
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("seed safe downgrade fixture: %v\n%s", err, statement)
		}
	}
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err != nil {
		t.Fatalf("apply cabinet wiring migration: %v", err)
	}
	if err := goose.DownTo(conn, ".", 18); err != nil {
		t.Fatalf("safe single-controller downgrade: %v", err)
	}
	var controllerID, start, count int64
	if err := conn.QueryRow(`SELECT controller_id,start_pixel,pixel_count FROM drawer_led_allocations`).Scan(&controllerID, &start, &count); err != nil {
		t.Fatal(err)
	}
	if controllerID != 1 || start != 81 || count != 10 {
		t.Fatalf("downgraded allocation=(controller %d,start %d,count %d), want (1,81,10)", controllerID, start, count)
	}
}

func TestCabinetWiringMigrationRejectsOutOfCapacityLegacyAllocation(t *testing.T) {
	conn := openCabinetWiringFixture(t)
	for _, statement := range []string{
		`INSERT INTO controllers (id,name,ip_address,port,led_count) VALUES (1,'first','127.0.0.1',80,100)`,
		`INSERT INTO cabinets (id,name,controller_id,coordinate_system) VALUES (1,'cabinet',1,'grid')`,
		`INSERT INTO cabinet_controllers (cabinet_id,controller_id,slot) VALUES (1,1,0)`,
		`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height) VALUES (1,1,'drawer',0,0,0,10,10)`,
		`INSERT INTO drawer_led_allocations (cabinet_id,drawer_id,controller_id,start_pixel,pixel_count,display_order) VALUES (1,1,1,98,5,0)`,
	} {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("seed invalid Task 048 fixture: %v\n%s", err, statement)
		}
	}
	before := captureCabinetWiringSnapshot(t, conn)
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err == nil {
		t.Fatal("migration accepted a saved allocation outside its controller capacity")
	}
	assertCabinetWiringSnapshotEqual(t, before, captureCabinetWiringSnapshot(t, conn))
	var legacyColumn int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('drawer_led_allocations') WHERE name='controller_id'`).Scan(&legacyColumn); err != nil {
		t.Fatal(err)
	}
	if legacyColumn != 1 {
		t.Fatalf("refused migration altered the allocation schema: controller_id column count=%d", legacyColumn)
	}
	var foreignKeys int
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("refused migration left foreign_keys=%d err=%v; want connection state restored", foreignKeys, err)
	}
	if _, err := conn.Exec(`SELECT COUNT(*) FROM drawers`); err != nil {
		t.Fatalf("connection unusable after safely refused migration: %v", err)
	}
}
