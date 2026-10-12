package db_test

import (
	"database/sql"
	"path/filepath"
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
	if err := goose.UpTo(conn, ".", cabinetWiringVersion); err == nil {
		t.Fatal("migration accepted a saved allocation outside its controller capacity")
	}
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
