package wled

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCentreCabinetUsesOnlyAllocatedPixels(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	drawer, err := f.store.GetDrawer(ctx, f.drawer1)
	if err != nil {
		t.Fatal(err)
	}
	before := centrePixel(f, 20)
	request := centreRequest(f)
	request.ControllerID = 0
	request.CabinetID = drawer.CabinetID
	request.Function = "locate_cabinet"
	request.Count = 0
	session, err := f.service.StartTest(ctx, request, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, session.ID, 1)
	if session.DrawerID != 0 || session.BinID != 0 {
		t.Fatalf("cabinet test invented drawer/bin ownership: %+v", session)
	}
	if got := centrePixel(f, 9); got != ([3]int{255, 0, 0}) {
		t.Fatalf("allocated cabinet pixel=%v", got)
	}
	if got := centrePixel(f, 20); got != before {
		t.Fatalf("unassigned cabinet pixel changed: %v -> %v", before, got)
	}
}

func TestCentreDrawerStopUsesSelectedID(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	if _, err := f.database.Exec(`UPDATE bin_drawer_attachments SET drawer_id=? WHERE bin_id=?`, f.drawer2, f.bin1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AssignBinPixel(ctx, f.store, f.bin1, f.drawer2, f.controller, 9); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AssignBinPixel(ctx, f.store, f.bin2, f.drawer2, f.controller, 10); err != nil {
		t.Fatal(err)
	}
	first, err := f.service.LocateDrawerBin(ctx, f.drawer2, f.bin1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, first.ID, 1)
	second, err := f.service.LocateDrawerBin(ctx, f.drawer2, f.bin2, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, second.ID, 2)
	if err = f.service.StopDrawer(ctx, f.drawer2, 1); !errors.Is(err, ErrLocateSessionActive) {
		t.Fatalf("ambiguous drawer stop=%v", err)
	}
	secondColor := centrePixel(f, 10)
	if err = f.service.StopDrawerOperation(ctx, f.drawer2, 1, first.ID); err != nil {
		t.Fatal(err)
	}
	if got := centrePixel(f, 10); got != secondColor {
		t.Fatalf("selected Stop changed other owner's pixel: %v -> %v", secondColor, got)
	}
	if got := centrePixel(f, 9); got != ([3]int{}) {
		t.Fatalf("selected pixel not cleared: %v", got)
	}
}

func TestCentreStopCanCleanSavedOwnershipWithNewOutputDisabled(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	session, err := f.service.StartTest(ctx, centreRequest(f), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "false")
	if err = f.service.StopOperation(ctx, session.ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := centrePixel(f, 20); got != ([3]int{}) {
		t.Fatalf("persisted owned pixel not cleaned: %v", got)
	}
}

func TestCentrePhysicalLocateUsesConfiguredFunctionStyle(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	styles := DefaultFunctionSettings()
	styles["locate_part"] = FunctionStyle{Color: "#FF0000", Brightness: 17}
	styles["locate_bin"] = FunctionStyle{Color: "#00FF00", Brightness: 51}
	if err := SaveFunctionSettings(ctx, f.store, styles); err != nil {
		t.Fatal(err)
	}
	if _, err := f.database.Exec(`UPDATE settings SET color_locate='obsolete invalid legacy color'`); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateInventoryBin(ctx, f.partID, f.bin1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if session.Color != "#FF0000" || session.Brightness != 17 {
		t.Fatalf("part style=%+v", session)
	}
	if err = f.service.StopOperation(ctx, session.ID, 1); err != nil {
		t.Fatal(err)
	}
	session, err = f.service.LocateDrawerBin(ctx, f.drawer1, f.bin1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.service.StopOperation(ctx, session.ID, 1)
	if session.Color != "#00FF00" || session.Brightness != 51 {
		t.Fatalf("bin style=%+v", session)
	}
}

func TestCentrePatternFailureRemainsVisibleAfterCleanup(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	request := centreRequest(f)
	session, err := f.service.StartTest(ctx, request, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.StopOperation(ctx, session.ID, 1); err != nil {
		t.Fatal(err)
	}
	f.service.recordTestFailure(session.ID, errors.New("simulated sequence acknowledgement failure"))
	// Metadata identifies this as a sequence; no pattern is launched by reads.
	if _, err = f.database.Exec(`UPDATE physical_operation_metadata SET function='test_pixel:sequence' WHERE session_id=?`, session.ID); err != nil {
		t.Fatal(err)
	}
	before := len(f.mock.posts())
	operations, err := f.service.Operations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].State != "failed" || !strings.Contains(operations[0].LastError, "acknowledgement failure") {
		t.Fatalf("failed operation missing: %+v", operations)
	}
	time.Sleep(20 * time.Millisecond)
	if len(f.mock.posts()) != before {
		t.Fatal("status read restarted failed test")
	}
}
