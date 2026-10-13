package hardware

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func controllerParams(name, address string, port int64) db.CreateControllerParams {
	return db.CreateControllerParams{Name: name, IpAddress: address, Port: sql.NullInt64{Int64: port, Valid: true}}
}

func TestValidateControllerSettings(t *testing.T) {
	for _, port := range []int64{0, -1, 65536, 70000} {
		t.Run("port_"+strconvFormatInt(port), func(t *testing.T) {
			if _, _, err := ValidateControllerSettings("C", "192.0.2.1", port); !errors.Is(err, ErrInvalidControllerSettings) {
				t.Fatalf("port %d error = %v, want invalid settings", port, err)
			}
		})
	}
	for _, address := range []string{
		"", "http://wled.local", "wled.local:8080", "[2001:db8::1]:80", "user:pass@wled.local",
		"wled.local/path", "256.1.1.1", "999.999.999.999", "-bad.local", "bad-.local", "bad..local",
		"bad_name.local", "bad host", "host.local.",
	} {
		t.Run("address_"+strings.ReplaceAll(address, "/", "_"), func(t *testing.T) {
			if _, _, err := ValidateControllerSettings("C", address, 80); !errors.Is(err, ErrInvalidControllerSettings) {
				t.Fatalf("address %q error = %v, want invalid settings", address, err)
			}
		})
	}
	for _, address := range []string{"192.0.2.1", "2001:db8::1", "wled.local", "shelf-1.home", "WLED"} {
		if _, gotAddress, err := ValidateControllerSettings(" C ", address, 65535); err != nil || gotAddress != address {
			t.Errorf("valid endpoint %q was rejected/changed: address=%q err=%v", address, gotAddress, err)
		}
	}
	if _, _, err := ValidateControllerSettings("  ", "wled.local", 80); !errors.Is(err, ErrInvalidControllerSettings) {
		t.Fatalf("empty name error = %v, want invalid settings", err)
	}
}

func TestControllerCreateAndImportRejectDuplicateEndpoints(t *testing.T) {
	service, store, conn := setupTest(t)
	defer conn.Close()
	ctx := context.Background()
	first, err := service.CreateController(ctx, controllerParams("First", "WLED.local", 80))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateController(ctx, controllerParams("Second", "wled.LOCAL", 80)); !errors.Is(err, ErrDuplicateController) {
		t.Fatalf("duplicate create error = %v, want ErrDuplicateController", err)
	}
	payload := []byte(`{"version":"1.0","controller":{"name":"Imported","ip_address":"wled.local","port":80},"containers":[],"bins":[]}`)
	if _, err := service.ImportConfig(ctx, "", "", 0, payload); !errors.Is(err, ErrDuplicateController) {
		t.Fatalf("duplicate import error = %v, want ErrDuplicateController", err)
	}
	controllers, err := store.GetControllers(ctx)
	if err != nil || len(controllers) != 1 || controllers[0].ID != first.ID {
		t.Fatalf("duplicate operation changed controllers: %#v err=%v", controllers, err)
	}
}

func TestImportConfigValidatesControllerEndpointAndPort(t *testing.T) {
	service, _, conn := setupTest(t)
	defer conn.Close()
	invalidPayloads := []string{
		`{"version":"1.0","controller":{"name":"C","ip_address":"http://wled.local","port":80},"containers":[],"bins":[]}`,
		`{"version":"1.0","controller":{"name":"C","ip_address":"wled.local:80","port":80},"containers":[],"bins":[]}`,
		`{"version":"1.0","controller":{"name":"C","ip_address":"wled.local","port":0},"containers":[],"bins":[]}`,
		`{"version":"1.0","controller":{"name":"C","ip_address":"wled.local","port":-1},"containers":[],"bins":[]}`,
		`{"version":"1.0","controller":{"name":"C","ip_address":"wled.local","port":65536},"containers":[],"bins":[]}`,
		`{"version":"1.0","controller":{"name":"C","ip_address":"wled.local","port":70000},"containers":[],"bins":[]}`,
		`{"version":"1.0","controller":{"name":"C","ip_address":"wled.local","port":"nonnumeric"},"containers":[],"bins":[]}`,
		`{"version":"1.0","controller":{"name":"C","ip_address":"wled.local"},"containers":[],"bins":[]}`,
		`{"version":"1.0","controller":{"name":"","ip_address":"wled.local","port":80},"containers":[],"bins":[]}`,
	}
	for _, payload := range invalidPayloads {
		if _, err := service.ImportConfig(context.Background(), "", "", 0, []byte(payload)); err == nil {
			t.Errorf("ImportConfig(%s) succeeded, want validation error", payload)
		}
	}
}

