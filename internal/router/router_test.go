package router

import (
	"net/http"
	"net/http/httptest"
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
