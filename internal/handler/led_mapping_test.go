package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tuxedocurly/wledger/internal/cabinetpixels"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// Embedding makes any unexpected database access fail the test immediately.
type ledMappingNoQueries struct{ db.Querier }

type ledMappingFixture struct {
	db.Querier
	allocations []db.DrawerLedAllocation
}

func (f ledMappingFixture) GetCabinet(context.Context, int64) (db.Cabinet, error) {
	return db.Cabinet{ID: 1}, nil
}
func (f ledMappingFixture) GetDrawer(_ context.Context, id int64) (db.Drawer, error) {
	return db.Drawer{ID: id, CabinetID: 1}, nil
}
func (f ledMappingFixture) ListAllDrawerLEDAllocations(context.Context) ([]db.DrawerLedAllocation, error) {
	return f.allocations, nil
}
func (f ledMappingFixture) ListCabinetControllers(context.Context, int64) ([]db.CabinetController, error) {
	return []db.CabinetController{{CabinetID: 1, ControllerID: 1}}, nil
}
func (f ledMappingFixture) ListCabinetLEDPathSections(context.Context, int64) ([]db.CabinetLedPathSection, error) {
	return nil, nil
}
func (f ledMappingFixture) GetController(context.Context, int64) (db.Controller, error) {
	return db.Controller{ID: 1, LedCount: 100}, nil
}
func (f ledMappingFixture) ListCabinetLEDStringsByController(context.Context, db.ListCabinetLEDStringsByControllerParams) ([]db.CabinetLedString, error) {
	return nil, nil
}
func (f ledMappingFixture) GetControllerOutputs(context.Context, int64) ([]db.ControllerOutput, error) {
	return nil, nil
}

