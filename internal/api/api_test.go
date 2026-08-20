package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// fakeWLED is a minimal in-memory wled.Service used to exercise the API.
type fakeWLED struct {
	locatedPart int64
	locatedBin  int64
	off         bool
}

func (f *fakeWLED) LocatePart(ctx context.Context, partID int64) error {
	f.locatedPart = partID
	return nil
}
func (f *fakeWLED) LocateBin(ctx context.Context, controllerID, binID int64) error {
	f.locatedBin = binID
	return nil
}
func (f *fakeWLED) GlobalOff(ctx context.Context) error {
	f.off = true
	return nil
}
func (f *fakeWLED) Ping(ctx context.Context, ip string) (bool, error) { return true, nil }

func setupAPI(t *testing.T) (*httptest.Server, *fakeWLED, db.Store) {
	t.Helper()
	os.Setenv("WLEDGER_API_TOKEN", "testtoken")
	t.Cleanup(func() { os.Unsetenv("WLEDGER_API_TOKEN") })

	conn, err := db.Open("file:apitest?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := db.NewStore(conn)

	fw := &fakeWLED{}
	h := NewHandler(store, fw, nil)

	if !Enabled() {
		t.Setenv("WLEDGER_API_TOKEN", "testtoken")
	}

	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv, fw, store
}

func doReq(t *testing.T, srv *httptest.Server, method, path string, auth bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer testtoken")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestTokenFromEnv(t *testing.T) {
	os.Setenv("WLEDGER_API_TOKEN", "sekret")
	defer os.Unsetenv("WLEDGER_API_TOKEN")
	if !Enabled() || Token() != "sekret" {
		t.Fatal("token env handling wrong")
	}
}

func TestHealthAuth(t *testing.T) {
	srv, _, _ := setupAPI(t)

	if r := doReq(t, srv, "GET", "/health", false); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: want 401, got %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/health", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: want 401, got %d", r.StatusCode)
	}
	if r := doReq(t, srv, "GET", "/health", true); r.StatusCode != http.StatusOK {
		t.Errorf("health with token: want 200, got %d", r.StatusCode)
	}
}

func TestGlobalOff(t *testing.T) {
	srv, fw, _ := setupAPI(t)
	if r := doReq(t, srv, "POST", "/global-off", true); r.StatusCode != http.StatusOK {
		t.Fatalf("global-off: want 200, got %d", r.StatusCode)
	}
	if !fw.off {
		t.Error("expected GlobalOff called")
	}
}

func TestLocatePart(t *testing.T) {
	srv, fw, _ := setupAPI(t)
	if r := doReq(t, srv, "POST", "/parts/123/locate", true); r.StatusCode != http.StatusOK {
		t.Errorf("locate part: want 200, got %d", r.StatusCode)
	}
	if fw.locatedPart != 123 {
		t.Errorf("expected locate part 123, got %d", fw.locatedPart)
	}
}

func TestLocateBin(t *testing.T) {
	srv, fw, store := setupAPI(t)

	// A bin that does not exist -> 404.
	if r := doReq(t, srv, "POST", "/bins/999/locate", true); r.StatusCode != http.StatusNotFound {
		t.Errorf("locate missing bin: want 404, got %d", r.StatusCode)
	}

	// Create a controller + container + bin, then locate it.
	ctx := context.Background()
	c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	container, err := store.CreateContainer(ctx, db.CreateContainerParams{Name: "X", ControllerID: c.ID, SegmentID: 0})
	if err != nil {
		t.Fatal(err)
	}
	binID, err := store.CreateBin(ctx, db.CreateBinParams{Name: "B1", ContainerID: container})
	if err != nil {
		t.Fatal(err)
	}

	if r := doReq(t, srv, "POST", "/bins/"+fmt.Sprintf("%d", binID)+"/locate", true); r.StatusCode != http.StatusOK {
		t.Errorf("locate bin: want 200, got %d", r.StatusCode)
	}
	if fw.locatedBin != binID {
		t.Errorf("expected bin locate %d, got %d", binID, fw.locatedBin)
	}
}

func sqlNull(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
