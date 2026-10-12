package wled

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func addSecondaryPhysicalTarget(t *testing.T, f *physicalFixture, label string) (int64, *physicalMock) {
	t.Helper()
	ctx := context.Background()
	mock := newPhysicalMock()
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var port int64
	if _, err := fmt.Sscan(portText, &port); err != nil {
		t.Fatal(err)
	}
	controller, err := f.store.CreateController(ctx, db.CreateControllerParams{Name: label, IpAddress: host, Port: sql.NullInt64{Int64: port, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpdateControllerLedCount(ctx, db.UpdateControllerLedCountParams{LedCount: 200, ID: controller.ID, LedCount_2: 0}); err != nil {
		t.Fatal(err)
	}
	cabinet, err := f.store.GetCabinet(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateCabinetController(ctx, db.CreateCabinetControllerParams{CabinetID: cabinet.ID, ControllerID: controller.ID, Slot: 1}); err != nil {
		t.Fatal(err)
	}
	return controller.ID, mock
}

func makeCrossControllerDrawer(t *testing.T, f *physicalFixture) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	drawer, err := f.store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: 1, Label: "Cross-controller drawer", DisplayOrder: 4, X: 30, Y: 30, Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	bin, err := f.store.CreateBin(ctx, db.CreateBinParams{Name: "Cross-controller bin", Width: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: bin, DrawerID: drawer, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateDrawerLEDAllocation(ctx, db.CreateDrawerLEDAllocationParams{CabinetID: 1, DrawerID: drawer, StartPixel: 195, PixelCount: 12}); err != nil {
		t.Fatal(err)
	}
	return drawer, bin
}

func durablePhysicalTargets(t *testing.T, f *physicalFixture, sessionID string) map[int64]db.PhysicalLocateSessionTarget {
	t.Helper()
	targets, err := f.store.ListPhysicalLocateSessionTargets(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	byController := make(map[int64]db.PhysicalLocateSessionTarget, len(targets))
	for _, target := range targets {
		byController[target.ControllerID] = target
	}
	return byController
}

func TestPhysicalLocateCrossesControllersInOneDurableSession(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "secondary cross-controller")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	primaryBefore, secondaryBefore := len(f.mock.posts()), len(secondary.posts())

	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil {
		t.Fatalf("multi-controller Locate: %v", err)
	}
	if session.State != "active" || session.ControllerID != f.controller || !reflect.DeepEqual(session.AffectedControllerIDs, []int64{f.controller, secondaryID}) {
		t.Fatalf("logical session did not capture ordered controller targets: %+v", session)
	}
	primaryPosts, secondaryPosts := f.mock.posts()[primaryBefore:], secondary.posts()[secondaryBefore:]
	if len(primaryPosts) != 1 || len(secondaryPosts) != 1 {
		t.Fatalf("activation requests primary=%d secondary=%d, want one each", len(primaryPosts), len(secondaryPosts))
	}
	assertPixelCommand(t, primaryPosts[0], 20, 94, 100, []any{float64(128), float64(0), float64(0)})
	assertPixelCommand(t, secondaryPosts[0], 10, 0, 6, []any{float64(128), float64(0), float64(0)})
	targets, err := f.store.ListPhysicalLocateSessionTargets(ctx, session.ID)
	if err != nil || len(targets) != 2 {
		t.Fatalf("durable target records=%+v err=%v, want two", targets, err)
	}
	for _, target := range targets {
		if target.MayHaveReceived != 1 || target.Fingerprint == "" || target.RangesJson == "[]" || target.CommandsJson == "[]" {
			t.Errorf("controller target lacks durable activation/cleanup state: %+v", target)
		}
	}
	restarted := NewPhysicalLocateService(f.store, NewClient())
	recovered, found, err := restarted.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !found || recovered.State != "active" || !reflect.DeepEqual(recovered.AffectedControllerIDs, []int64{f.controller, secondaryID}) {
		t.Fatalf("restart did not recover the full logical multi-controller session: %+v found=%v err=%v", recovered, found, err)
	}
	if err := restarted.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("multi-controller Stop after restart: %v", err)
	}
	if len(f.mock.posts()) != primaryBefore+2 || len(secondary.posts()) != secondaryBefore+2 {
		t.Fatalf("Stop did not target both controllers: primary=%d secondary=%d", len(f.mock.posts())-primaryBefore, len(secondary.posts())-secondaryBefore)
	}
	assertPixelCommand(t, f.mock.posts()[primaryBefore+1], 20, 94, 100, []any{float64(0), float64(0), float64(0)})
	assertPixelCommand(t, secondary.posts()[secondaryBefore+1], 10, 0, 6, []any{float64(0), float64(0), float64(0)})
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "stopped" {
		t.Fatalf("multi-controller session status=%+v found=%v err=%v", status, ok, err)
	}
}

func TestPhysicalLocatePartialActivationAttemptsCleanupOnEveryTarget(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "secondary partial failure")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	// Controller 1's second write is its failed cleanup attempt; controller 2's
	// first write is a failed activation, but its cleanup must still be attempted.
	f.mock.failPostNumber = 2
	secondary.failPostNumber = 1
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err == nil || session.State != "partial" {
		t.Fatalf("partial activation result session=%+v err=%v, want durable partial failure", session, err)
	}
	if !strings.Contains(session.LastError, "cleanup not confirmed") && !strings.Contains(session.LastError, "controller #") {
		t.Errorf("partial failure omitted actionable cleanup state: %+v", session)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 2 {
		t.Fatalf("cleanup did not attempt both target controllers: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	if err := f.service.WithoutActiveSession(ctx, func() error { return nil }); !errors.Is(err, ErrLocateSessionActive) {
		t.Fatalf("configuration write during partial multi-controller session = %v, want busy refusal", err)
	}

	f.mock.failPostNumber = 0
	secondary.failPostNumber = 0
	if err := f.service.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("retry cleanup for every attempted controller: %v", err)
	}
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "stopped" {
		t.Fatalf("retried session status=%+v found=%v err=%v", status, ok, err)
	}
	if len(f.mock.posts()) != 3 || len(secondary.posts()) != 2 {
		t.Fatalf("retry cleanup did not target only the unresolved controller: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
}

func TestPhysicalLocateCleansHealthySecondControllerWhenFirstIsUnreachable(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "healthy second controller")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil || session.State != "active" {
		t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
	}

	f.server.Close()
	if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
		t.Fatal("Stop should retain an error for the unreachable first controller")
	}
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "partial" {
		t.Fatalf("unreachable-first cleanup status=%+v found=%v err=%v", status, ok, err)
	}
	if len(f.mock.posts()) != 1 || len(secondary.posts()) != 2 {
		t.Fatalf("unreachable first controller blocked or received unsafe cleanup: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	if !strings.Contains(status.LastError, fmt.Sprintf("controller #%d", f.controller)) {
		t.Fatalf("session error does not identify unreachable controller: %q", status.LastError)
	}
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 1 || !strings.Contains(targets[f.controller].LastError, "cannot verify") {
		t.Errorf("unreachable cleanup target was not durably retained: %+v", targets[f.controller])
	}
	if targets[secondaryID].MayHaveReceived != 0 || targets[secondaryID].LastError != "" {
		t.Errorf("healthy controller cleanup was not durably resolved: %+v", targets[secondaryID])
	}

	if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
		t.Fatal("retry should continue reporting the unresolved first controller")
	}
	if len(secondary.posts()) != 2 {
		t.Fatalf("retry resent cleanup to the already-resolved second controller: %d writes", len(secondary.posts()))
	}
}

func TestPhysicalLocateCleansFirstControllerWhenSecondIsUnverifiable(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "unverifiable second controller")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil || session.State != "active" {
		t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
	}
	secondary.mu.Lock()
	secondary.offline = true
	secondary.mu.Unlock()

	if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
		t.Fatal("Stop should retain an error for the unverifiable second controller")
	}
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "partial" {
		t.Fatalf("unverifiable-second cleanup status=%+v found=%v err=%v", status, ok, err)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 1 {
		t.Fatalf("second-controller verification failure did not isolate targeted cleanup: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	if !strings.Contains(status.LastError, fmt.Sprintf("controller #%d", secondaryID)) {
		t.Fatalf("session error does not identify unverifiable controller: %q", status.LastError)
	}
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 0 || targets[f.controller].LastError != "" {
		t.Errorf("healthy first controller cleanup was not durably resolved: %+v", targets[f.controller])
	}
	if targets[secondaryID].MayHaveReceived != 1 || !strings.Contains(targets[secondaryID].LastError, "cannot verify") {
		t.Errorf("unverifiable second target was not durably retained: %+v", targets[secondaryID])
	}
}

func TestPhysicalLocateRetriesOnlyControllerWithPartialCleanupFailure(t *testing.T) {
	f := newPhysicalFixture(t)
	ctx := context.Background()
	secondaryID, secondary := addSecondaryPhysicalTarget(t, f, "partial cleanup failure")
	drawer, bin := makeCrossControllerDrawer(t, f)
	if err := f.service.SaveControllerMappingVerification(ctx, secondaryID, true); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.LocateDrawerBin(ctx, drawer, bin, 7)
	if err != nil || session.State != "active" {
		t.Fatalf("activate cross-controller Locate: session=%+v err=%v", session, err)
	}
	secondary.failPostNumber = 2 // fail only the second controller's cleanup write

	if err := f.service.StopDrawer(ctx, drawer, 7); err == nil {
		t.Fatal("Stop should report the failed targeted cleanup")
	}
	status, ok, err := f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "partial" {
		t.Fatalf("partial-cleanup status=%+v found=%v err=%v", status, ok, err)
	}
	targets := durablePhysicalTargets(t, f, session.ID)
	if targets[f.controller].MayHaveReceived != 0 || targets[f.controller].LastError != "" {
		t.Errorf("successful first controller cleanup was not durably resolved: %+v", targets[f.controller])
	}
	if targets[secondaryID].MayHaveReceived != 1 || !strings.Contains(targets[secondaryID].LastError, "targeted cleanup failed") {
		t.Errorf("failed second controller cleanup was not retained with its error: %+v", targets[secondaryID])
	}
	if !strings.Contains(status.LastError, fmt.Sprintf("controller #%d", secondaryID)) {
		t.Errorf("session error does not identify the failed controller: %q", status.LastError)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 2 {
		t.Fatalf("initial partial cleanup writes primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}

	secondary.failPostNumber = 0
	if err := f.service.StopDrawer(ctx, drawer, 7); err != nil {
		t.Fatalf("retry unresolved second-controller cleanup: %v", err)
	}
	if len(f.mock.posts()) != 2 || len(secondary.posts()) != 3 {
		t.Fatalf("retry did not target only unresolved controller: primary=%d secondary=%d", len(f.mock.posts()), len(secondary.posts()))
	}
	status, ok, err = f.service.SessionStatusForDrawer(ctx, drawer)
	if err != nil || !ok || status.State != "stopped" {
		t.Fatalf("retried cleanup status=%+v found=%v err=%v", status, ok, err)
	}
}
