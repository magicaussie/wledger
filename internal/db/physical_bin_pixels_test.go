package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

func TestPhysicalBinPixelsRollbackRequiresExplicitClearing(t *testing.T) {
	conn, err := db.Open("file:" + t.TempDir() + "/bin-pixels.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	goose.SetBaseFS(schema.Migrations)
	if err = goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	// Isolate migration 026; later migrations belong to separate tasks.
	if err = goose.UpTo(conn, ".", 26); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO controllers(id,name,ip_address,led_count) VALUES(1,'configured','192.0.2.1',8)`,
		`INSERT INTO cabinets(id,name,controller_id) VALUES(1,'cabinet',1)`,
		`INSERT INTO cabinet_controllers(cabinet_id,controller_id,slot) VALUES(1,1,0)`,
		`INSERT INTO drawers(id,cabinet_id,label,x,y,width,height) VALUES(1,1,'drawer',0,0,10,10)`,
		`INSERT INTO bins(id,name) VALUES(1,'one'),(2,'two')`,
		`INSERT INTO drawer_led_allocations(id,cabinet_id,drawer_id,start_pixel,pixel_count) VALUES(1,1,1,1,8)`,
	} {
		if _, err = conn.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	store := db.NewStore(conn)
	for _, id := range []int64{1, 2} {
		if err = store.SavePhysicalBinPixel(ctx, db.PhysicalBinPixel{BinID: id, DrawerID: 1, AllocationID: 1, Offset: id - 1, ControllerID: 1, Pixel: id - 1, TopologyFingerprint: strings.Repeat("a", 64)}); err != nil {
			t.Fatal(err)
		}
	}
	if err = goose.DownTo(conn, ".", 25); err == nil || !strings.Contains(err.Error(), "clear_physical_bin_pixel_overrides_before_rollback") {
		t.Fatalf("populated rollback=%v", err)
	}
	version, err := goose.GetDBVersion(conn)
	if err != nil || version != 26 {
		t.Fatalf("rollback changed migration version: %d %v", version, err)
	}
	rows, err := store.ListPhysicalBinPixels(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rollback lost overrides: %+v %v", rows, err)
	}
	if _, err = conn.Exec(`UPDATE drawer_led_allocations SET start_pixel=2 WHERE id=1`); err == nil {
		t.Fatal("failed rollback removed allocation guard")
	}
	if err = store.DeletePhysicalBinPixel(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err = store.DeletePhysicalBinPixel(ctx, 1); err != nil {
		t.Fatalf("clear must be idempotent: %v", err)
	}
	rows, err = store.ListPhysicalBinPixels(ctx)
	if err != nil || len(rows) != 1 || rows[0].BinID != 2 {
		t.Fatalf("single-bin delete changed other override: %+v %v", rows, err)
	}
	if err = goose.DownTo(conn, ".", 25); err == nil {
		t.Fatal("rollback discarded remaining bin override")
	}
	if err = store.DeletePhysicalBinPixel(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err = goose.DownTo(conn, ".", 25); err != nil {
		t.Fatalf("empty rollback should succeed: %v", err)
	}
	version, err = goose.GetDBVersion(conn)
	if err != nil || version != 25 {
		t.Fatalf("rollback version=%d err=%v", version, err)
	}
	var count int
	if err = conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='physical_bin_pixels'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("table remains after empty rollback: %d %v", count, err)
	}
}
