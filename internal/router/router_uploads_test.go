package router

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestUploadsRequireReadAuth verifies that uploaded files obey the read
// authorization policy. With the default private reads, an unauthenticated
// client must not reach them; an authenticated admin receives documents as
// non-renderable downloads; and directory listings are never produced.
func TestUploadsRequireReadAuth(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()

	// Serve uploads from a disposable working directory and place one document.
	t.Chdir(t.TempDir())
	docsDir := filepath.Join("app", "uploads", "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "evil.html"), []byte("<script>alert(1)</script>"), 0o644); err != nil {
		t.Fatal(err)
	}

	env.createUser(t, "admin@test.com", "admin")

	// Guest: reads are private by default, so the upload is not reachable.
	guest := httptest.NewRecorder()
	env.router.ServeHTTP(guest, httptest.NewRequest(http.MethodGet, "/uploads/docs/evil.html", nil))
	if guest.Code != http.StatusSeeOther || guest.Header().Get("Location") != "/login" {
		t.Fatalf("guest upload GET = %d loc=%q, want 303 /login", guest.Code, guest.Header().Get("Location"))
	}

	cookies := env.login(t, "admin@test.com")

	// Authenticated admin: the document downloads with a non-renderable type.
	req := httptest.NewRequest(http.MethodGet, "/uploads/docs/evil.html", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	env.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin upload GET = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("document content type = %q, want application/octet-stream", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != "attachment" {
		t.Errorf("document content disposition = %q, want attachment", cd)
	}

	// Directory listings are never produced.
	listReq := httptest.NewRequest(http.MethodGet, "/uploads/docs/", nil)
	for _, c := range cookies {
		listReq.AddCookie(c)
	}
	list := httptest.NewRecorder()
	env.router.ServeHTTP(list, listReq)
	if list.Code != http.StatusNotFound {
		t.Errorf("directory listing = %d, want 404", list.Code)
	}
}

// TestUploadsHandlerRefusesHiddenControlPlane verifies that the hidden restore
// control plane inside the uploads tree (the in-mount generation staging
// directory and the durable restore journal) is never reachable over HTTP, even
// for an authenticated administrator.
func TestUploadsHandlerRefusesHiddenControlPlane(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	t.Chdir(t.TempDir())

	control := filepath.Join("app", "uploads", ".wledger-restore")
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(control, ".wledger-restore-journal.json"), []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	env.createUser(t, "hidden-admin@test.com", "admin")
	cookies := env.login(t, "hidden-admin@test.com")

	for _, path := range []string{
		"/uploads/.wledger-restore/.wledger-restore-journal.json",
		"/uploads/.wledger-restore",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		env.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("hidden control-plane GET %s = %d, want 404", path, rec.Code)
		}
	}
}

// TestSetupRejectsWeakPassword verifies that the initial administrator setup
// enforces the shared password policy. Previously only bcrypt was invoked, so an
// empty or trivially short password created a working administrator account.
func TestSetupRejectsWeakPassword(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()

	get := httptest.NewRecorder()
	env.router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/setup", nil))
	cookies := get.Result().Cookies()
	if get.Code != http.StatusOK || len(cookies) == 0 {
		t.Fatalf("setup GET = %d with %d cookies", get.Code, len(cookies))
	}
	// With no users yet, the FirstRunCheck middleware redirects other paths to
	// /setup, so the token is read from the rendered setup form rather than the
	// (unavailable) csrf-probe route.
	match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
	if len(match) != 2 {
		t.Fatalf("setup page is missing a csrf token: %s", get.Body.String())
	}
	token := match[1]

	form := url.Values{"email": {"admin@test.com"}, "password": {"short"}, "csrf_token": {token}}
	req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	env.router.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("weak setup password = %d body=%s, want 400", rr.Code, rr.Body.String())
	}

	count, err := env.store.CountUsers(t.Context())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Fatalf("users after rejected setup = %d, want 0", count)
	}
}
