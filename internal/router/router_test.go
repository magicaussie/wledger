package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

// TestNewRegistersRoutes verifies that the full route table (including the
// administrator conversion routes) registers without a duplicate-pattern panic.
func TestNewRegistersRoutes(t *testing.T) {
	mw := middleware.New(nil, nil, nil, nil)
	sm := scs.New()
	h := &handler.Handler{}
	r := New(mw, sm, h)
	if r == nil {
		t.Fatal("router is nil")
	}

	// A request must route (not panic) even with the stub handler.
	req := httptest.NewRequest(http.MethodGet, "/hardware/conversion", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
}
