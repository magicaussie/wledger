package router

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGuidedControllerSetupAdminAndCSRF(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	for _, role := range []string{"admin", "editor", "viewer"} {
		email := "setup-" + role + "@example.test"
		env.createUser(t, email, role)
		cookies := env.login(t, email)
		token := env.csrfToken(t, cookies)
		for _, path := range []string{"/hardware/new", "/hardware/setup", "/hardware/setup/discover"} {
			method := "POST"
			if path == "/hardware/new" {
				method = "GET"
			}
			for _, csrf := range []string{token, ""} {
				req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-CSRF-Token", csrf)
				for _, cookie := range cookies {
					req.AddCookie(cookie)
				}
				w := httptest.NewRecorder()
				env.router.ServeHTTP(w, req)
				denied := role != "admin" || (method == "POST" && csrf == "")
				if denied && w.Code != 403 {
					t.Fatalf("%s %s csrf=%t got%d", role, path, csrf != "", w.Code)
				}
				if !denied && (w.Code == 403 || w.Code == 404 || w.Code == 405) {
					t.Fatalf("allowed %s %s got%d", role, path, w.Code)
				}
			}
		}
	}
	if env.wled.total() != 0 {
		t.Fatal("setup navigation/invalid requests contacted a controller")
	}
}
