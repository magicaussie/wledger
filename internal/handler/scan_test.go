package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestHandleScan_RoutingAndIdentityValidation(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	ctx := context.Background()
	ctrl, err := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "Legacy controller", IpAddress: "127.0.0.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	containerID, err := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Legacy container", ControllerID: ctrl.ID, SegmentID: 0})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (20, 'Physical cabinet', NULL, 'grid', 0, 0, 0, 0)`, nil},
		{`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (21, 20, 'Physical drawer', 0, 0, 0, 10, 5, 0)`, nil},
		{`INSERT INTO bins (id, name, container_id, led_index) VALUES (22, 'Physical bin', NULL, NULL)`, nil},
		{`INSERT INTO parts (id, name, barcode_data) VALUES (23, 'Physical part', 'old-barcode')`, nil},
	} {
		if _, err := dbConn.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert scan fixture: %v", err)
		}
	}

	cases := []struct {
		code     string
		wantPath string
	}{
		{"wledger:bin:22", "/parts?bin=22"},
		{"wledger:cabinet:" + strconv.FormatInt(ctrl.ID, 10), "/hardware/" + strconv.FormatInt(ctrl.ID, 10) + "/grid"},
		{"wledger:drawer:" + strconv.FormatInt(containerID, 10), "/drawers/" + strconv.FormatInt(containerID, 10)},
		{"wledger:physical-cabinet:20", "/storage/cabinets/20"},
		{"wledger:physical-drawer:21", "/storage/drawers/21"},
		{"wledger:part-id:23", "/parts/23"},
		{"wledger:part:old-barcode", "/parts/23"},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/scan?q="+url.QueryEscape(tc.code), nil)
			rr := httptest.NewRecorder()
			h.HandleScan(rr, req)
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("expected 303, got %d: %s", rr.Code, rr.Body.String())
			}
			if loc := rr.Header().Get("Location"); loc != tc.wantPath {
				t.Errorf("expected redirect to %s, got %s", tc.wantPath, loc)
			}
		})
	}

	for _, tc := range []struct {
		code string
		want int
	}{
		{"wledger:bin:not-an-id", http.StatusBadRequest},
		{"wledger:bin:0", http.StatusBadRequest},
		{"wledger:bin:+22", http.StatusBadRequest},
		{"wledger:bin:022", http.StatusBadRequest},
		{"wledger:bin:999", http.StatusNotFound},
		{"wledger:cabinet:not-an-id", http.StatusBadRequest},
		{"wledger:cabinet:999", http.StatusNotFound},
		{"wledger:drawer:999", http.StatusNotFound},
		{"wledger:physical-cabinet:nope", http.StatusBadRequest},
		{"wledger:physical-cabinet:999", http.StatusNotFound},
		{"wledger:physical-drawer:999", http.StatusNotFound},
		{"wledger:part-id:nope", http.StatusBadRequest},
		{"wledger:part-id:999", http.StatusNotFound},
	} {
		t.Run("invalid/unknown/"+tc.code, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/scan?q="+url.QueryEscape(tc.code), nil)
			rr := httptest.NewRecorder()
			h.HandleScan(rr, req)
			if rr.Code != tc.want {
				t.Errorf("status = %d, want %d (%s)", rr.Code, tc.want, rr.Body.String())
			}
		})
	}

	if _, err := dbConn.ExecContext(ctx, `DELETE FROM drawers WHERE id = 21`); err != nil {
		t.Fatalf("delete physical drawer fixture: %v", err)
	}
	if _, err := dbConn.ExecContext(ctx, `DELETE FROM cabinets WHERE id = 20`); err != nil {
		t.Fatalf("delete physical cabinet fixture: %v", err)
	}
	for _, code := range []string{"wledger:physical-cabinet:20", "wledger:physical-drawer:21"} {
		req := httptest.NewRequest(http.MethodGet, "/scan?q="+url.QueryEscape(code), nil)
		rr := httptest.NewRecorder()
		h.HandleScan(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("deleted identity %s returned %d, want 404", code, rr.Code)
		}
	}
}

func TestHandleQRRoutesGenerateOnlyExistingIdentities(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	ctx := context.Background()
	controller, err := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "Legacy controller", IpAddress: "127.0.0.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	containerID, err := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Legacy container", ControllerID: controller.ID, SegmentID: 0})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	for _, query := range []string{
		`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (10, 'Physical cabinet', NULL, 'grid', 0, 0, 0, 0)`,
		`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (11, 10, 'Physical drawer', 0, 0, 0, 10, 5, 0)`,
		`INSERT INTO bins (id, name, container_id, led_index) VALUES (12, 'Physical bin', NULL, NULL)`,
		`INSERT INTO parts (id, name) VALUES (13, 'Physical part')`,
	} {
		if _, err := dbConn.ExecContext(ctx, query); err != nil {
			t.Fatalf("insert QR fixture: %v", err)
		}
	}

	r := chi.NewRouter()
	r.Get("/bin/{id}/qr", h.HandleBinQR)
	r.Get("/cabinet/{id}/qr", h.HandleCabinetQR)
	r.Get("/drawer/{id}/qr", h.HandleDrawerQR)
	r.Get("/parts/{id}/qr", h.HandlePartQR)
	r.Get("/storage/cabinets/{id}/qr", h.HandlePhysicalCabinetQR)
	r.Get("/storage/drawers/{id}/qr", h.HandlePhysicalDrawerQR)
	r.Get("/scan/barcode/{type}/{id}", h.HandleEntityBarcode)
	r.Get("/labels/{type}/{id}", h.HandleScanLabel)

	paths := []string{
		"/bin/12/qr",
		"/cabinet/" + strconv.FormatInt(controller.ID, 10) + "/qr",
		"/drawer/" + strconv.FormatInt(containerID, 10) + "/qr",
		"/parts/13/qr",
		"/storage/cabinets/10/qr",
		"/storage/drawers/11/qr",
		"/scan/barcode/bin/12",
		"/scan/barcode/physical-cabinet/10",
		"/scan/barcode/physical-drawer/11",
		"/scan/barcode/part/13",
	}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d: %s", path, rr.Code, rr.Body.String())
			continue
		}
		if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s: expected image/png, got %s", path, ct)
		}
		if rr.Body.Len() == 0 {
			t.Errorf("%s: expected a PNG body", path)
		}
	}

	for _, path := range []string{"/bin/999/qr", "/cabinet/999/qr", "/drawer/999/qr", "/storage/cabinets/999/qr", "/storage/drawers/999/qr", "/scan/barcode/bin/999", "/scan/barcode/physical-cabinet/999", "/scan/barcode/physical-drawer/999", "/scan/barcode/part/999"} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusNotFound {
			t.Errorf("unknown QR identity %s returned %d, want 404", path, rr.Code)
		}
	}

	for _, path := range []string{"/labels/physical-cabinet/10", "/labels/physical-drawer/11", "/labels/bin/12", "/labels/part/13"} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusOK {
			t.Errorf("%s: expected printable label, got %d: %s", path, rr.Code, rr.Body.String())
			continue
		}
		if !strings.Contains(rr.Body.String(), "Code 128") || !strings.Contains(rr.Body.String(), "QR code") {
			t.Errorf("%s: printable label must contain both QR and Code 128 output", path)
		}
	}
}

func TestHandleScanResolveReturnsOneTypedStableIdentity(t *testing.T) {
	h, conn := setupPartTest(t)
	defer conn.Close()
	defer cleanupPartTest()

	ctx := context.Background()
	controller, err := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "Electrical controller", IpAddress: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	container, err := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Electrical container", ControllerID: controller.ID, SegmentID: 0})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO cabinets (id,name,coordinate_system,layout_revision,electrical_revision,display_order,archived) VALUES (20,'Physical cabinet','grid',0,0,0,0)`,
		`INSERT INTO drawers (id,cabinet_id,label,display_order,x,y,width,height,archived) VALUES (21,20,'Physical drawer',0,0,0,10,5,0)`,
		`INSERT INTO bins (id,name,container_id,led_index) VALUES (22,'Physical bin',NULL,NULL)`,
		`INSERT INTO parts (id,name,barcode_data) VALUES (23,'Scanned product','maker-123')`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		code string
		want scanResolution
	}{
		{"wledger:physical-cabinet:20", scanResolution{Type: "physical-cabinet", ID: 20}},
		{"wledger:physical-drawer:21", scanResolution{Type: "physical-drawer", ID: 21, CabinetID: 20}},
		{"wledger:bin:22", scanResolution{Type: "bin", ID: 22}},
		{"wledger:part-id:23", scanResolution{Type: "part", ID: 23}},
		{"maker-123", scanResolution{Type: "part", ID: 23}},
		{"wledger:cabinet:" + strconv.FormatInt(controller.ID, 10), scanResolution{Type: "legacy-controller", ID: controller.ID}},
		{"wledger:drawer:" + strconv.FormatInt(container, 10), scanResolution{Type: "legacy-container", ID: container}},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/scan/resolve?q="+url.QueryEscape(tc.code), nil)
		rr := httptest.NewRecorder()
		h.HandleScanResolve(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200: %s", tc.code, rr.Code, rr.Body.String())
			continue
		}
		var got scanResolution
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Errorf("%s: decode response: %v", tc.code, err)
			continue
		}
		if got.Type != tc.want.Type || got.ID != tc.want.ID || got.CabinetID != tc.want.CabinetID {
			t.Errorf("%s: got %+v, want %+v", tc.code, got, tc.want)
		}
	}

	for _, tc := range []struct {
		code string
		want int
	}{
		{"wledger:physical-cabinet:20x", http.StatusBadRequest},
		{"wledger:physical-drawer:999", http.StatusNotFound},
		{"wledger:part-id:999", http.StatusNotFound},
		{"unknown-maker-code", http.StatusNotFound},
	} {
		rr := httptest.NewRecorder()
		h.HandleScanResolve(rr, httptest.NewRequest(http.MethodGet, "/scan/resolve?q="+url.QueryEscape(tc.code), nil))
		if rr.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", tc.code, rr.Code, tc.want, rr.Body.String())
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO parts (name,barcode_data) VALUES ('Duplicate barcode','maker-123')`); err == nil {
		t.Fatal("duplicate manufacturer barcode unexpectedly inserted")
	}
}
