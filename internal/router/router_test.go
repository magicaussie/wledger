package router

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewRegistersRoutes verifies that the full route table (including the
// administrator conversion routes) registers without a duplicate-pattern panic.
func TestNewRegistersRoutes(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "route-test@test.com", "admin")

	// A request must route through the real middleware stack without a duplicate-pattern panic.
	req := httptest.NewRequest(http.MethodGet, "/hardware/conversion", nil)
	rr := httptest.NewRecorder()
	env.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated conversion page = %d, location=%q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestClientForwardingHeadersDoNotChangeLoggedRemoteAddr(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()

	var logs bytes.Buffer
	env.mw.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.RemoteAddr = "192.0.2.10:4321"
	req.Header.Set("Forwarded", "for=198.51.100.20;proto=https")
	req.Header.Set("X-Forwarded-For", "198.51.100.21")
	req.Header.Set("X-Real-IP", "198.51.100.22")

	env.router.ServeHTTP(httptest.NewRecorder(), req)

	logged := logs.String()
	if !strings.Contains(logged, "ip=192.0.2.10:4321") {
		t.Fatalf("request log does not use the transport peer address: %s", logged)
	}
	if strings.Contains(logged, "198.51.100.") {
		t.Fatalf("client-supplied forwarding header influenced the logged client address: %s", logged)
	}
}
