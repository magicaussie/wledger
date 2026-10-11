package handler_test

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/router"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

// storageFixture is one migrated, file-backed SQLite database plus a handler
// whose only dependencies are the store and an error responder, which is all
// the read-only physical storage handlers touch.
type storageFixture struct {
	t    *testing.T
	conn *sql.DB
	h    *handler.Handler
}

func newStorageFixture(t *testing.T) *storageFixture {
	t.Helper()

	conn, err := db.Open("file:" + t.TempDir() + "/storage.db")
	if err != nil {
		t.Fatalf("open storage fixture database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate storage fixture: %v", err)
	}
	if err := i18n.InitWithDir("../../locales"); err != nil {
		t.Fatalf("initialize fixture locales: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &handler.Handler{
		Logger:   logger,
		Queries:  db.NewStore(conn),
		Database: conn,
		UIError:  uierror.New(logger),
	}
	return &storageFixture{t: t, conn: conn, h: h}
}

func (f *storageFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.ExecContext(context.Background(), query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

// cabinet inserts a physical cabinet. controllerID is nil for a draft cabinet.
func (f *storageFixture) cabinet(id int64, name string, displayOrder, archived int64) {
	f.t.Helper()
	f.exec(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (?, ?, NULL, ?, 0, 0, ?, ?)`,
		id, name, "grid", displayOrder, archived)
}

// drawer inserts a drawer occupying a 10x5 cabinet-local rectangle.
func (f *storageFixture) drawer(id, cabinetID int64, label string, displayOrder, archived int64) {
	f.t.Helper()
	f.exec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (?, ?, ?, ?, 0.0, 0.0, 10.0, 5.0, ?)`,
		id, cabinetID, label, displayOrder, archived)
}

// bin inserts a bin with NO legacy electrical association: container_id and
// led_index are NULL, so its physical visibility must come from the attachment
// row alone.
func (f *storageFixture) bin(id int64, name string) {
	f.t.Helper()
	f.exec(`INSERT INTO bins (id, name, container_id, led_index) VALUES (?, ?, NULL, NULL)`, id, name)
}

func (f *storageFixture) part(id int64, name string) {
	f.t.Helper()
	f.exec(`INSERT INTO parts (id, name) VALUES (?, ?)`, id, name)
}

func (f *storageFixture) assignment(partID, binID, quantity int64) {
	f.t.Helper()
	f.exec(`INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES (?, ?, ?)`, partID, binID, quantity)
}

func (f *storageFixture) attach(binID, drawerID int64, displayOrder int64) {
	f.t.Helper()
	f.exec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (?, ?, 0.0, 0.0, 1.0, 1.0, ?)`,
		binID, drawerID, displayOrder)
}

// get serves a GET against the storage browse routes registered exactly as the
// router mounts them.
func (f *storageFixture) get(path string) *httptest.ResponseRecorder {
	f.t.Helper()
	r := chi.NewRouter()
	r.Get("/storage/cabinets", f.h.HandleStorageCabinets)
	r.Get("/storage/cabinets/{id}", f.h.HandleStorageCabinet)
	r.Get("/storage/drawers/{id}", f.h.HandleStorageDrawer)

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

func assertContains(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("response body missing %q", want)
		}
	}
}

// TestStorageCabinets_RendersActiveHierarchy proves GET /storage/cabinets
// renders active cabinets with their drawers in the browsable layout, and that
// the archived cabinet is excluded from the active listing.
func TestStorageCabinets_RendersActiveHierarchy(t *testing.T) {
	f := newStorageFixture(t)
	f.cabinet(1, "Cabinet Alpha", 0, 0)
	f.cabinet(2, "Cabinet Retired", 1, 1)
	f.drawer(10, 1, "Drawer Ten", 0, 0)

	rr := f.get("/storage/cabinets")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /storage/cabinets = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	assertContains(t, rr.Body.String(), "Cabinet Alpha", "#1")
	if strings.Contains(rr.Body.String(), "Cabinet Retired") {
		t.Errorf("active listing must not include the archived cabinet")
	}
}

// TestStorageCabinet_DetailAndErrors proves GET /storage/cabinets/{id} renders
// the cabinet's active drawers using the stable ids, returns 404 for an absent
// cabinet and 400 for a non-numeric id.
func TestStorageCabinet_DetailAndErrors(t *testing.T) {
	f := newStorageFixture(t)
	f.cabinet(1, "Cabinet Alpha", 0, 0)
	f.drawer(10, 1, "Drawer Ten", 0, 0)
	f.drawer(11, 1, "Drawer Archived", 0, 1)

	rr := f.get("/storage/cabinets/1")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /storage/cabinets/1 = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	assertContains(t, rr.Body.String(), "Cabinet Alpha", "Drawer Ten", "/storage/drawers/10")
	if strings.Contains(rr.Body.String(), "Drawer Archived") {
		t.Errorf("cabinet detail must not list archived drawers")
	}

	if rr := f.get("/storage/cabinets/999"); rr.Code != http.StatusNotFound {
		t.Errorf("absent cabinet = %d, want 404", rr.Code)
	}
	if rr := f.get("/storage/cabinets/not-a-number"); rr.Code != http.StatusBadRequest {
		t.Errorf("invalid cabinet id = %d, want 400", rr.Code)
	}
}

// TestStorageDrawer_PhysicalAttachmentWithNullContainer proves GET
// /storage/drawers/{id} lists a bin attached only through bin_drawer_attachments
// even when the bin's legacy container_id is NULL, preserves the bin's stable
// identity and shows its inventory contents and exact quantities.
func TestStorageDrawer_PhysicalAttachmentWithNullContainer(t *testing.T) {
	f := newStorageFixture(t)
	f.cabinet(1, "Cabinet Alpha", 0, 0)
	f.drawer(10, 1, "Drawer Ten", 0, 0)
	f.bin(50, "Null Container Bin")
	f.part(70, "Resistor 10k")
	f.part(71, "Capacitor 1u")
	f.assignment(70, 50, 42)
	f.assignment(71, 50, 0)
	f.attach(50, 10, 0)

	rr := f.get("/storage/drawers/10")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /storage/drawers/10 = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertContains(t, body, "Drawer Ten", "Null Container Bin", "Bin #50", "Resistor 10k", "Capacitor 1u", "/parts/70", "/parts/71")
	if !strings.Contains(body, "×42") {
		t.Errorf("drawer body must show the exact quantity 42")
	}
	if !strings.Contains(body, "×0") {
		t.Errorf("drawer body must preserve the zero quantity")
	}

	if rr := f.get("/storage/drawers/999"); rr.Code != http.StatusNotFound {
		t.Errorf("absent drawer = %d, want 404", rr.Code)
	}
	if rr := f.get("/storage/drawers/not-a-number"); rr.Code != http.StatusBadRequest {
		t.Errorf("invalid drawer id = %d, want 400", rr.Code)
	}
}

// TestStorageDrawer_UnattachedBinNotListed proves the drawer view is physical:
// a bin that is not attached to the drawer is never shown in it, even when it
// shares a legacy container with an attached bin.
func TestStorageDrawer_UnattachedBinNotListed(t *testing.T) {
	f := newStorageFixture(t)
	f.cabinet(1, "Cabinet Alpha", 0, 0)
	f.drawer(10, 1, "Drawer Ten", 0, 0)
	f.drawer(11, 1, "Drawer Eleven", 1, 0)
	f.bin(50, "Attached Bin")
	f.bin(51, "Elsewhere Bin")
	f.attach(50, 10, 0)
	f.attach(51, 11, 0)

	rr := f.get("/storage/drawers/10")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /storage/drawers/10 = %d, want 200", rr.Code)
	}
	assertContains(t, rr.Body.String(), "Attached Bin")
	if strings.Contains(rr.Body.String(), "Elsewhere Bin") {
		t.Errorf("drawer 10 must not list a bin attached to another drawer")
	}
}

func assertRegistered(t *testing.T, label string, got, want http.Handler) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is not registered", label)
		return
	}
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(want).Pointer() {
		t.Errorf("%s is registered to a different handler than the original", label)
	}
}

// TestPhysicalStorageRoutesAndLegacyRoutesRegistered proves the real route
// table exposes the new read-only storage routes and that the pre-existing
// legacy /drawers/{id} detail and /drawers/{id}/locate handlers remain wired to
// their original functions.
func TestPhysicalStorageRoutesAndLegacyRoutesRegistered(t *testing.T) {
	t.Setenv("WLEDGER_API_TOKEN", "")

	h := &handler.Handler{}
	r := router.New(middleware.New(nil, nil, nil, nil), scs.New(), h)

	var gotCabinets, gotCabinet, gotDrawerStorage, gotBinPicker http.Handler
	var gotLegacyDrawer, gotLegacyLocate http.Handler

	err := chi.Walk(r, func(method, route string, handler http.Handler, _ ...func(http.Handler) http.Handler) error {
		switch {
		case method == http.MethodGet && route == "/storage/cabinets":
			gotCabinets = handler
		case method == http.MethodGet && route == "/storage/cabinets/{id}":
			gotCabinet = handler
		case method == http.MethodGet && route == "/storage/drawers/{id}":
			gotDrawerStorage = handler
		case method == http.MethodGet && route == "/storage/bin_picker":
			gotBinPicker = handler
		case method == http.MethodGet && route == "/drawers/{id}":
			gotLegacyDrawer = handler
		case method == http.MethodPost && route == "/drawers/{id}/locate":
			gotLegacyLocate = handler
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}

	assertRegistered(t, "GET /storage/cabinets", gotCabinets, http.HandlerFunc(h.HandleStorageCabinets))
	assertRegistered(t, "GET /storage/cabinets/{id}", gotCabinet, http.HandlerFunc(h.HandleStorageCabinet))
	assertRegistered(t, "GET /storage/drawers/{id}", gotDrawerStorage, http.HandlerFunc(h.HandleStorageDrawer))
	assertRegistered(t, "GET /storage/bin_picker", gotBinPicker, http.HandlerFunc(h.HandleStorageBinPicker))
	assertRegistered(t, "GET /drawers/{id}", gotLegacyDrawer, http.HandlerFunc(h.HandleDrawerDetail))
	assertRegistered(t, "POST /drawers/{id}/locate", gotLegacyLocate, http.HandlerFunc(h.HandleDrawerLocate))
}

// TestStorageRoutesRejectWrites proves the browse routes are read-only: a POST
// to a storage browse path is not routed to the GET handler.
func TestStorageRoutesRejectWrites(t *testing.T) {
	f := newStorageFixture(t)

	r := chi.NewRouter()
	r.Get("/storage/cabinets", f.h.HandleStorageCabinets)
	req := httptest.NewRequest(http.MethodPost, "/storage/cabinets", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /storage/cabinets = %d, want 405", rr.Code)
	}
}
