package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// okHandler records that it was reached and returns 200.
func okHandler(reached *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthMiddlewareMissingToken(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", nil, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token: want 401, got %d", rec.Code)
	}
	if reached {
		t.Fatal("handler reached without a token")
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("want WWW-Authenticate Bearer, got %q", got)
	}
}

func TestAuthMiddlewareInvalidToken(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", nil, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid token: want 401, got %d", rec.Code)
	}
	if reached {
		t.Fatal("handler reached with an invalid token")
	}
}

func TestAuthMiddlewareValidToken(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", nil, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: want 200, got %d", rec.Code)
	}
	if !reached {
		t.Fatal("handler not reached with a valid token")
	}
}

func TestAuthMiddlewareTokenPrefix(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", nil, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Token sekret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("Token prefix: want 200 and handler reached, got %d reached=%v", rec.Code, reached)
	}
}

// TestAuthMiddlewareSchemeCaseInsensitive ensures the auth scheme is matched
// case-insensitively (RFC 7235), so any casing of the two accepted schemes is
// valid while other schemes remain rejected.
func TestAuthMiddlewareSchemeCaseInsensitive(t *testing.T) {
	accepted := []string{"Bearer sekret", "bearer sekret", "BEARER sekret", "BeArEr sekret", "Token sekret", "token sekret", "TOKEN sekret", "ToKeN sekret"}
	for _, auth := range accepted {
		t.Run("accept "+auth, func(t *testing.T) {
			reached := false
			h := authMiddleware("sekret", nil, okHandler(&reached))

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", auth)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK || !reached {
				t.Errorf("auth=%q: want 200 and handler reached, got %d reached=%v", auth, rec.Code, reached)
			}
		})
	}

	rejected := []string{"Basic sekret", "Digest sekret", "ApiKey sekret", "Bearerx sekret", "Tokenx sekret"}
	for _, auth := range rejected {
		t.Run("reject "+auth, func(t *testing.T) {
			reached := false
			h := authMiddleware("sekret", nil, okHandler(&reached))

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", auth)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized || reached {
				t.Errorf("auth=%q: want 401 and handler not reached, got %d reached=%v", auth, rec.Code, reached)
			}
		})
	}
}

// TestAuthMiddlewareStrictAuthorization covers malformed Authorization values
// that must all be rejected with 401.
func TestAuthMiddlewareStrictAuthorization(t *testing.T) {
	cases := []struct {
		name string
		auth string
	}{
		{"raw secret without scheme", "sekret"},
		{"nested Bearer Token prefix", "Bearer Token sekret"},
		{"nested Token Bearer prefix", "Token Bearer sekret"},
		{"scheme only", "Bearer"},
		{"empty credential", "Bearer "},
		{"double space", "Bearer  sekret"},
		{"leading space", " Bearer sekret"},
		{"tab separator", "Bearer\tsekret"},
		{"wrong scheme", "Basic sekret"},
		{"trailing space", "Bearer sekret "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reached := false
			h := authMiddleware("sekret", nil, okHandler(&reached))

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", c.auth)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("auth=%q: want 401, got %d", c.auth, rec.Code)
			}
			if reached {
				t.Errorf("auth=%q: handler reached", c.auth)
			}
		})
	}
}

// TestAuthMiddlewareDuplicateAuthorization ensures duplicate Authorization
// headers are rejected rather than one being picked.
func TestAuthMiddlewareDuplicateAuthorization(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", nil, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Add("Authorization", "Bearer wrong")
	req.Header.Add("Authorization", "Bearer sekret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate Authorization: want 401, got %d", rec.Code)
	}
	if reached {
		t.Fatal("handler reached with duplicate Authorization headers")
	}
}

// TestAuthMiddlewareEmptyConfiguredTokenFailsClosed ensures an unset/empty
// configured token never authorises a request, even one presenting an empty
// credential (which would otherwise compare equal).
func TestAuthMiddlewareEmptyConfiguredTokenFailsClosed(t *testing.T) {
	cases := []string{"", "Bearer ", "Bearer anything"}
	for _, auth := range cases {
		reached := false
		h := authMiddleware("", nil, okHandler(&reached))

		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("empty configured token, auth=%q: want 401, got %d", auth, rec.Code)
		}
		if reached {
			t.Errorf("empty configured token, auth=%q: handler reached", auth)
		}
	}
}

func TestAuthMiddlewareOriginRejected(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", []string{"https://allowed.example"}, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin: want 403, got %d", rec.Code)
	}
	if reached {
		t.Fatal("handler reached with a disallowed origin")
	}
}

func TestAuthMiddlewareOriginAllowedConfigured(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", []string{"https://allowed.example"}, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	req.Header.Set("Origin", "https://allowed.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("allowed origin: want 200 and reached, got %d reached=%v", rec.Code, reached)
	}
}

func TestAuthMiddlewareOriginLoopbackTrusted(t *testing.T) {
	for _, origin := range []string{"http://localhost:3000", "http://127.0.0.1:5173", "http://[::1]:8080"} {
		reached := false
		h := authMiddleware("sekret", nil, okHandler(&reached))

		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer sekret")
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK || !reached {
			t.Errorf("loopback origin %q: want 200 and reached, got %d reached=%v", origin, rec.Code, reached)
		}
	}
}

