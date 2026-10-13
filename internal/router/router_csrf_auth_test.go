package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidCSRFDoesNotBypassRoleAuthorization(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "csrf-viewer@test.com", "viewer")
	cookies := env.login(t, "csrf-viewer@test.com")
	token := env.csrfToken(t, cookies)
	before := mutationDBSnapshot(t, env.conn)

	for _, route := range []mutationRoute{
		{method: http.MethodPost, path: "/parts"},
		{method: http.MethodPost, path: "/storage/cabinets"},
		{method: http.MethodPost, path: "/settings"},
	} {
		response := sendMutation(env, route, cookies, token, "", "")
		if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "Invalid or missing CSRF token") {
			t.Errorf("viewer %s %s = %d, want role-denied 403 after CSRF validation", route.method, route.path, response.Code)
		}
	}
	if after := mutationDBSnapshot(t, env.conn); after != before {
		t.Fatal("valid-CSRF viewer requests mutated protected resources")
	}
	if env.wled.total() != 0 {
		t.Fatalf("viewer triggered %d WLED operation(s)", env.wled.total())
	}

	unauthenticated := httptest.NewRecorder()
	env.router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/parts", nil))
	if unauthenticated.Code != http.StatusSeeOther || unauthenticated.Header().Get("Location") != "/login" {
		t.Fatalf("unauthenticated part mutation = %d location=%q, want login redirect", unauthenticated.Code, unauthenticated.Header().Get("Location"))
	}
}

func TestOAuthCallbackRejectsStateNotBoundToSession(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "oauth-admin@test.com", "admin")
	cookies := env.login(t, "oauth-admin@test.com")

	request := httptest.NewRequest(http.MethodGet, "/suppliers/oauth/callback?code=synthetic-code&state=digikey%3Aattacker-state", nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "OAuth state validation failed") {
		t.Fatalf("unbound OAuth callback = %d body=%q, want state rejection", response.Code, response.Body.String())
	}
}
