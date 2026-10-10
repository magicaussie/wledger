package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
)

// fakeDrawerWLED is a minimal wled.Service that records the last LocateDrawer
// call so the handler can be tested without touching a real controller.
type fakeDrawerWLED struct {
	locateCalls      int
	locateController int64
	locateContainer  int64
}

func (f *fakeDrawerWLED) LocatePart(ctx context.Context, partID int64) error { return nil }
func (f *fakeDrawerWLED) LocateBin(ctx context.Context, controllerID, binID int64) error {
	return nil
}
func (f *fakeDrawerWLED) LocateDrawer(ctx context.Context, controllerID, containerID int64) error {
	f.locateCalls++
	f.locateController = controllerID
	f.locateContainer = containerID
	return nil
}
func (f *fakeDrawerWLED) FlashError(ctx context.Context, controllerID, binID int64) error {
	return nil
}
func (f *fakeDrawerWLED) GlobalOff(ctx context.Context) error               { return nil }
func (f *fakeDrawerWLED) Ping(ctx context.Context, ip string) (bool, error) { return true, nil }

func TestHandleDrawerDetail(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	c, err := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "Cabinet 1", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	cont, err := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Drawer 1", ControllerID: c.ID, SegmentID: 0})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	bin, err := h.Queries.CreateBin(ctx, db.CreateBinParams{Name: "Bin A", ContainerID: cont})
	if err != nil {
		t.Fatalf("create bin: %v", err)
	}
	part, err := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Resistor 10k"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if err := h.Queries.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   part,
		BinID:    sql.NullInt64{Int64: bin, Valid: true},
		Quantity: 42,
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	r := chi.NewRouter()
	r.Get("/drawers/{id}", h.HandleDrawerDetail)

	req := httptest.NewRequest(http.MethodGet, "/drawers/"+strconv.FormatInt(cont, 10), nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	for _, want := range []string{"Drawer 1", "Cabinet 1", "Bin A", "Resistor 10k"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %q", want)
		}
	}

	// Manual-only locate: the served drawer page must not auto-trigger a locate on
	// load, and the explicit Locate control must remain wired to the endpoint.
	if strings.Contains(body, `hx-trigger="load"`) {
		t.Error("drawer page must not auto-trigger LED locate on load")
	}
	if !strings.Contains(body, fmt.Sprintf(`hx-post="/drawers/%d/locate"`, cont)) {
		t.Errorf("explicit Locate control not wired to /drawers/%d/locate", cont)
	}
}

func TestHandleDrawerDetail_NotFound(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	r := chi.NewRouter()
	r.Get("/drawers/{id}", h.HandleDrawerDetail)

	req := httptest.NewRequest(http.MethodGet, "/drawers/999", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleDrawerDetail_InvalidID(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	r := chi.NewRouter()
	r.Get("/drawers/{id}", h.HandleDrawerDetail)

	req := httptest.NewRequest(http.MethodGet, "/drawers/not-a-number", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestHandleDrawerLocate(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()
	ctx := context.Background()

	fw := &fakeDrawerWLED{}
	h.WLED = fw

	c, err := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "Cabinet 1", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	cont, err := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "Drawer 1", ControllerID: c.ID, SegmentID: 0})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/drawers/{id}/locate", h.HandleDrawerLocate)

	req := httptest.NewRequest(http.MethodPost, "/drawers/"+strconv.FormatInt(cont, 10)+"/locate", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if fw.locateCalls != 1 {
		t.Fatalf("expected LocateDrawer to be called once, got %d", fw.locateCalls)
	}
	if fw.locateController != c.ID {
		t.Errorf("expected controller %d, got %d", c.ID, fw.locateController)
	}
	if fw.locateContainer != cont {
		t.Errorf("expected container %d, got %d", cont, fw.locateContainer)
	}
}

func TestHandleDrawerLocate_NotFound(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	fw := &fakeDrawerWLED{}
	h.WLED = fw

	r := chi.NewRouter()
	r.Post("/drawers/{id}/locate", h.HandleDrawerLocate)

	req := httptest.NewRequest(http.MethodPost, "/drawers/999/locate", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
	if fw.locateCalls != 0 {
		t.Errorf("expected LocateDrawer not to be called, got %d calls", fw.locateCalls)
	}
}
