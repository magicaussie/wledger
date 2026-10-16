package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWiringDesignerRolesAndCSRF(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	for _, role := range []string{"viewer", "editor", "admin"} {
		email := "designer-" + role + "@test.com"
		env.createUser(t, email, role)
		cookies := env.login(t, email)
		token := env.csrfToken(t, cookies)
		for _, path := range []string{"", "/review", "/apply"} {
			for _, csrf := range []string{token, ""} {
				r := httptest.NewRequest(http.MethodPost, "/hardware/1/wiring-designer"+path, strings.NewReader(`{}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-CSRF-Token", csrf)
				for _, cookie := range cookies {
					r.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				env.router.ServeHTTP(w, r)
				denied := role == "viewer" || (path != "" && role != "admin") || csrf == ""
				if denied && w.Code != 403 {
					t.Fatalf("%s %s csrf=%t: got %d", role, path, csrf != "", w.Code)
				}
				if !denied && (w.Code == 403 || w.Code == 405) {
					t.Fatalf("authorized %s %s: %d", role, path, w.Code)
				}
			}
		}
	}
	if env.wled.total() != 0 {
		t.Fatal("designer authorization contacted controllers")
	}
}
