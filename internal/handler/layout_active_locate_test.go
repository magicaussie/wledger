package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func TestCabinetElectricalHandlersRejectWritesDuringActiveOrPartialLocate(t *testing.T) {
	for _, state := range []string{"active", "partial"} {
		t.Run(state, func(t *testing.T) {
			f := newStorageFixture(t)
			seedElectricalMutationFixture(t, f, state)

			r := chi.NewRouter()
			r.Post("/storage/cabinets/{id}/controller", f.h.HandleCabinetControllerSave)
			r.Post("/storage/cabinets/{id}/wiring", f.h.HandleCabinetWiringSave)
			r.Post("/storage/cabinets/{id}/drawers/{drawerID}/led", f.h.HandleDrawerLEDAllocationsSave)
			r.Post("/hardware/{id}/capacity", f.h.HandleHardwareCapacitySave)
			r.Post("/hardware/{id}/locate-mapping", f.h.HandleControllerLocateMappingSave)

			requests := []struct {
				name       string
				path       string
				form       url.Values
				wantStatus int
			}{
				{
					name: "controller reassignment",
					path: "/storage/cabinets/1/controller",
					form: url.Values{"electrical_revision": {"0"}, "controller_ids": {"2"}, "confirm_mapping_impact": {"confirm"}},
				},
				{
					name: "cabinet wiring change",
					path: "/storage/cabinets/1/wiring",
					form: url.Values{
						"electrical_revision":    {"0"},
						"confirm_mapping_impact": {"confirm"},
						"wiring":                 {`{"revision":0,"configured_controller_ids":[1],"strings":[{"id":1,"controller_id":1,"name":"Replacement","display_order":0,"pixel_count":10,"layout_complete":true,"sections":[{"id":1,"display_order":0,"pixel_count":10,"points":[{"x":1,"y":1},{"x":9,"y":1}]}]}]}`},
					},
				},
				{
					name: "drawer LED allocation change",
					path: "/storage/cabinets/1/drawers/1/led",
					form: url.Values{"revision": {"0"}, "coordinate_space": {"cabinet"}, "allocations": {`[{"id":1,"start_pixel":21,"pixel_count":5}]`}},
				},
				{
					name: "controller capacity change",
					path: "/hardware/1/capacity",
					form: url.Values{"led_count": {"120"}, "expected_led_count": {"100"}, "electrical_revision": {"0"}, "confirm_mapping_impact": {"confirm"}},
				},
				{
					name:       "mapping verification change",
					path:       "/hardware/1/locate-mapping",
					wantStatus: http.StatusConflict,
				},
			}
			for _, test := range requests {
				t.Run(test.name, func(t *testing.T) {
					before := electricalMutationSnapshot(t, f)
					req := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					rr := httptest.NewRecorder()
					r.ServeHTTP(rr, req)

					wantStatus := test.wantStatus
					if wantStatus == 0 {
						wantStatus = http.StatusSeeOther
					}
					if rr.Code != wantStatus {
						t.Fatalf("POST %s = %d, want rejection status %d: %s", test.path, rr.Code, wantStatus, rr.Body.String())
					}
					if test.wantStatus == http.StatusConflict {
						if !strings.Contains(rr.Body.String(), "active") && !strings.Contains(rr.Body.String(), "Locate") {
							t.Fatalf("POST %s did not report active Locate rejection: %s", test.path, rr.Body.String())
						}
					} else {
						location, err := url.Parse(rr.Header().Get("Location"))
						if err != nil {
							t.Fatalf("parse rejection location %q: %v", rr.Header().Get("Location"), err)
						}
						if !strings.Contains(location.RawQuery, "active") && !strings.Contains(location.RawQuery, "Locate") {
							t.Fatalf("POST %s did not report active Locate rejection: %s", test.path, rr.Header().Get("Location"))
						}
					}
					if after := electricalMutationSnapshot(t, f); !reflect.DeepEqual(before, after) {
						t.Fatalf("POST %s mutated persistent electrical state\nbefore: %v\nafter:  %v", test.path, before, after)
					}
				})
			}
		})
	}
}

