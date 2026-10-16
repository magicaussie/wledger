package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestWiringDesignerPixelResolvesUnallocatedCanonicalAddress(t *testing.T) {
	for caseIndex, tc := range []struct {
		pattern, direction string
		custom             []int64
		want               []int64
	}{
		{"linear", "forward", nil, []int64{1, 2, 3, 4, 5, 6}},
		{"linear", "reverse", nil, []int64{6, 5, 4, 3, 2, 1}},
		{"serpentine_rows", "forward", nil, []int64{1, 2, 3, 6, 5, 4}},
		{"custom", "forward", []int64{2, 0, 5, 1, 4, 3}, []int64{2, 4, 1, 6, 5, 3}},
	} {
		t.Run(tc.pattern+tc.direction, func(t *testing.T) {
			h, _, conn := setupHardwareHandler(t)
			defer conn.Close()
			ctx := context.Background()
			c, err := h.Hardware.CreateControllerConfigured(ctx, db.CreateControllerParams{Name: "Offline", IpAddress: fmt.Sprintf("192.0.2.%d", 150+caseIndex), Port: sql.NullInt64{Int64: 80, Valid: true}}, controllerconfig.Settings{Outputs: []controllerconfig.Output{{Name: "Output", PixelCount: 6, Pattern: tc.pattern, Direction: tc.direction, Rows: 2, Columns: 3, CustomOrder: tc.custom}}})
			if err != nil {
				t.Fatal(err)
			}
			exec := func(statement string, args ...any) {
				t.Helper()
				if _, e := conn.Exec(statement, args...); e != nil {
					t.Fatal(e)
				}
			}
			exec("INSERT INTO cabinets(id,name,controller_id) VALUES(?,'Cabinet',?)", c.ID, c.ID)
			exec("INSERT INTO cabinet_controllers(cabinet_id,controller_id,slot,strings_configured) VALUES(?,?,0,1)", c.ID, c.ID)
			exec("INSERT INTO cabinet_led_strings(cabinet_id,controller_id,name,display_order,pixel_count,controller_output_id) SELECT controller_id,controller_id,'String',0,6,id FROM controller_outputs WHERE controller_id=?", c.ID)
			router := chi.NewRouter()
			router.Get("/hardware/{id}/wiring-designer/pixel", h.HandleWiringDesignerPixel)
			for pixel, want := range tc.want {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/hardware/%d/wiring-designer/pixel?pixel=%d", c.ID, pixel), nil))
				if w.Code != 200 {
					t.Fatalf("%d: %d %s", pixel, w.Code, w.Body.String())
				}
				var body struct {
					Addresses []struct {
						Pixel int64 `json:"pixel"`
					} `json:"cabinet_addresses"`
					Warnings []string `json:"warnings"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if len(body.Addresses) != 1 || body.Addresses[0].Pixel != want || len(body.Warnings) != 0 {
					t.Fatalf("physical %d want cabinet %d: %s", pixel, want, w.Body.String())
				}
			}
		})
	}
}
