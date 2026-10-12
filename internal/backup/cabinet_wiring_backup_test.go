package backup

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestCabinetWiringBackupRoundTripPreservesOrderedStringsAndPaths(t *testing.T) {
	source := physNewFixture(t, "cabinet_wiring_backup_source")
	source.initSettings()
	physSeedLegacy(t, source.conn)
	physSeedPhysical(t, source.conn)
	physExec(t, source.conn, `UPDATE cabinet_controllers SET strings_configured=1 WHERE cabinet_id=1 AND controller_id=1`)
	physExec(t, source.conn, `INSERT INTO cabinet_led_strings (id,cabinet_id,controller_id,name,display_order,pixel_count,layout_complete,created_at,updated_at) VALUES (51,1,1,'Four-pass serpentine',0,100,1,'2024-02-01','2024-02-01')`)
	physExec(t, source.conn, `INSERT INTO cabinet_led_strings (id,cabinet_id,controller_id,name,display_order,pixel_count,layout_complete,created_at,updated_at) VALUES (52,1,1,'Vertical continuation',1,200,0,'2024-02-02','2024-02-02')`)
	physExec(t, source.conn, `INSERT INTO cabinet_led_path_sections (id,string_id,display_order,pixel_count,points_json,created_at,updated_at) VALUES (61,51,0,50,'[{"x":1,"y":1},{"x":10,"y":1}]','2024-02-03','2024-02-03')`)
	physExec(t, source.conn, `INSERT INTO cabinet_led_path_sections (id,string_id,display_order,pixel_count,points_json,created_at,updated_at) VALUES (62,51,1,50,'[{"x":10,"y":2},{"x":1,"y":2}]','2024-02-04','2024-02-04')`)

	before := physReadPhysicalRows(t, source.conn)
	archive := source.export()
	envelope := ccDecodeEnvelope(t, archive)
	if envelope.PhysicalStorage.AllocationCoordinateSpace != "cabinet" || !envelope.PhysicalStorage.CabinetWiringDeclared {
		t.Fatalf("new export did not declare cabinet-wide wiring coordinates: %+v", envelope.PhysicalStorage)
	}
	if len(envelope.PhysicalStorage.CabinetLEDStrings) != 2 || len(envelope.PhysicalStorage.CabinetLEDPathSections) != 2 {
		t.Fatalf("exported wiring graph has %d strings and %d path sections", len(envelope.PhysicalStorage.CabinetLEDStrings), len(envelope.PhysicalStorage.CabinetLEDPathSections))
	}

	target := physNewFixture(t, "cabinet_wiring_backup_target")
	target.initSettings()
	if err := target.restore(archive); err != nil {
		t.Fatalf("restore wiring backup: %v", err)
	}
	if got := physReadPhysicalRows(t, target.conn); !reflect.DeepEqual(before, got) {
		t.Fatalf("wiring graph changed across backup roundtrip:\nwant=%+v\ngot=%+v", before, got)
	}
}

func TestTask048ArchiveControllerLocalRangesDecodeAsCabinetWide(t *testing.T) {
	source := physNewFixture(t, "task048_local_range_archive")
	source.initSettings()
	ccSeedMultiController(t, source.conn)
	archive := source.export()
	entries := physZipEntries(t, archive)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(entries[currentStorageManifestName], &top); err != nil {
		t.Fatal(err)
	}
	var physical map[string]json.RawMessage
	if err := json.Unmarshal(top[storageManifestPhysicalField], &physical); err != nil {
		t.Fatal(err)
	}
	delete(physical, "cabinet_led_strings")
	delete(physical, "cabinet_led_path_sections")
	delete(physical, "allocation_coordinate_space")
	env := ccDecodeEnvelope(t, archive)
	members := resolveCabinetControllers(env.PhysicalStorage)
	controllers := make(map[int64]db.Controller)
	for _, controller := range env.Legacy.Controllers {
		controllers[controller.ID] = controller
	}
	var legacyAllocations []struct {
		ID           int64     `json:"id"`
		CabinetID    int64     `json:"cabinet_id"`
		DrawerID     int64     `json:"drawer_id"`
		ControllerID int64     `json:"controller_id"`
		StartPixel   int64     `json:"start_pixel"`
		PixelCount   int64     `json:"pixel_count"`
		DisplayOrder int64     `json:"display_order"`
		CreatedAt    time.Time `json:"created_at"`
		UpdatedAt    time.Time `json:"updated_at"`
	}
	for _, allocation := range env.PhysicalStorage.LEDAllocations {
		var offset int64
		found := false
		for _, member := range members {
			if member.CabinetID != allocation.CabinetID {
				continue
			}
			capacity := controllers[member.ControllerID].LedCount
			if allocation.StartPixel > offset && allocation.StartPixel+allocation.PixelCount-1 <= offset+capacity {
				legacyAllocations = append(legacyAllocations, struct {
					ID           int64     `json:"id"`
					CabinetID    int64     `json:"cabinet_id"`
					DrawerID     int64     `json:"drawer_id"`
					ControllerID int64     `json:"controller_id"`
					StartPixel   int64     `json:"start_pixel"`
					PixelCount   int64     `json:"pixel_count"`
					DisplayOrder int64     `json:"display_order"`
					CreatedAt    time.Time `json:"created_at"`
					UpdatedAt    time.Time `json:"updated_at"`
				}{allocation.ID, allocation.CabinetID, allocation.DrawerID, member.ControllerID, allocation.StartPixel - offset, allocation.PixelCount, allocation.DisplayOrder, allocation.CreatedAt, allocation.UpdatedAt})
				found = true
				break
			}
			offset += capacity
		}
		if !found {
			t.Fatalf("fixture range %+v does not fit one Task 048 controller-local span; members=%+v controllers=%+v", allocation, members, controllers)
		}
	}
	localJSON, err := json.Marshal(legacyAllocations)
	if err != nil {
		t.Fatal(err)
	}
	physical["drawer_led_allocations"] = localJSON
	physicalJSON, err := json.Marshal(physical)
	if err != nil {
		t.Fatal(err)
	}
	top[storageManifestPhysicalField] = physicalJSON
	legacyJSON, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	oldArchive := physBuildZip(t, [][2]string{{currentStorageManifestName, string(legacyJSON)}})
	decoded := ccDecodeEnvelope(t, oldArchive)
	if decoded.PhysicalStorage.AllocationCoordinateSpace != "cabinet" || decoded.PhysicalStorage.CabinetWiringDeclared {
		t.Fatalf("old Task 048 archive flags were not interpreted correctly: %+v", decoded.PhysicalStorage)
	}
	if !reflect.DeepEqual(decoded.PhysicalStorage.LEDAllocations, env.PhysicalStorage.LEDAllocations) {
		t.Fatalf("Task 048 local ranges did not round-trip to their original cabinet-wide spans:\nwant=%+v\ngot=%+v", env.PhysicalStorage.LEDAllocations, decoded.PhysicalStorage.LEDAllocations)
	}
}

