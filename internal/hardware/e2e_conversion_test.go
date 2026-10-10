package hardware

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// payloadRecorder captures WLED JSON bodies sent to a test controller.
type payloadRecorder struct {
	mu       sync.Mutex
	payloads []string
}

func (p *payloadRecorder) add(s string) {
	p.mu.Lock()
	p.payloads = append(p.payloads, s)
	p.mu.Unlock()
}

func (p *payloadRecorder) waitFor(sub string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		for _, body := range p.payloads {
			if strings.Contains(body, sub) {
				p.mu.Unlock()
				return true
			}
		}
		p.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func (p *payloadRecorder) reset() {
	p.mu.Lock()
	p.payloads = nil
	p.mu.Unlock()
}

// wantLED builds the substring a WLED apply sends for a physical range.
func wantLED(index, width int) string {
	return fmt.Sprintf("\"i\":[%d,%d", index, index+width)
}

func fetchBinByName(t *testing.T, store db.Store, ctx context.Context, containerID int64, name string) db.Bin {
	t.Helper()
	bins, err := store.GetBinsByContainer(ctx, containerID)
	if err != nil {
		t.Fatalf("get bins: %v", err)
	}
	for _, b := range bins {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("bin %q not found in container %d", name, containerID)
	return db.Bin{}
}

// TestEndToEndConversionAndLocate exercises the complete workflow:
//
//	segment save -> preview -> confirmed conversion -> drawer load ->
//	drawer edit/save -> reload -> locate/flash
//
// verifying that every bin keeps addressing the same physical LEDs.
func TestEndToEndConversionAndLocate(t *testing.T) {
	pr := &payloadRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		pr.add(string(body))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	_, store, dbConn := setupFileAllocTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	svc := NewService(store, wled.NewClient(), convLogger())
	wsvc := wled.NewService(store, wled.NewClient(), convLogger())

	// --- segment-mode grid save: two drawers sharing segment 0 -----------------
	configData := `[` +
		`{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":4,"config":{"type":"linear","total":4}},` +
		`{"id":null,"name":"B","segment_id":0,"led_start":4,"led_count":6,"config":{"type":"linear","total":6}}]`
	gridData := `[` +
		`{"container_index":0,"x":0,"y":0,"led_index":0,"width":3,"name":"a1"},` +
		`{"container_index":0,"x":3,"y":0,"led_index":3,"width":1,"name":"a2"},` +
		`{"container_index":1,"x":0,"y":0,"led_index":5,"width":2,"name":"b1"}]`
	if _, err := svc.SaveGrid(ctx, ctrl.ID, gridData, configData); err != nil {
		t.Fatalf("segment SaveGrid: %v", err)
	}

	containers, _ := store.GetContainersByController(ctx, ctrl.ID)
	var aID, bID int64
	for _, c := range containers {
		switch c.Name {
		case "A":
			aID = c.ID
		case "B":
			bID = c.ID
		}
	}
	if aID == 0 || bID == 0 {
		t.Fatalf("expected drawers A and B, got %+v", containers)
	}
	a1 := fetchBinByName(t, store, ctx, aID, "a1")
	b1 := fetchBinByName(t, store, ctx, bID, "b1")

	// Physical targets before conversion.
	before := map[int64][2]int64{}
	for _, bin := range []db.Bin{a1, b1} {
		cs, _ := store.GetContainersByController(ctx, ctrl.ID)
		seg, idx, err := mapper.CalculateGlobalIndex(ledspace.Segment, cs, bin)
		if err != nil {
			t.Fatalf("map before %s: %v", bin.Name, err)
		}
		before[bin.ID] = [2]int64{seg, idx}
	}
	if before[a1.ID] != [2]int64{0, 0} || before[b1.ID] != [2]int64{0, 5} {
		t.Fatalf("unexpected pre-conversion targets: %v", before)
	}

	// Locate + part assignment in segment mode.
	if err := wsvc.LocateBin(ctx, ctrl.ID, a1.ID); err != nil {
		t.Fatalf("LocateBin segment: %v", err)
	}
	if !pr.waitFor(wantLED(0, 3), time.Second) {
		t.Fatalf("segment LocateBin did not target LED 0 width 3")
	}

	partID, err := store.CreatePart(ctx, db.CreatePartParams{Name: "P"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: b1.ID, Valid: true}, Quantity: 1,
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	pr.reset()
	if err := wsvc.LocatePart(ctx, partID); err != nil {
		t.Fatalf("LocatePart segment: %v", err)
	}
	if !pr.waitFor(wantLED(5, 2), time.Second) {
		t.Fatalf("segment LocatePart did not target LED 5 width 2")
	}

	// --- administrator preview + confirmed conversion --------------------------
	report, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if report.Space != ledspace.Segment || report.Blocked != 0 {
		t.Fatalf("preflight = space %q blocked %d", report.Space, report.Blocked)
	}
	res, err := ConvertToDrawerRelativeConfirmed(ctx, store, report.Fingerprint(), convLogger())
	if err != nil {
		t.Fatalf("confirmed conversion: %v", err)
	}
	if res.Outcome != ConversionConverted {
		t.Fatalf("outcome = %q, want converted", res.Outcome)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Drawer {
		t.Fatalf("space = %q, want drawer", space)
	}

	// Stored indices are now drawer-relative.
	b1 = fetchBinByName(t, store, ctx, bID, "b1")
	if !b1.LedIndex.Valid || b1.LedIndex.Int64 != 1 {
		t.Fatalf("b1 stored index = %v, want drawer-relative 1", b1.LedIndex)
	}

	// Physical targets unchanged after conversion.
	cs, _ := store.GetContainersByController(ctx, ctrl.ID)
	for _, bin := range []db.Bin{a1, b1} {
		seg, idx, err := mapper.CalculateGlobalIndex(ledspace.Drawer, cs, bin)
		if err != nil {
			t.Fatalf("map after %s: %v", bin.Name, err)
		}
		if [2]int64{seg, idx} != before[bin.ID] {
			t.Fatalf("physical target for %s changed: before %v after (%d,%d)", bin.Name, before[bin.ID], seg, idx)
		}
	}

	// --- drawer-mode load, edit and save ---------------------------------------
	// Reload bins as the painter would, then move b1 to drawer-relative index 3.
	pr.reset()
	if err := wsvc.LocateBin(ctx, ctrl.ID, b1.ID); err != nil {
		t.Fatalf("LocateBin after conversion: %v", err)
	}
	if !pr.waitFor(wantLED(5, 2), time.Second) {
		t.Fatalf("post-conversion LocateBin targeted a different LED")
	}
	pr.reset()
	if err := wsvc.LocatePart(ctx, partID); err != nil {
		t.Fatalf("LocatePart after conversion: %v", err)
	}
	if !pr.waitFor(wantLED(5, 2), time.Second) {
		t.Fatalf("post-conversion LocatePart targeted a different LED")
	}

	drawerConfig := fmt.Sprintf(`[`+
		`{"id":%d,"name":"A","segment_id":0,"led_start":0,"led_count":4,"config":{"type":"linear","total":4}},`+
		`{"id":%d,"name":"B","segment_id":0,"led_start":4,"led_count":6,"config":{"type":"linear","total":6}}]`, aID, bID)
	drawerGrid := `[` +
		`{"container_index":0,"x":0,"y":0,"led_index":0,"width":3,"name":"a1"},` +
		`{"container_index":0,"x":3,"y":0,"led_index":3,"width":1,"name":"a2"},` +
		`{"container_index":1,"x":0,"y":0,"led_index":3,"width":2,"name":"b1"}]` // drawer-relative 3 -> physical 7
	if _, err := svc.SaveGrid(ctx, ctrl.ID, drawerGrid, drawerConfig); err != nil {
		t.Fatalf("drawer SaveGrid: %v", err)
	}

	b1 = fetchBinByName(t, store, ctx, bID, "b1")
	if !b1.LedIndex.Valid || b1.LedIndex.Int64 != 3 {
		t.Fatalf("b1 stored index after edit = %v, want 3", b1.LedIndex)
	}
	// Verify the edit addresses the new physical LEDs.
	pr.reset()
	if err := wsvc.LocateBin(ctx, ctrl.ID, b1.ID); err != nil {
		t.Fatalf("LocateBin after edit: %v", err)
	}
	if !pr.waitFor(wantLED(7, 2), time.Second) {
		t.Fatalf("edited drawer-relative bin did not target physical LED 7")
	}

	// FlashError targets the same physical LEDs.
	pr.reset()
	if err := wsvc.FlashError(ctx, ctrl.ID, b1.ID); err != nil {
		t.Fatalf("FlashError: %v", err)
	}
	if !pr.waitFor(wantLED(7, 2), 2*time.Second) {
		t.Fatalf("FlashError targeted a different physical LED")
	}
}
