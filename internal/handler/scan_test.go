package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestHandleScan_Routing(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	ctx := context.Background()

	ctrl, err := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "Cabinet 1", IpAddress: "1.2.3.4"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	cont, err := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Drawer 1", ControllerID: ctrl.ID, SegmentID: 0})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	ctrlID := strconv.FormatInt(ctrl.ID, 10)
	cases := []struct {
		code     string
		wantPath string
	}{
		{"wledger:bin:5", "/parts?bin=5"},
		{"wledger:cabinet:" + ctrlID, "/hardware/" + ctrlID + "/grid"},
		{"wledger:drawer:" + strconv.FormatInt(cont, 10), "/hardware/" + ctrlID + "/grid"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/scan?q="+url.QueryEscape(tc.code), nil)
		rr := httptest.NewRecorder()
		h.HandleScan(rr, req)

		if rr.Code != http.StatusSeeOther {
			t.Errorf("%s: expected 303, got %d", tc.code, rr.Code)
			continue
		}
		if loc := rr.Header().Get("Location"); loc != tc.wantPath {
			t.Errorf("%s: expected redirect to %s, got %s", tc.code, tc.wantPath, loc)
		}
	}
}

func TestHandleCabinetAndDrawerQR(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	r := chi.NewRouter()
	r.Get("/cabinet/{id}/qr", h.HandleCabinetQR)
	r.Get("/drawer/{id}/qr", h.HandleDrawerQR)

	for _, path := range []string{"/cabinet/1/qr", "/drawer/1/qr"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, rr.Code)
			continue
		}
		if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
			t.Errorf("%s: expected image/png, got %s", path, ct)
		}
		if rr.Body.Len() == 0 {
			t.Errorf("%s: expected a PNG body", path)
		}
	}
}