func TestLEDMappingPreviewSequentialAndControllerZeroBased(t *testing.T) {
	q := ledMappingFixture{}
	result, err := ledBuildPreview(context.Background(), q, ledPreviewRequest{CabinetID: 1, DrawerIDs: []int64{2, 3}, Start: 11, CountPerDrawer: 4})
	if err != nil || len(result.Conflicts) != 0 || len(result.Proposals) != 2 || result.Proposals[1].Start != 15 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	first, last := int64(0), int64(9)
	result, err = ledBuildPreview(context.Background(), q, ledPreviewRequest{CabinetID: 1, DrawerIDs: []int64{2}, ControllerID: 1, FirstPixel: &first, LastPixel: &last})
	if err != nil || len(result.Conflicts) != 0 || len(result.Proposals) != 1 || result.Proposals[0].Start != 1 || result.Proposals[0].Count != 10 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestLEDMappingAppendValidationPreservesExistingAndRejectsOverlap(t *testing.T) {
	q := ledMappingFixture{allocations: []db.DrawerLedAllocation{{ID: 9, CabinetID: 1, DrawerID: 2, StartPixel: 10, PixelCount: 5, DisplayOrder: 7}}}
	ctx := context.Background()
	for _, proposals := range [][]ledProposal{{{2, 14, 1}}, {{3, 14, 2}}, {{3, 30, 5}, {4, 34, 2}}, {{3, 100, 2}}} {
		if err := ledValidateAppend(ctx, q, 1, proposals); err == nil {
			t.Fatalf("accepted %+v", proposals)
		}
	}
	if err := ledValidateAppend(ctx, q, 1, []ledProposal{{2, 15, 3}, {3, 20, 4}}); err != nil {
		t.Fatal(err)
	}
	if q.allocations[0].ID != 9 || q.allocations[0].DisplayOrder != 7 || q.allocations[0].StartPixel != 10 {
		t.Fatal("existing range changed")
	}
}

type ledHTTPFixture struct {
	h             *Handler
	conn          *sql.DB
	hardwareCalls *atomic.Int64
}

func newLEDHTTPFixture(t *testing.T) ledHTTPFixture {
	t.Helper()
	conn, err := db.Open("file:" + t.TempDir() + "/led.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err = db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	calls := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unexpected hardware call", 500)
	}))
	t.Cleanup(server.Close)
	address := strings.TrimPrefix(server.URL, "http://")
	var port int
	fmt.Sscanf(strings.TrimPrefix(address, "127.0.0.1:"), "%d", &port)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO controllers(id,name,ip_address,port,led_count) VALUES(1,'offline mock','127.0.0.1',?,100)`, port)
	exec(`INSERT INTO controller_outputs(controller_id,display_order,name,pixel_count) VALUES(1,0,'configured',100)`)
	exec(`INSERT INTO cabinets(id,name,controller_id) VALUES(1,'cabinet',1)`)
	exec(`INSERT INTO cabinet_controllers(cabinet_id,controller_id,slot) VALUES(1,1,0)`)
	exec(`INSERT INTO drawers(id,cabinet_id,label,x,y,width,height) VALUES(1,1,'drawer one',0,0,10,10),(2,1,'drawer two',12,0,10,10)`)
	exec(`INSERT INTO containers(id,name,controller_id,segment_id,led_start,led_count) VALUES(1,'legacy electrical',1,0,0,100)`)
	exec(`INSERT INTO bins(id,name,container_id,led_index,width) VALUES(1,'first bin',1,7,1),(2,'second bin',1,8,1)`)
	exec(`INSERT INTO bin_drawer_attachments(bin_id,drawer_id,x,y,width,height) VALUES(1,1,0,0,1,1),(2,1,2,0,1,1)`)
	exec(`INSERT INTO drawer_led_allocations(id,cabinet_id,drawer_id,start_pixel,pixel_count,display_order) VALUES(10,1,1,11,3,0),(20,1,1,31,2,1)`)
	store := db.NewStore(conn)
	if err = store.InitSettings(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ledHTTPFixture{&Handler{Queries: store, Database: conn, Locator: wled.NewPhysicalLocateService(store, wled.NewClient())}, conn, calls}
}
func ledHTTPCall(t *testing.T, handle http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/mapping", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handle(w, r)
	return w
}
func TestLEDMappingHTTPAppendAtomicAndPreservesExistingIDs(t *testing.T) {
	f := newLEDHTTPFixture(t)
	rejected := ledHTTPCall(t, f.h.HandleLEDMappingSave, `{"cabinet_id":1,"proposals":[{"drawer_id":2,"start":50,"count":2},{"drawer_id":1,"start":12,"count":1}],"confirm":true}`)
	if rejected.Code < 400 {
		t.Fatalf("overlap accepted: %s", rejected.Body.String())
	}
	rows, err := f.h.Queries.ListAllDrawerLEDAllocations(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatalf("partial save: %+v %v", rows, err)
	}
	saved := ledHTTPCall(t, f.h.HandleLEDMappingSave, `{"cabinet_id":1,"proposals":[{"drawer_id":1,"start":50,"count":2},{"drawer_id":2,"start":60,"count":3}],"confirm":true}`)
	if saved.Code != 200 {
		t.Fatalf("save=%d %s", saved.Code, saved.Body.String())
	}
	rows, err = f.h.Queries.ListDrawerLEDAllocationsByDrawer(context.Background(), 1)
	if err != nil || len(rows) != 3 || rows[0].ID != 10 || rows[1].ID != 20 || rows[2].DisplayOrder != 2 {
		t.Fatalf("ranges=%+v err=%v", rows, err)
	}
	cab, err := f.h.Queries.GetCabinet(context.Background(), 1)
	if err != nil || cab.ElectricalRevision != 1 {
		t.Fatalf("revision=%d err=%v", cab.ElectricalRevision, err)
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("mapping contacted hardware")
	}
}
func TestLEDMappingHTTPRollbackOnInsertFailure(t *testing.T) {
	f := newLEDHTTPFixture(t)
	if _, err := f.conn.Exec(`CREATE TRIGGER fail_mapping BEFORE INSERT ON drawer_led_allocations WHEN NEW.drawer_id=2 BEGIN SELECT RAISE(ABORT,'injected insert failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := ledHTTPCall(t, f.h.HandleLEDMappingSave, `{"cabinet_id":1,"proposals":[{"drawer_id":1,"start":50,"count":2},{"drawer_id":2,"start":60,"count":3}],"confirm":true}`)
	if response.Code < 400 {
		t.Fatal("injected failure accepted")
	}
	rows, err := f.h.Queries.ListAllDrawerLEDAllocations(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatalf("partial transaction persisted: %+v %v", rows, err)
	}
	cab, _ := f.h.Queries.GetCabinet(context.Background(), 1)
	if cab.ElectricalRevision != 0 {
		t.Fatal("failed transaction advanced revision")
	}
}
func TestLEDMappingHTTPBinAssignmentDataAndNoHardware(t *testing.T) {
	f := newLEDHTTPFixture(t)
	response := ledHTTPCall(t, f.h.HandleLEDBinPixel, `{"bin_id":1,"drawer_id":1,"controller_id":1,"pixel":31,"confirm":true}`)
	if response.Code != 200 {
		t.Fatalf("assignment=%d %s", response.Code, response.Body.String())
	}
	var assigned struct {
		RelativePixel int64 `json:"relative_pixel"`
		AllocationID  int64 `json:"allocation_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &assigned); err != nil {
		t.Fatal(err)
	}
	if assigned.RelativePixel != 4 || assigned.AllocationID != 20 {
		t.Fatalf("assigned=%+v", assigned)
	}
	duplicate := ledHTTPCall(t, f.h.HandleLEDBinPixel, `{"bin_id":2,"drawer_id":1,"controller_id":1,"pixel":31,"confirm":true}`)
	if duplicate.Code < 400 {
		t.Fatal("duplicate pixel accepted")
	}
	data := httptest.NewRecorder()
	f.h.HandleLEDTestCentreData(data, httptest.NewRequest(http.MethodGet, "/data", nil))
	if data.Code != 200 {
		t.Fatalf("data=%d %s", data.Code, data.Body.String())
	}
	var view struct {
		Cabinets   []ledDataCabinet `json:"cabinets"`
		Validation []ledValidation  `json:"validation"`
	}
	if err := json.Unmarshal(data.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	drawer := view.Cabinets[0].Drawers[0]
	if drawer.Ranges[0].Physical[0].RelativeStart != 0 || drawer.Ranges[1].Physical[0].RelativeStart != 3 {
		t.Fatalf("relative ranges=%+v", drawer.Ranges)
	}
	if !drawer.Bins[0].Assigned || drawer.Bins[0].RelativePixel == nil || *drawer.Bins[0].RelativePixel != 4 || drawer.Bins[1].Assigned {
		t.Fatalf("bins=%+v", drawer.Bins)
	}
	found := false
	for _, v := range view.Validation {
		if v.BinID == 2 && v.Severity == "warning" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing unassigned validation")
	}
	legacy, _ := f.h.Queries.GetBin(context.Background(), 1)
	if legacy.LedIndex.Int64 != 7 {
		t.Fatal("legacy led_index changed")
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("GET or mapping contacted hardware")
	}
}
func TestLEDMappingHTTPStrictRequestsAndActiveOperationBlocking(t *testing.T) {
	f := newLEDHTTPFixture(t)
	for _, body := range []string{`{"cabinet_id":1,"drawer_ids":[2],"start":50,"count_per_drawer":1,"unknown":true}`, `{"cabinet_id":1} {}`, `{"cabinet_id":1,"drawer_ids":[999],"start":50,"count_per_drawer":1}`, `{"cabinet_id":1,"drawer_ids":[2],"start":100,"count_per_drawer":2}`} {
		response := ledHTTPCall(t, f.h.HandleLEDMappingPreview, body)
		if response.Code < 400 {
			t.Fatalf("accepted invalid preview: %s", body)
		}
	}
	noConfirm := ledHTTPCall(t, f.h.HandleLEDMappingSave, `{"cabinet_id":1,"proposals":[{"drawer_id":2,"start":50,"count":2}]}`)
	if noConfirm.Code < 400 {
		t.Fatal("accepted unconfirmed save")
	}
	if _, err := f.conn.Exec(`UPDATE physical_emergency_state SET active=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		handler http.HandlerFunc
		body    string
	}{
		{f.h.HandleLEDMappingSave, `{"cabinet_id":1,"proposals":[{"drawer_id":2,"start":50,"count":2}],"confirm":true}`},
		{f.h.HandleLEDBinPixel, `{"bin_id":1,"drawer_id":1,"controller_id":1,"pixel":31,"confirm":true}`},
	} {
		response := ledHTTPCall(t, call.handler, call.body)
		if response.Code < 400 {
			t.Fatal("mapping mutated under operation ownership")
		}
	}
	mappings, _ := f.h.Queries.ListPhysicalBinPixels(context.Background())
	if len(mappings) != 0 {
		t.Fatal("active-operation rejection saved bin override")
	}
}