func TestUpdateControllerRejectsDuplicateEndpoint(t *testing.T) {
	service, store, conn := setupTest(t)
	defer conn.Close()
	ctx := context.Background()
	first, err := service.CreateController(ctx, controllerParams("First", "192.0.2.40", 8080))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.CreateController(ctx, controllerParams("Second", "192.0.2.41", 8080))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateController(ctx, second.ID, controllerParams("Second", "192.0.2.40", 8080)); !errors.Is(err, ErrDuplicateController) {
		t.Fatalf("duplicate edit error = %v, want ErrDuplicateController", err)
	}
	unchanged, err := store.GetController(ctx, second.ID)
	if err != nil || unchanged.ID != second.ID || unchanged.IpAddress != "192.0.2.41" {
		t.Fatalf("rejected duplicate edit changed record: %+v err=%v (other ID %d)", unchanged, err, first.ID)
	}
}

func TestUpdateControllerPreservesIdentityAndRelationships(t *testing.T) {
	service, store, conn := setupTest(t)
	defer conn.Close()
	ctx := context.Background()
	controller, err := store.CreateController(ctx, controllerParams("Before", "192.0.2.10", 80))
	if err != nil {
		t.Fatal(err)
	}
	containerID, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Legacy drawer", ControllerID: controller.ID, SegmentID: 1, LedStart: 4, LedCount: 8,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":8}`, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	binID, err := store.CreateBin(ctx, db.CreateBinParams{
		Name: "Bin A", ContainerID: sql.NullInt64{Int64: containerID, Valid: true},
		LedIndex: sql.NullInt64{Int64: 6, Valid: true}, Width: sql.NullInt64{Int64: 2, Valid: true},
		GridX: sql.NullInt64{Int64: 1, Valid: true}, GridY: sql.NullInt64{Int64: 2, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	partID, err := store.CreatePart(ctx, db.CreatePartParams{Name: "Stocked part"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}, Quantity: 7}); err != nil {
		t.Fatal(err)
	}
	cabinetID, err := store.CreateCabinet(ctx, db.CreateCabinetParams{Name: "Cabinet", CoordinateSystem: "grid"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinetID, ControllerID: controller.ID, Slot: 0}); err != nil {
		t.Fatal(err)
	}
	drawerID, err := store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer 1", Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	allocationID, err := store.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: cabinetID, DrawerID: drawerID, StartPixel: 21, PixelCount: 8})
	if err != nil {
		t.Fatal(err)
	}
	stringID, err := store.CreateCabinetLEDString(ctx, db.CreateCabinetLEDStringParams{CabinetID: cabinetID, ControllerID: controller.ID, Name: "String 1", DisplayOrder: 0, PixelCount: 64})
	if err != nil {
		t.Fatal(err)
	}
	pathID, err := store.CreateCabinetLEDPathSection(ctx, db.CreateCabinetLEDPathSectionParams{StringID: stringID, DisplayOrder: 0, PixelCount: 64, PointsJson: `[]`})
	if err != nil {
		t.Fatal(err)
	}

	if err := service.UpdateController(ctx, controller.ID, controllerParams("After", "wled.home", 8080)); err != nil {
		t.Fatalf("UpdateController: %v", err)
	}
	updated, err := store.GetController(ctx, controller.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != controller.ID || updated.Name != "After" || updated.IpAddress != "wled.home" || !updated.Port.Valid || updated.Port.Int64 != 8080 {
		t.Fatalf("updated controller = %+v", updated)
	}
	var cabinetMembership, allocationCount, wiringCount, containerCount, binCount, assignmentCount int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM cabinet_controllers WHERE cabinet_id=? AND controller_id=?`, cabinetID, controller.ID).Scan(&cabinetMembership); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM drawer_led_allocations WHERE id=? AND drawer_id=? AND start_pixel=21 AND pixel_count=8`, allocationID, drawerID).Scan(&allocationCount); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM cabinet_led_strings s JOIN cabinet_led_path_sections p ON p.string_id=s.id WHERE s.id=? AND p.id=? AND s.controller_id=?`, stringID, pathID, controller.ID).Scan(&wiringCount); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM containers WHERE id=? AND controller_id=?`, containerID, controller.ID).Scan(&containerCount); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM bins WHERE id=? AND container_id=?`, binID, containerID).Scan(&binCount); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM part_assignments WHERE part_id=? AND bin_id=? AND quantity=7`, partID, binID).Scan(&assignmentCount); err != nil {
		t.Fatal(err)
	}
	if cabinetMembership != 1 || allocationCount != 1 || wiringCount != 1 || containerCount != 1 || binCount != 1 || assignmentCount != 1 {
		t.Fatalf("controller edit lost related data: membership=%d allocation=%d wiring=%d container=%d bin=%d stock=%d", cabinetMembership, allocationCount, wiringCount, containerCount, binCount, assignmentCount)
	}
}

func TestUpdateControllerLocateOwnershipAllowsNameOnlyAndBlocksEndpoint(t *testing.T) {
	service, store, conn := setupTest(t)
	defer conn.Close()
	ctx := context.Background()
	controller, err := store.CreateController(ctx, controllerParams("Before", "192.0.2.20", 80))
	if err != nil {
		t.Fatal(err)
	}
	otherController, err := store.CreateController(ctx, controllerParams("Other", "Other.local", 80))
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.ExecContext(ctx, `INSERT INTO physical_locate_sessions
		(controller_id,controller_address,controller_port,session_id,drawer_id,bin_id,owner_id,state,ranges_json,commands_json,fingerprint,color,brightness,last_operation,last_operation_at,last_successful_operation,last_successful_at,last_error)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, 'activation',CURRENT_TIMESTAMP,'activation',CURRENT_TIMESTAMP,'unresolved')`,
		controller.ID, "192.0.2.20", 80, "editing-active", 1, 1, 1, "partial", `[]`, `[]`, "fingerprint", "#ff0000", 128)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO physical_locate_session_targets
		(session_id,controller_id,controller_address,controller_port,ranges_json,commands_json,fingerprint,may_have_received,last_error)
		VALUES (?,?,?,?,?,?,?,0,'')`, "editing-active", otherController.ID, "Other.local", 80, `[]`, `[]`, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateController(ctx, otherController.ID, controllerParams("Other changed", "other.local", 80)); !errors.Is(err, db.ErrPhysicalLocateActive) {
		t.Fatalf("secondary active target endpoint edit = %v, want ErrPhysicalLocateActive", err)
	}
	if err := service.UpdateController(ctx, controller.ID, controllerParams("Renamed", "192.0.2.20", 80)); err != nil {
		t.Fatalf("name-only edit during Locate: %v", err)
	}
	if err := service.UpdateController(ctx, controller.ID, controllerParams("Changed", "192.0.2.21", 80)); !errors.Is(err, db.ErrPhysicalLocateActive) {
		t.Fatalf("endpoint change during Locate = %v, want ErrPhysicalLocateActive", err)
	}
	stored, err := store.GetController(ctx, controller.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID != controller.ID || stored.Name != "Renamed" || stored.IpAddress != "192.0.2.20" {
		t.Fatalf("rejected endpoint edit changed controller: %+v", stored)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE physical_locate_sessions SET state='stopped' WHERE session_id='editing-active'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO physical_locate_session_targets
		(session_id,controller_id,controller_address,controller_port,ranges_json,commands_json,fingerprint,may_have_received,last_error)
		VALUES (?,?,?,?,?,?,?,1,'cleanup unresolved')`, "editing-active", controller.ID, "192.0.2.20", 80, `[]`, `[]`, "fingerprint"); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateController(ctx, controller.ID, controllerParams("Changed", "192.0.2.21", 80)); !errors.Is(err, db.ErrPhysicalLocateActive) {
		t.Fatalf("unresolved target ownership edit = %v, want ErrPhysicalLocateActive", err)
	}
}

func TestConcurrentLocateReservationAndControllerEditSerialize(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller-edit.sqlite")
	conn, err := db.Open("file:" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(conn)
	service := NewService(store, wled.NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	controller, err := store.CreateController(ctx, controllerParams("C", "192.0.2.30", 80))
	if err != nil {
		t.Fatal(err)
	}
	claimHeld := make(chan struct{})
	releaseClaim := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseClaim) })
	claimDone := make(chan error, 1)
	go func() {
		claimDone <- db.WithOperationClaim(ctx, store, func(q db.Querier) error {
			_, err := q.SavePhysicalLocateSession(ctx, db.SavePhysicalLocateSessionParams{
				ControllerID: controller.ID, ControllerAddress: "192.0.2.30", ControllerPort: 80,
				SessionID: "concurrent-locate", DrawerID: 1, BinID: 1, OwnerID: 1, State: "activating",
				RangesJson: `[]`, CommandsJson: `[]`, Fingerprint: "fp", Color: "#ff0000", Brightness: 128,
				LastOperation: "activation", LastOperationAt: time.Now(), LastSuccessfulOperation: "", LastSuccessfulAt: "", LastError: "",
			})
			if err != nil {
				return err
			}
			close(claimHeld)
			<-releaseClaim
			return nil
		})
	}()
	select {
	case <-claimHeld:
	case <-time.After(5 * time.Second):
		t.Fatal("Locate reservation did not acquire its SQLite claim")
	}

	editDone := make(chan error, 1)
	go func() {
		editDone <- service.UpdateController(ctx, controller.ID, controllerParams("Changed", "192.0.2.31", 80))
	}()
	select {
	case err := <-editDone:
		t.Fatalf("controller edit completed before Locate reservation released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(releaseClaim) })
	if err := <-claimDone; err != nil {
		t.Fatalf("Locate reservation claim: %v", err)
	}
	select {
	case err := <-editDone:
		if !errors.Is(err, db.ErrPhysicalLocateActive) {
			t.Fatalf("serialized controller edit = %v, want active Locate rejection", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("controller edit did not finish after claim release")
	}
	stored, err := store.GetController(ctx, controller.ID)
	if err != nil || stored.IpAddress != "192.0.2.30" {
		t.Fatalf("concurrent edit changed endpoint: %+v err=%v", stored, err)
	}
}

// strconvFormatInt avoids fmt formatting noise in subtest names.
func strconvFormatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