// TestAuthMiddlewareNoOriginAllowed ensures non-browser clients (no Origin
// header) are not blocked by the Origin policy.
func TestAuthMiddlewareNoOriginAllowed(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", []string{"https://allowed.example"}, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("no origin: want 200 and reached, got %d reached=%v", rec.Code, reached)
	}
}

// TestAuthMiddlewareDuplicateOrigin ensures duplicate Origin headers are
// rejected with 403.
func TestAuthMiddlewareDuplicateOrigin(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", nil, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	req.Header.Add("Origin", "http://localhost")
	req.Header.Add("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("duplicate origin: want 403, got %d", rec.Code)
	}
	if reached {
		t.Fatal("handler reached with duplicate Origin headers")
	}
}

// TestAuthMiddlewareEmptyOrigin ensures an explicitly present but empty Origin
// header is rejected with 403.
func TestAuthMiddlewareEmptyOrigin(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", nil, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer sekret")
	req.Header.Set("Origin", "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("empty origin: want 403, got %d", rec.Code)
	}
	if reached {
		t.Fatal("handler reached with an empty Origin header")
	}
}

// TestAuthMiddlewareOriginRejectedBeforeAuth ensures a disallowed origin is
// rejected with 403 even when no credential is supplied (origin is checked
// first and does not leak whether a token would have been valid).
func TestAuthMiddlewareOriginRejectedBeforeAuth(t *testing.T) {
	reached := false
	h := authMiddleware("sekret", nil, okHandler(&reached))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("disallowed origin without token: want 403, got %d", rec.Code)
	}
}

// TestAuthMiddlewareTokenNotDisclosed ensures the configured token is not
// echoed in the response body or headers on failure.
func TestAuthMiddlewareTokenNotDisclosed(t *testing.T) {
	const token = "super-secret-token-value"
	h := authMiddleware(token, nil, okHandler(new(bool)))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), token) {
		t.Fatalf("response body disclosed the token: %q", rec.Body.String())
	}
	for k, vs := range rec.Header() {
		for _, v := range vs {
			if strings.Contains(v, token) {
				t.Fatalf("response header %s disclosed the token: %q", k, v)
			}
		}
	}
}

func TestParseBearer(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"Bearer sekret", "sekret", true},
		{"Token sekret", "sekret", true},
		{"bearer sekret", "sekret", true},
		{"BEARER sekret", "sekret", true},
		{"BeArEr sekret", "sekret", true},
		{"token sekret", "sekret", true},
		{"TOKEN sekret", "sekret", true},
		{"Bearer ", "", false},
		{"Token ", "", false},
		{"Bearer", "", false},
		{"sekret", "", false},
		{"Bearer Token sekret", "", false},
		{"Bearer  sekret", "", false},
		{" Bearer sekret", "", false},
		{"Bearer\tsekret", "", false},
		{"Basic sekret", "", false},
		{"Bearerx sekret", "", false},
		{"Bearer sekret ", "", false},
	}
	for _, c := range cases {
		got, ok := parseBearer(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("parseBearer(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestOriginAllowed(t *testing.T) {
	allowed := []string{"https://allowed.example", "https://other.example/"}
	cases := []struct {
		name   string
		origin []string
		want   bool
	}{
		{"no origin header", nil, true},
		{"configured origin", []string{"https://allowed.example"}, true},
		{"configured origin trailing slash normalised", []string{"https://other.example"}, true},
		{"loopback localhost", []string{"http://localhost:3000"}, true},
		{"loopback ipv4", []string{"http://127.0.0.1"}, true},
		{"loopback ipv6", []string{"http://[::1]:9100"}, true},
		{"disallowed origin", []string{"https://evil.example"}, false},
		{"null origin", []string{"null"}, false},
		{"not a url", []string{"not a url"}, false},
		{"non-http scheme", []string{"ftp://allowed.example"}, false},
		{"empty origin value", []string{""}, false},
		{"duplicate origin", []string{"http://localhost", "https://evil.example"}, false},
		{"userinfo deceptive", []string{"http://evil.com@localhost"}, false},
		{"path", []string{"http://localhost/path"}, false},
		{"trailing slash path", []string{"http://localhost/"}, false},
		{"query", []string{"http://localhost?x=1"}, false},
		{"fragment", []string{"http://localhost#fragment"}, false},
		{"deceptive hostname", []string{"http://localhost.evil.com"}, false},
		{"malformed port", []string{"http://localhost:abc"}, false},
		{"out of range port", []string{"http://localhost:99999"}, false},
		{"zero port", []string{"http://localhost:0"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := originAllowed(c.origin, allowed); got != c.want {
				t.Errorf("originAllowed(%v) = %v, want %v", c.origin, got, c.want)
			}
		})
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	got := parseAllowedOrigins(" https://a.example , https://b.example ,, ")
	want := []string{"https://a.example", "https://b.example"}
	if len(got) != len(want) {
		t.Fatalf("parseAllowedOrigins = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseAllowedOrigins = %v, want %v", got, want)
		}
	}
	if len(parseAllowedOrigins("")) != 0 {
		t.Fatal("empty input should yield no origins")
	}
}