// Check preview proposals against the canonical forward map, independently of
// the inverse preview implementation and its proposal coalescing.
func assertLEDPreviewExactSelection(t *testing.T, q db.Querier, result ledMappingResult, first, last int64) {
	t.Helper()
	topology, err := cabinetpixels.Load(context.Background(), q, 1)
	if err != nil {
		t.Fatal(err)
	}
	logicalSeen := map[int64]bool{}
	physicalSeen := map[int64]bool{}
	previousEnd := int64(0)
	for _, p := range result.Proposals {
		if p.DrawerID != 2 || p.Start < 1 || p.Count < 1 || p.Start <= previousEnd {
			t.Fatalf("invalid, duplicate or unsorted proposal: %+v", result.Proposals)
		}
		previousEnd = p.Start + p.Count - 1
		for offset := int64(0); offset < p.Count; offset++ {
			logical := p.Start + offset
			if logicalSeen[logical] {
				t.Fatalf("duplicate logical pixel %d", logical)
			}
			logicalSeen[logical] = true
			mapped, e := cabinetpixels.SplitTopologyRange(topology, logical, 1)
			if e != nil || len(mapped) != 1 || mapped[0].ControllerID != 1 {
				t.Fatalf("proposal map=%+v err=%v", mapped, e)
			}
			pixel := mapped[0].LocalStart - 1
			if pixel < first || pixel > last || physicalSeen[pixel] {
				t.Fatalf("unexpected/duplicate absolute pixel %d for selection %d..%d", pixel, first, last)
			}
			physicalSeen[pixel] = true
		}
	}
	if int64(len(physicalSeen)) != last-first+1 {
		t.Fatalf("incomplete physical selection: got %d, want %d", len(physicalSeen), last-first+1)
	}
}

