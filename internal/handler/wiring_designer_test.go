package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wiringdesigner"
)

func TestWiringDesignerVisualSavePreservesControllerOutputsAndRejectsStaleRevision(t *testing.T) {
	h, store, conn := setupHardwareHandler(t)
	defer conn.Close()
	ctx := context.Background()
	created, err := h.Hardware.CreateControllerConfigured(ctx, db.CreateControllerParams{Name: "Designer", IpAddress: "192.0.2.11", Port: sql.NullInt64{Int64: 80, Valid: true}}, controllerconfig.Settings{Outputs: []controllerconfig.Output{{DisplayOrder: 0, Name: "Output A", PixelCount: 6, Direction: "forward", Pattern: "linear"}}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := h.Hardware.GetControllerSettings(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Outputs) != 1 {
		t.Fatalf("outputs=%+v", before.Outputs)
	}
	layout := wiringdesigner.Layout{Version: 1, Revision: 0, Width: 500, Height: 300, Outputs: []wiringdesigner.OutputLayout{{OutputID: before.Outputs[0].ID, X: 10, Y: 10, Width: 200, Height: 100, Rotation: 90, Axis: "rows", Rows: []int64{2, 4}, Sections: []int64{3, 3}}}, Jumpers: []wiringdesigner.Jumper{{ID: "j1", Points: []wiringdesigner.Point{{X: 210, Y: 50}, {X: 300, Y: 50}}}}}
	dbOutputs, err := h.Queries.GetControllerOutputs(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	topology := make([]wiringdesigner.TopologyOutput, 0, len(dbOutputs))
	for _, o := range dbOutputs {
		topology = append(topology, wiringdesigner.TopologyOutput{ID: o.ID, DisplayOrder: o.DisplayOrder, PixelCount: o.PixelCount, Direction: o.Direction, Pattern: o.Pattern, Rows: o.Rows, Columns: o.Columns, CustomOrder: o.CustomOrderJson})
	}
	layout.TopologySignature = wiringdesigner.TopologySignature(topology)
	body, _ := json.Marshal(layout)
	router := chi.NewRouter()
	router.Post("/hardware/{id}/wiring-designer", h.HandleWiringDesignerSave)
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.FormatInt(created.ID, 10)+"/wiring-designer", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	first := request()
	if first.Code != 200 {
		t.Fatalf("save status %d: %s", first.Code, first.Body.String())
	}
	var saved wiringdesigner.Layout
	if err = json.Unmarshal(first.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 {
		t.Fatalf("revision=%d", saved.Revision)
	}
	second := request()
	if second.Code != 409 || !strings.Contains(second.Body.String(), "changed in another session") {
		t.Fatalf("stale save status %d: %s", second.Code, second.Body.String())
	}
	after, err := h.Hardware.GetControllerSettings(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.Outputs, before.Outputs) {
		t.Fatalf("visual save changed electrical outputs: before=%+v after=%+v", before.Outputs, after.Outputs)
	}
	flag, err := store.GetFlag(ctx, designerFlagKey(created.ID))
	if err != nil {
		t.Fatal(err)
	}
	var persisted wiringdesigner.Layout
	if err = json.Unmarshal([]byte(flag), &persisted); err != nil || persisted.Outputs[0].Rows[1] != 4 || len(persisted.Jumpers) != 1 {
		t.Fatalf("persisted metadata=%s err=%v", flag, err)
	}
}