func seedElectricalMutationFixture(t *testing.T, f *storageFixture, state string) {
	t.Helper()
	f.exec(`INSERT INTO controllers (id,name,ip_address,port,led_count) VALUES (1,'Controller one','127.0.0.1',80,100)`)
	f.exec(`INSERT INTO controllers (id,name,ip_address,port,led_count) VALUES (2,'Controller two','127.0.0.2',80,100)`)
	f.exec(`INSERT INTO cabinets (id,name,controller_id,coordinate_system,electrical_revision,layout_width,layout_height) VALUES (1,'Test cabinet',1,'grid',0,100,100)`)
	f.exec(`INSERT INTO cabinet_controllers (cabinet_id,controller_id,slot,strings_configured) VALUES (1,1,0,1)`)
	f.exec(`INSERT INTO cabinet_controllers (cabinet_id,controller_id,slot,strings_configured) VALUES (1,2,1,0)`)
	f.exec(`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height) VALUES (1,1,'Drawer one',0,0,0,10,10)`)
	f.exec(`INSERT INTO cabinet_led_strings (id,cabinet_id,controller_id,name,display_order,pixel_count,layout_complete) VALUES (1,1,1,'Original string',0,100,1)`)
	f.exec(`INSERT INTO cabinet_led_path_sections (id,string_id,display_order,pixel_count,points_json) VALUES (1,1,0,100,'[{"x":1,"y":1},{"x":99,"y":1}]')`)
	f.exec(`INSERT INTO drawer_led_allocations (id,cabinet_id,drawer_id,start_pixel,pixel_count,display_order) VALUES (1,1,1,10,5,0)`)
	f.exec(`INSERT INTO physical_locate_sessions (controller_id,controller_address,controller_port,session_id,drawer_id,bin_id,owner_id,state,ranges_json,commands_json,fingerprint,color,brightness,last_operation,last_operation_at,last_successful_operation,last_successful_at,last_error) VALUES (1,'127.0.0.1',80,?,1,1,7,?,'[{"start":10,"count":5}]','[{"segment_id":10,"start":9,"stop":14}]','fixture-fingerprint','#0000ff',128,'activation',CURRENT_TIMESTAMP,'activation',CURRENT_TIMESTAMP,?)`, "mutation-guard-"+state, state, map[bool]string{true: "controller cleanup unresolved", false: ""}[state == "partial"])
	f.exec(`INSERT INTO physical_locate_session_targets (session_id,controller_id,controller_address,controller_port,ranges_json,commands_json,fingerprint,may_have_received,last_error) VALUES (?,1,'127.0.0.1',80,'[{"start":10,"count":5}]','[{"segment_id":10,"start":9,"stop":14}]','fixture-fingerprint',1,?)`, "mutation-guard-"+state, map[bool]string{true: "cleanup unresolved", false: ""}[state == "partial"])
	f.h.Locator = wled.NewPhysicalLocateService(db.NewStore(f.conn), wled.NewClient())
}

func electricalMutationSnapshot(t *testing.T, f *storageFixture) []string {
	t.Helper()
	const query = `
SELECT 'cabinet:'||id||':'||coalesce(controller_id,-1)||':'||electrical_revision FROM cabinets WHERE id=1
UNION ALL SELECT 'membership:'||cabinet_id||':'||controller_id||':'||slot||':'||strings_configured FROM cabinet_controllers WHERE cabinet_id=1
UNION ALL SELECT 'string:'||id||':'||cabinet_id||':'||controller_id||':'||name||':'||display_order||':'||pixel_count||':'||layout_complete FROM cabinet_led_strings WHERE cabinet_id=1
UNION ALL SELECT 'section:'||s.id||':'||s.string_id||':'||s.display_order||':'||s.pixel_count||':'||s.points_json FROM cabinet_led_path_sections s JOIN cabinet_led_strings x ON x.id=s.string_id WHERE x.cabinet_id=1
UNION ALL SELECT 'allocation:'||id||':'||cabinet_id||':'||drawer_id||':'||start_pixel||':'||pixel_count||':'||display_order FROM drawer_led_allocations WHERE cabinet_id=1
UNION ALL SELECT 'session:'||controller_id||':'||controller_address||':'||controller_port||':'||session_id||':'||drawer_id||':'||bin_id||':'||owner_id||':'||state||':'||ranges_json||':'||commands_json||':'||fingerprint||':'||color||':'||brightness||':'||last_operation||':'||last_operation_at||':'||last_successful_operation||':'||last_successful_at||':'||last_error FROM physical_locate_sessions
UNION ALL SELECT 'target:'||session_id||':'||controller_id||':'||controller_address||':'||controller_port||':'||ranges_json||':'||commands_json||':'||fingerprint||':'||may_have_received||':'||last_error FROM physical_locate_session_targets
ORDER BY 1`
	rows, err := f.conn.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("read persistent electrical snapshot: %v", err)
	}
	defer rows.Close()
	var snapshot []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatalf("scan persistent electrical snapshot: %v", err)
		}
		snapshot = append(snapshot, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate persistent electrical snapshot: %v", err)
	}
	if len(snapshot) == 0 {
		t.Fatal("fixture electrical snapshot is empty")
	}
	return snapshot
}