func TestLEDMappingPreviewAbsoluteNonzeroOutputReverseAndCustom(t *testing.T) {
	for _, pattern := range []string{"linear", "custom"} {
		t.Run(pattern, func(t *testing.T) {
			f := newLEDHTTPFixture(t)
			if _, err := f.conn.Exec(`DELETE FROM controller_outputs`); err != nil {
				t.Fatal(err)
			}
			order := "[]"
			if pattern == "custom" {
				values := make([]int64, 90)
				for i := range values {
					values[i] = int64((i * 7) % 90)
				}
				raw, _ := json.Marshal(values)
				order = string(raw)
			}
			var firstOutput, secondOutput int64
			if err := f.conn.QueryRow(`INSERT INTO controller_outputs(controller_id,display_order,name,pixel_count) VALUES(1,0,'first',10) RETURNING id`).Scan(&firstOutput); err != nil {
				t.Fatal(err)
			}
			if err := f.conn.QueryRow(`INSERT INTO controller_outputs(controller_id,display_order,name,pixel_count,direction,pattern,custom_order_json) VALUES(1,1,'second',90,'reverse',?,?) RETURNING id`, pattern, order).Scan(&secondOutput); err != nil {
				t.Fatal(err)
			}
			if _, err := f.conn.Exec(`INSERT INTO cabinet_led_strings(cabinet_id,controller_id,name,display_order,pixel_count,controller_output_id) VALUES(1,1,'first',0,10,?),(1,1,'second',1,90,?)`, firstOutput, secondOutput); err != nil {
				t.Fatal(err)
			}
			if _, err := f.conn.Exec(`UPDATE cabinet_controllers SET strings_configured=1`); err != nil {
				t.Fatal(err)
			}
			response := ledHTTPCall(t, f.h.HandleLEDMappingPreview, `{"cabinet_id":1,"controller_id":1,"output_order":1,"first_pixel":89,"last_pixel":90,"drawer_ids":[2]}`)
			if response.Code != 200 {
				t.Fatalf("preview=%d %s", response.Code, response.Body.String())
			}
			var result ledMappingResult
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Conflicts) != 0 {
				t.Fatalf("conflicts=%v", result.Conflicts)
			}
			assertLEDPreviewExactSelection(t, f.h.Queries, result, 89, 90)
			for _, bounds := range [][2]int64{{0, 0}, {99, 99}, {8, 12}, {0, 99}} {
				body := fmt.Sprintf(`{"cabinet_id":1,"controller_id":1,"output_order":1,"first_pixel":%d,"last_pixel":%d,"drawer_ids":[2]}`, bounds[0], bounds[1])
				preview := ledHTTPCall(t, f.h.HandleLEDMappingPreview, body)
				if preview.Code != 200 {
					t.Fatalf("boundary preview=%d %s", preview.Code, preview.Body.String())
				}
				var exact ledMappingResult
				if err := json.Unmarshal(preview.Body.Bytes(), &exact); err != nil {
					t.Fatal(err)
				}
				assertLEDPreviewExactSelection(t, f.h.Queries, exact, bounds[0], bounds[1])
			}
			if pattern == "linear" {
				if len(result.Proposals) != 1 || result.Proposals[0].Start != 20 || result.Proposals[0].Count != 2 {
					t.Fatalf("reverse proposals=%+v", result.Proposals)
				}
			} else {
				if len(result.Proposals) != 2 || result.Proposals[0].Start != 38 || result.Proposals[1].Start != 51 {
					t.Fatalf("custom proposals=%+v", result.Proposals)
				}
			}
			// Configured logical output order does not prove WLED's actual
			// output-array identity; even a matching number must not translate
			// or constrain these absolute coordinates to the logical string.
			absolute := ledHTTPCall(t, f.h.HandleLEDMappingPreview, `{"cabinet_id":1,"controller_id":1,"output_order":1,"first_pixel":0,"last_pixel":1,"drawer_ids":[2]}`)
			if absolute.Code != 200 {
				t.Fatalf("logical output order incorrectly constrained absolute range: %d %s", absolute.Code, absolute.Body.String())
			}
			var firstResult ledMappingResult
			if err := json.Unmarshal(absolute.Body.Bytes(), &firstResult); err != nil {
				t.Fatal(err)
			}
			if len(firstResult.Proposals) != 1 || firstResult.Proposals[0].Start != 1 || firstResult.Proposals[0].Count != 2 {
				t.Fatalf("absolute range translated: %+v", firstResult.Proposals)
			}
			// Unknown hardware output order is not a logical string selector and must
			// never introduce an offset or reinterpret absolute controller pixels.
			unproven := ledHTTPCall(t, f.h.HandleLEDMappingPreview, `{"cabinet_id":1,"controller_id":1,"output_order":8,"first_pixel":89,"last_pixel":90,"drawer_ids":[2]}`)
			if unproven.Code != 200 {
				t.Fatalf("unproven order must retain absolute coordinates: %d %s", unproven.Code, unproven.Body.String())
			}
			data := httptest.NewRecorder()
			f.h.HandleLEDTestCentreData(data, httptest.NewRequest(http.MethodGet, "/data", nil))
			if data.Code != 200 {
				t.Fatalf("data=%d %s", data.Code, data.Body.String())
			}
			var view struct {
				Cabinets []ledDataCabinet `json:"cabinets"`
			}
			if err := json.Unmarshal(data.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			physical := view.Cabinets[0].Drawers[0].Ranges[0].Physical
			expectedSecond := int64(98)
			if pattern == "custom" {
				expectedSecond = 92
			}
			if len(physical) != 3 || physical[0].FirstPixel != 99 || physical[0].RelativeStart != 0 || physical[1].FirstPixel != expectedSecond || physical[1].RelativeStart != 1 {
				t.Fatalf("logical-to-physical inspection=%+v", physical)
			}
			if f.hardwareCalls.Load() != 0 {
				t.Fatal("preview performed hardware discovery")
			}
		})
	}
}

