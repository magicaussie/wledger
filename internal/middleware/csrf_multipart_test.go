package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

// TestRequireCSRFBoundsMultipartBody verifies that the CSRF middleware refuses
// an oversized multipart request before spooling it. Reading a form token
// requires parsing the multipart body, but ParseMultipartForm's argument is only
// a memory threshold, so the middleware now caps the whole body first.
func TestRequireCSRFBoundsMultipartBody(t *testing.T) {
	sm := scs.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := &Manager{Session: sm, UIError: uierror.New(logger), Logger: logger}

	// Establish an authenticated session (authenticated requests are the ones
	// the middleware will parse).
	setup := sm.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sm.Put(r.Context(), config.SessionKeyUserID, int64(1))
	}))
	setupRR := httptest.NewRecorder()
	setup.ServeHTTP(setupRR, httptest.NewRequest(http.MethodGet, "/", nil))
	cookies := setupRR.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie was set")
	}

	guarded := sm.LoadAndSave(m.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("oversized multipart request reached the next handler")
	})))

	req := httptest.NewRequest(http.MethodPost, "/parts", strings.NewReader("x"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=wledger-test")
	req.ContentLength = csrfMultipartBodyLimit + 1
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	guarded.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
}