func TestLegacyTask048AllocationBackupConvertsControllerLocalRangesBySavedOrder(t *testing.T) {
	timestamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	legacy := []db.Controller{
		{ID: 1, LedCount: 100},
		{ID: 2, LedCount: 50},
	}
	env := StorageManifest{
		Legacy: Manifest{Controllers: legacy},
		PhysicalStorage: PhysicalStorageSnapshot{
			Cabinets: []db.Cabinet{{ID: 1, Name: "Cabinet", ControllerID: sql.NullInt64{Int64: 1, Valid: true}}},
			CabinetControllers: []db.CabinetController{
				{CabinetID: 1, ControllerID: 1, Slot: 0, CreatedAt: timestamp, UpdatedAt: timestamp},
				{CabinetID: 1, ControllerID: 2, Slot: 1, CreatedAt: timestamp, UpdatedAt: timestamp},
			},
			CabinetControllersDeclared: true,
			LEDAllocations:             []db.DrawerLedAllocation{{ID: 7, CabinetID: 1, DrawerID: 9, StartPixel: 21, PixelCount: 10, DisplayOrder: 0}},
		},
	}
	old := []struct {
		ID           int64 `json:"id"`
		CabinetID    int64 `json:"cabinet_id"`
		DrawerID     int64 `json:"drawer_id"`
		ControllerID int64 `json:"controller_id"`
		StartPixel   int64 `json:"start_pixel"`
		PixelCount   int64 `json:"pixel_count"`
		DisplayOrder int64 `json:"display_order"`
	}{{ID: 7, CabinetID: 1, DrawerID: 9, ControllerID: 2, StartPixel: 21, PixelCount: 10}}
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := convertLegacyAllocationCoordinates(&env, map[string]json.RawMessage{"drawer_led_allocations": raw}); err != nil {
		t.Fatalf("convert Task 048 local range: %v", err)
	}
	if got := env.PhysicalStorage.LEDAllocations[0].StartPixel; got != 121 {
		t.Fatalf("translated cabinet-wide start=%d, want 121 (controller 2 begins at 101)", got)
	}
	if env.PhysicalStorage.AllocationCoordinateSpace != "cabinet" {
		t.Fatalf("converted coordinate space=%q, want cabinet", env.PhysicalStorage.AllocationCoordinateSpace)
	}
}

func TestBackupRestoreRefusesWhileDurableLocateMayOwnPixels(t *testing.T) {
	source := ccSingleControllerSource(t, "active_locate_restore_source")
	archive := source.export()
	target := physNewFixture(t, "active_locate_restore_target")
	target.initSettings()
	physSeedLegacy(t, target.conn)
	physSeedPhysical(t, target.conn)
	physSeedUploadSentinels(t, target.uploadsDir)
	physExec(t, target.conn, `INSERT INTO physical_locate_sessions (controller_id,controller_address,controller_port,session_id,drawer_id,bin_id,owner_id,state,ranges_json,commands_json,fingerprint,color,brightness,last_operation,last_operation_at,last_successful_operation,last_successful_at,last_error) VALUES (1,'192.168.1.10',80,'active-locate',1,1,1,'partial','[]','[]','fingerprint','#0000ff',128,'cleanup pending',CURRENT_TIMESTAMP,'','', 'offline')`)
	before := physTakeSnapshot(t, target)
	uploads := physUploadsSnapshot(t, target.uploadsDir)
	if err := target.restore(archive); err == nil {
		t.Fatal("restore accepted a database with a durable active/partial Locate session")
	} else {
		var conflict *ActivePhysicalLocateConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("restore error=%v, want ActivePhysicalLocateConflictError", err)
		}
	}
	physAssertUnchanged(t, "active-locate-restore-conflict", target, before)
	if got := physUploadsSnapshot(t, target.uploadsDir); !reflect.DeepEqual(got, uploads) {
		t.Errorf("uploads changed by refused active-session restore: want %v got %v", uploads, got)
	}
	var state string
	if err := target.conn.QueryRow(`SELECT state FROM physical_locate_sessions WHERE session_id='active-locate'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "partial" {
		t.Fatalf("durable Locate state changed during refused restore: %q", state)
	}
}