func TestLEDMappingHTTPRejectsProvableControllerAliasOverlap(t *testing.T) {
	f := newLEDHTTPFixture(t)
	for _, query := range []string{
		`INSERT INTO controllers(id,name,ip_address,port,led_count) SELECT 2,'alias',ip_address,port,led_count FROM controllers WHERE id=1`,
		`INSERT INTO cabinets(id,name,controller_id) VALUES(2,'alias cabinet',2)`,
		`INSERT INTO cabinet_controllers(cabinet_id,controller_id,slot) VALUES(2,2,0)`,
		`INSERT INTO drawers(id,cabinet_id,label,x,y,width,height) VALUES(3,2,'alias drawer',0,0,10,10)`,
	} {
		if _, err := f.conn.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	response := ledHTTPCall(t, f.h.HandleLEDMappingSave, `{"cabinet_id":2,"proposals":[{"drawer_id":3,"start":11,"count":1}],"confirm":true}`)
	if response.Code < 400 {
		t.Fatal("provable controller alias overlap accepted")
	}
	rows, err := f.h.Queries.ListDrawerLEDAllocationsByDrawer(context.Background(), 3)
	if err != nil || len(rows) != 0 {
		t.Fatalf("alias mapping persisted: %+v %v", rows, err)
	}
}

func TestLEDMappingHTTPClearOnlyConfirmedAttachedBinAndUnlocksAllocation(t *testing.T) {
	f := newLEDHTTPFixture(t)
	ctx := context.Background()
	for _, body := range []string{`{"bin_id":1,"drawer_id":1,"controller_id":1,"pixel":31,"confirm":true}`, `{"bin_id":2,"drawer_id":1,"controller_id":1,"pixel":30,"confirm":true}`} {
		response := ledHTTPCall(t, f.h.HandleLEDBinPixel, body)
		if response.Code != 200 {
			t.Fatalf("setup assignment=%d %s", response.Code, response.Body.String())
		}
	}
	for _, body := range []string{`{"bin_id":1,"drawer_id":1,"clear":true}`, `{"bin_id":1,"drawer_id":2,"clear":true,"confirm":true}`, `{"bin_id":999,"drawer_id":1,"clear":true,"confirm":true}`} {
		response := ledHTTPCall(t, f.h.HandleLEDBinPixel, body)
		if response.Code < 400 {
			t.Fatalf("accepted invalid clear: %s", body)
		}
	}
	if _, err := f.conn.Exec(`UPDATE physical_emergency_state SET active=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	blocked := ledHTTPCall(t, f.h.HandleLEDBinPixel, `{"bin_id":1,"drawer_id":1,"clear":true,"confirm":true}`)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("clear under ownership=%d %s", blocked.Code, blocked.Body.String())
	}
	if _, err := f.h.Queries.GetPhysicalBinPixel(ctx, 1); err != nil {
		t.Fatalf("rejected clear lost override: %v", err)
	}
	if _, err := f.conn.Exec(`UPDATE physical_emergency_state SET active=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		cleared := ledHTTPCall(t, f.h.HandleLEDBinPixel, `{"bin_id":1,"drawer_id":1,"clear":true,"confirm":true}`)
		if cleared.Code != 200 {
			t.Fatalf("clear=%d %s", cleared.Code, cleared.Body.String())
		}
		var result struct {
			BinID    int64 `json:"bin_id"`
			Cleared  bool  `json:"cleared"`
			Assigned bool  `json:"assigned"`
		}
		if err := json.Unmarshal(cleared.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.BinID != 1 || !result.Cleared || result.Assigned {
			t.Fatalf("clear result=%+v", result)
		}
	}
	if _, err := f.h.Queries.GetPhysicalBinPixel(ctx, 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("override not deleted: %v", err)
	}
	if _, err := f.h.Queries.GetPhysicalBinPixel(ctx, 2); err != nil {
		t.Fatalf("clearing bin one changed bin two: %v", err)
	}
	if _, err := f.conn.Exec(`DELETE FROM drawer_led_allocations WHERE id=20`); err == nil {
		t.Fatal("another bin's allocation protection was removed")
	}
	cleared := ledHTTPCall(t, f.h.HandleLEDBinPixel, `{"bin_id":2,"drawer_id":1,"clear":true,"confirm":true}`)
	if cleared.Code != 200 {
		t.Fatalf("clear bin two=%d %s", cleared.Code, cleared.Body.String())
	}
	if _, err := f.conn.Exec(`DELETE FROM drawer_led_allocations WHERE id=20`); err != nil {
		t.Fatalf("explicit clear did not unlock allocation editing: %v", err)
	}
	for id, want := range map[int64]int64{1: 7, 2: 8} {
		bin, err := f.h.Queries.GetBin(ctx, id)
		if err != nil || !bin.LedIndex.Valid || bin.LedIndex.Int64 != want {
			t.Fatalf("legacy bin changed: %+v %v", bin, err)
		}
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("clear contacted hardware")
	}
}
func TestLEDMappingHTTPClearStaleOverrideUsesCurrentAttachment(t *testing.T) {
	f := newLEDHTTPFixture(t)
	assigned := ledHTTPCall(t, f.h.HandleLEDBinPixel, `{"bin_id":1,"drawer_id":1,"controller_id":1,"pixel":31,"confirm":true}`)
	if assigned.Code != 200 {
		t.Fatal(assigned.Body.String())
	}
	if _, err := f.conn.Exec(`UPDATE bin_drawer_attachments SET drawer_id=2 WHERE bin_id=1`); err != nil {
		t.Fatal(err)
	}
	old := ledHTTPCall(t, f.h.HandleLEDBinPixel, `{"bin_id":1,"drawer_id":1,"clear":true,"confirm":true}`)
	if old.Code != 409 {
		t.Fatalf("old attachment clear=%d", old.Code)
	}
	cleared := ledHTTPCall(t, f.h.HandleLEDBinPixel, `{"bin_id":1,"drawer_id":2,"clear":true,"confirm":true}`)
	if cleared.Code != 200 {
		t.Fatalf("stale correction clear=%d %s", cleared.Code, cleared.Body.String())
	}
	if _, err := f.h.Queries.GetPhysicalBinPixel(context.Background(), 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale override remains: %v", err)
	}
}

type ledEmergencyReadFailureStore struct{ db.Store }

func (s ledEmergencyReadFailureStore) ListPhysicalEmergencyOutcomes(context.Context) ([]db.PhysicalEmergencyOutcome, error) {
	return nil, errors.New("injected emergency status read failure")
}
func TestLEDMappingHTTPDataEmergencyOutcomesSurviveServiceRecreation(t *testing.T) {
	f := newLEDHTTPFixture(t)
	if _, err := f.conn.Exec(`UPDATE physical_emergency_state SET active=1,emergency_id='persisted-test' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	outcome := db.PhysicalEmergencyOutcome{ControllerID: 1, Address: "127.0.0.1", Port: 12345, EmergencyID: "persisted-test", Attempted: true, Acknowledged: false, Error: "mock off failed"}
	if err := f.h.Queries.SavePhysicalEmergencyOutcome(context.Background(), outcome); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if i == 1 {
			f.h.Locator = wled.NewPhysicalLocateService(db.NewStore(f.conn), wled.NewClient())
		}
		response := httptest.NewRecorder()
		f.h.HandleLEDTestCentreData(response, httptest.NewRequest(http.MethodGet, "/data", nil))
		if response.Code != 200 {
			t.Fatalf("data=%d %s", response.Code, response.Body.String())
		}
		var result struct {
			Emergency []wled.ControllerOffResult `json:"emergency_results"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Emergency) != 1 || result.Emergency[0].ControllerID != 1 || result.Emergency[0].Acknowledged || result.Emergency[0].Error != "mock off failed" {
			t.Fatalf("persisted outcomes=%+v", result.Emergency)
		}
	}
	f.h.Locator = wled.NewPhysicalLocateService(ledEmergencyReadFailureStore{f.h.Queries}, wled.NewClient())
	failed := httptest.NewRecorder()
	f.h.HandleLEDTestCentreData(failed, httptest.NewRequest(http.MethodGet, "/data", nil))
	if failed.Code < 400 || !strings.Contains(failed.Body.String(), "emergency status read failure") {
		t.Fatalf("status read failure hidden: %d %s", failed.Code, failed.Body.String())
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("emergency status GET contacted hardware")
	}
}
func TestLEDMappingHTTPDataCoverageAndUnassignedController(t *testing.T) {
	f := newLEDHTTPFixture(t)
	if _, err := f.conn.Exec(`INSERT INTO controllers(id,name,ip_address,led_count) VALUES(2,'unassigned','192.0.2.2',8)`); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	f.h.HandleLEDTestCentreData(response, httptest.NewRequest(http.MethodGet, "/data", nil))
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var view struct {
		Controllers []ledDataController `json:"controllers"`
		Unassigned  int                 `json:"unassigned_controller_count"`
		Validation  []ledValidation     `json:"validation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Unassigned != 1 {
		t.Fatalf("unassigned controllers=%d", view.Unassigned)
	}
	for _, c := range view.Controllers {
		if c.AllocatedPixelCount == nil || c.UnassignedPixelCount == nil {
			t.Fatalf("missing valid coverage: %+v", c)
		}
		if c.ID == 1 && (*c.AllocatedPixelCount != 5 || *c.UnassignedPixelCount != 95) {
			t.Fatalf("configured coverage=%+v", c)
		}
		if c.ID == 2 && (*c.AllocatedPixelCount != 0 || *c.UnassignedPixelCount != 8) {
			t.Fatalf("unused controller coverage=%+v", c)
		}
	}
	found := false
	for _, v := range view.Validation {
		if v.ControllerID == 2 && v.Message == "Controller is not assigned to a cabinet" {
			found = true
		}
	}
	if !found {
		t.Fatal("unassigned controller not reported")
	}
	if f.hardwareCalls.Load() != 0 {
		t.Fatal("coverage performed live inspection")
	}
}

type ledCoverageQuerier struct{ ledMappingFixture }

func (q ledCoverageQuerier) ListAllCabinetControllers(context.Context) ([]db.CabinetController, error) {
	return []db.CabinetController{{CabinetID: 1, ControllerID: 1}}, nil
}
func (q ledCoverageQuerier) ListCabinetsIncludingArchived(context.Context) ([]db.Cabinet, error) {
	return []db.Cabinet{{ID: 1}}, nil
}
func (q ledCoverageQuerier) ListAllDrawers(context.Context) ([]db.Drawer, error) {
	return []db.Drawer{{ID: 1, CabinetID: 1}, {ID: 2, CabinetID: 999}}, nil
}
func (q ledCoverageQuerier) ListBinDrawerAttachments(context.Context) ([]db.BinDrawerAttachment, error) {
	return []db.BinDrawerAttachment{{BinID: 1, DrawerID: 999}}, nil
}
func TestLEDMappingCoverageSuppressesUnknownCountsAndReportsRelationships(t *testing.T) {
	q := ledCoverageQuerier{ledMappingFixture{allocations: []db.DrawerLedAllocation{{ID: 1, CabinetID: 1, DrawerID: 1, StartPixel: 100, PixelCount: 2}}}}
	controllers := []ledDataController{{ID: 1, LEDCount: 100}}
	validation := []ledValidation{}
	if _, err := ledValidateCoverage(context.Background(), q, controllers, &validation); err != nil {
		t.Fatal(err)
	}
	if controllers[0].AllocatedPixelCount != nil || controllers[0].UnassignedPixelCount != nil {
		t.Fatal("invalid ranges reported as known free capacity")
	}
	found := map[string]bool{}
	for _, issue := range validation {
		if issue.Severity == "error" {
			found[issue.Message] = true
		}
	}
	if !found["Drawer references a missing cabinet"] || !found["Bin attachment references a missing drawer"] {
		t.Fatalf("relational issues not reported: %+v", validation)
	}
	mappedError := false
	for _, issue := range validation {
		if issue.DrawerID == 1 && strings.Contains(issue.Message, "cannot be mapped") {
			mappedError = true
		}
	}
	if !mappedError {
		t.Fatal("invalid allocation was not reported")
	}
	// Overlapping intervals are unioned, not counted twice as extra coverage.
	q.allocations = []db.DrawerLedAllocation{{ID: 1, CabinetID: 1, DrawerID: 1, StartPixel: 10, PixelCount: 5}, {ID: 2, CabinetID: 1, DrawerID: 1, StartPixel: 12, PixelCount: 5}}
	controllers = []ledDataController{{ID: 1, LEDCount: 100}}
	validation = nil
	if _, err := ledValidateCoverage(context.Background(), q, controllers, &validation); err != nil {
		t.Fatal(err)
	}
	if controllers[0].AllocatedPixelCount == nil || *controllers[0].AllocatedPixelCount != 7 || controllers[0].UnassignedPixelCount == nil || *controllers[0].UnassignedPixelCount != 93 {
		t.Fatalf("coverage did not union configured intervals: %+v", controllers[0])
	}
}

func TestLEDMappingPreviewRejectsMalformedInputsBeforeReading(t *testing.T) {
	zero := int64(0)
	negative := int64(-1)
	tests := []ledPreviewRequest{
		{},
		{CabinetID: 1, DrawerIDs: []int64{1}, Start: 0, CountPerDrawer: 1},
		{CabinetID: 1, DrawerIDs: []int64{1, 1}, Start: 1, CountPerDrawer: 1},
		{CabinetID: 1, DrawerIDs: []int64{1, 2}, Start: math.MaxInt64, CountPerDrawer: 1},
		{CabinetID: 1, DrawerIDs: []int64{1}, ControllerID: 1},
		{CabinetID: 1, DrawerIDs: []int64{1}, ControllerID: 1, FirstPixel: &negative, LastPixel: &zero},
		{CabinetID: 1, DrawerIDs: []int64{1, 2}, ControllerID: 1, FirstPixel: &zero, LastPixel: &zero},
		{CabinetID: 1, DrawerIDs: []int64{1}, ControllerID: 1, FirstPixel: &zero, LastPixel: &zero, Start: 1},
	}
	for i, in := range tests {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			_, err := ledBuildPreview(context.Background(), ledMappingNoQueries{}, in)
			if !errors.Is(err, storage.ErrLEDAllocationInvalid) {
				t.Fatalf("got %v, want invalid allocation", err)
			}
		})
	}
}
