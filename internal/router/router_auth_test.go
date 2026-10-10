package router

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

// fakeWLED records LED operations so the authorization tests never touch real
// hardware. Every method succeeds.
type fakeWLED struct {
	locatePart   int
	locateBin    int
	locateDrawer int
	globalOff    int
}

func (f *fakeWLED) LocatePart(ctx context.Context, partID int64) error { f.locatePart++; return nil }
func (f *fakeWLED) LocateBin(ctx context.Context, controllerID, binID int64) error {
	f.locateBin++
	return nil
}
func (f *fakeWLED) LocateDrawer(ctx context.Context, controllerID, containerID int64) error {
	f.locateDrawer++
	return nil
}
func (f *fakeWLED) FlashError(ctx context.Context, controllerID, binID int64) error { return nil }
func (f *fakeWLED) GlobalOff(ctx context.Context) error                             { f.globalOff++; return nil }
func (f *fakeWLED) Ping(ctx context.Context, ip string) (bool, error)               { return true, nil }

func (f *fakeWLED) total() int {
	return f.locatePart + f.locateBin + f.locateDrawer + f.globalOff
}

// hardwareActionPaths are the LED-operating endpoints that must require a
// write-capable role.
var hardwareActionPaths = []string{
	"/hardware/1/locate",
	"/parts/1/locate",
	"/drawers/1/locate",
	"/hardware/off",
}

type authTestEnv struct {
	router http.Handler
	store  db.Store
	wled   *fakeWLED
	conn   *sql.DB
}

func newAuthTestEnv(t *testing.T) *authTestEnv {
	t.Helper()
	// Keep the machine-to-machine API unmounted so the route table is minimal.
	t.Setenv("WLEDGER_API_TOKEN", "")

	conn, err := db.Open("file:" + t.TempDir() + "/auth.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := db.NewStore(conn)
	if err := store.InitSettings(context.Background()); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	uiErr := uierror.New(logger)
	sm := scs.New()
	sm.Store = auth.NewStore(store)
	sm.Lifetime = time.Hour

	fw := &fakeWLED{}
	h := &handler.Handler{
		Logger:   logger,
		Queries:  store,
		Database: conn,
		Session:  sm,
		WLED:     fw,
		UIError:  uiErr,
	}
	mw := middleware.New(store, sm, logger, uiErr)
	return &authTestEnv{router: New(mw, sm, h), store: store, wled: fw, conn: conn}
}

func (e *authTestEnv) createUser(t *testing.T, email, role string) {
	t.Helper()
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := e.store.CreateUser(context.Background(), db.CreateUserParams{
		Email:                  email,
		PasswordHash:           hash,
		Role:                   role,
		ChangePasswordRequired: sql.NullBool{Bool: false, Valid: true},
	}); err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
}

func (e *authTestEnv) login(t *testing.T, email string) []*http.Cookie {
	t.Helper()
	form := url.Values{"email": {email}, "password": {"secret123"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d, want 303: %s", email, rr.Code, rr.Body.String())
	}
	return rr.Result().Cookies()
}

func (e *authTestEnv) post(path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr
}

// TestHardwareActionsRequireAuthentication verifies that an unauthenticated
// guest cannot reach any LED-operating endpoint (redirected to /login) and that
// no WLED action is triggered.
func TestHardwareActionsRequireAuthentication(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "admin@test.com", "admin")

	for _, p := range hardwareActionPaths {
		rr := env.post(p, nil)
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
			t.Errorf("guest POST %s = %d loc=%q, want 303 /login", p, rr.Code, rr.Header().Get("Location"))
		}
	}
	if env.wled.total() != 0 {
		t.Errorf("guest triggered %d WLED action(s), want 0", env.wled.total())
	}
}

// TestHardwareActionsDeniedForGuestWhenReadIsPublic verifies that making read
// access public does NOT expose the LED-operating endpoints: they remain behind
// authentication.
func TestHardwareActionsDeniedForGuestWhenReadIsPublic(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "admin@test.com", "admin")

	if err := env.store.UpdateGeneralSettings(context.Background(), db.UpdateGeneralSettingsParams{
		RequireAuthForRead: sql.NullBool{Bool: false, Valid: true},
	}); err != nil {
		t.Fatalf("set public read: %v", err)
	}

	for _, p := range hardwareActionPaths {
		rr := env.post(p, nil)
		if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
			t.Errorf("guest (public read) POST %s = %d loc=%q, want 303 /login", p, rr.Code, rr.Header().Get("Location"))
		}
	}
	if env.wled.total() != 0 {
		t.Errorf("guest (public read) triggered %d WLED action(s), want 0", env.wled.total())
	}
}

// TestHardwareActionsDeniedForViewer verifies that an authenticated read-only
// viewer is forbidden (403) from every LED-operating endpoint.
func TestHardwareActionsDeniedForViewer(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "viewer@test.com", "viewer")
	cookies := env.login(t, "viewer@test.com")

	for _, p := range hardwareActionPaths {
		rr := env.post(p, cookies)
		if rr.Code != http.StatusForbidden {
			t.Errorf("viewer POST %s = %d, want 403", p, rr.Code)
		}
	}
	if env.wled.total() != 0 {
		t.Errorf("viewer triggered %d WLED action(s), want 0", env.wled.total())
	}
}

// TestHardwareActionsAllowedForEditorAndAdmin verifies that write-capable roles
// pass authorization and reach the handler (200), using the fake WLED so no
// real LED command is sent.
func TestHardwareActionsAllowedForEditorAndAdmin(t *testing.T) {
	for _, role := range []string{"editor", "admin"} {
		t.Run(role, func(t *testing.T) {
			env := newAuthTestEnv(t)
			defer env.conn.Close()
			env.createUser(t, role+"@test.com", role)

			ctx := context.Background()
			ctrl, err := env.store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
			if err != nil {
				t.Fatalf("create controller: %v", err)
			}
			cont, err := env.store.CreateContainer(ctx, db.CreateContainerParams{
				Name: "D", ControllerID: ctrl.ID, SegmentID: 0, LedCount: 10,
			})
			if err != nil {
				t.Fatalf("create container: %v", err)
			}

			cookies := env.login(t, role+"@test.com")
			paths := []string{
				fmt.Sprintf("/hardware/%d/locate", ctrl.ID),
				"/parts/1/locate",
				fmt.Sprintf("/drawers/%d/locate", cont),
				"/hardware/off",
			}
			for _, p := range paths {
				rr := env.post(p, cookies)
				if rr.Code != http.StatusOK {
					t.Errorf("%s POST %s = %d, want 200: %s", role, p, rr.Code, rr.Body.String())
				}
			}
			if env.wled.locateBin != 1 || env.wled.locatePart != 1 || env.wled.locateDrawer != 1 || env.wled.globalOff != 1 {
				t.Errorf("%s WLED calls = bin:%d part:%d drawer:%d off:%d, want 1 each",
					role, env.wled.locateBin, env.wled.locatePart, env.wled.locateDrawer, env.wled.globalOff)
			}
		})
	}
}
