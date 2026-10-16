package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCentreEveryEndpointRolesAndCSRF(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	endpoints := []struct{ method, path string }{
		{"GET", ""}, {"GET", "/data"}, {"POST", "/inspect"}, {"POST", "/start"}, {"POST", "/stop"}, {"POST", "/emergency-off"}, {"POST", "/mapping-preview"}, {"POST", "/mapping-save"}, {"POST", "/bin-pixel"},
	}
	call := func(method, path string, cookies []*http.Cookie, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/hardware/led-test-centre"+path, strings.NewReader(`{}`))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set("X-CSRF-Token", token)
		}
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		env.router.ServeHTTP(w, r)
		return w
	}
	for _, role := range []string{"viewer", "editor", "admin"} {
		email := "centre-" + role + "@test.com"
		env.createUser(t, email, role)
		cookies := env.login(t, email)
		token := env.csrfToken(t, cookies)
		for _, endpoint := range endpoints {
			t.Run(role+"/"+endpoint.method+endpoint.path, func(t *testing.T) {
				w := call(endpoint.method, endpoint.path, cookies, token)
				if role == "viewer" {
					if w.Code != 403 {
						t.Fatalf("viewer=%d %s", w.Code, w.Body.String())
					}
					return
				}
				if endpoint.method == "GET" {
					if w.Code != 200 {
						t.Fatalf("authorized GET=%d %s", w.Code, w.Body.String())
					}
					return
				}
				// The fixture intentionally has no Locator. Authorization must pass through
				// to validation/unavailable-service responses, never a hardware operation.
				if w.Code == 403 || w.Code == 401 || w.Code == 404 || w.Code == 405 || w.Code >= 500 && w.Code != 503 {
					t.Fatalf("authorized endpoint=%d %s", w.Code, w.Body.String())
				}
				for _, invalid := range []string{"", "invalid-token"} {
					denied := call(endpoint.method, endpoint.path, cookies, invalid)
					if denied.Code != 403 {
						t.Fatalf("CSRF %q=%d %s", invalid, denied.Code, denied.Body.String())
					}
				}
			})
		}
	}
	for _, endpoint := range endpoints {
		t.Run("anonymous/"+endpoint.method+endpoint.path, func(t *testing.T) {
			w := call(endpoint.method, endpoint.path, nil, "")
			if w.Code != 403 && w.Code != 401 && w.Code != 302 && w.Code != 303 {
				t.Fatalf("anonymous=%d %s", w.Code, w.Body.String())
			}
		})
	}
	if env.wled.total() != 0 {
		t.Fatalf("authorization tests issued %d WLED calls", env.wled.total())
	}
}
