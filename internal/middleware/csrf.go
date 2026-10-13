package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/csrf"
)

// csrfSessionKey is the session key under which a per-session CSRF token is
// stored. It is deliberately separate from the authentication session data.
const csrfSessionKey = "csrf_token"

// ErrCSRFUnavailable is returned when a CSRF token is requested without a session
// manager.
var ErrCSRFUnavailable = errors.New("csrf unavailable: no session manager")

// CSRFToken returns the current session's CSRF token, generating and persisting
// a random one on first use. The token is bound to the session (and therefore to
// the SameSite-scoped session cookie), so a cross-site request cannot include a
// valid value.
func CSRFToken(ctx context.Context, sm *scs.SessionManager) (string, error) {
	if sm == nil {
		return "", ErrCSRFUnavailable
	}
	if tok := sm.GetString(ctx, csrfSessionKey); tok != "" {
		return tok, nil
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(buf)
	sm.Put(ctx, csrfSessionKey, tok)
	return tok, nil
}

// ValidateCSRF reports whether submitted matches the session's CSRF token, in
// constant time. A missing token on either side is rejected.
func ValidateCSRF(ctx context.Context, sm *scs.SessionManager, submitted string) bool {
	if sm == nil {
		return false
	}
	expected := sm.GetString(ctx, csrfSessionKey)
	if expected == "" || submitted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(submitted)) == 1
}

// RotateCSRF discards the session's current CSRF token so the next request that
// needs one generates a fresh value. It is called on authentication boundaries
// (login, and the setup auto-login) so a token obtained before authenticating can
// never be reused after the session's privilege level changes. Logout clears the
// whole session and therefore needs no separate rotation.
func RotateCSRF(ctx context.Context, sm *scs.SessionManager) {
	if sm == nil {
		return
	}
	sm.Remove(ctx, csrfSessionKey)
}

// CSRFContext ensures authenticated pages and the public login/setup forms have
// a session-bound token, then exposes it to templates via internal/csrf.
func (m *Manager) CSRFContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated := m.Session != nil && m.Session.GetInt64(r.Context(), config.SessionKeyUserID) != 0
		publicAuthRoute := r.URL.Path == "/login" || r.URL.Path == "/setup"
		if m.Session != nil && (authenticated || publicAuthRoute) {
			tok := m.Session.GetString(r.Context(), csrfSessionKey)
			if tok == "" && r.Method == http.MethodGet && (authenticated || publicAuthRoute) {
				tok, _ = CSRFToken(r.Context(), m.Session)
			}
			if tok != "" {
				r = r.WithContext(csrf.WithToken(r.Context(), tok))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireCSRF applies one policy to browser-session mutations across the whole
// router. Public login/setup forms and logout are protected as well. Unauthenticated
// requests to other protected paths continue to the authentication middleware so
// existing redirects and role checks remain authoritative. The bearer API is
// excluded only by path because it validates its own required bearer credential.
func (m *Manager) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isStateChangingMethod(r.Method) || isMachineAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		publicAuthRoute := r.URL.Path == "/login" || r.URL.Path == "/setup" || r.URL.Path == "/logout"
		authenticated := m.Session != nil && m.Session.GetInt64(r.Context(), config.SessionKeyUserID) != 0
		if !authenticated && !publicAuthRoute {
			next.ServeHTTP(w, r)
			return
		}

		submitted := r.Header.Get("X-CSRF-Token")
		if submitted == "" {
			if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
				parseErr := r.ParseMultipartForm(1 << 20)
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
				if parseErr != nil {
					m.UIError.Respond(w, r, nil, "Invalid or missing CSRF token", http.StatusForbidden)
					return
				}
			} else {
				_ = r.ParseForm()
			}
			submitted = r.FormValue("csrf_token")
		}
		if !ValidateCSRF(r.Context(), m.Session, submitted) {
			m.UIError.Respond(w, r, nil, "Invalid or missing CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func isMachineAPIPath(path string) bool {
	return path == "/api/v1" || strings.HasPrefix(path, "/api/v1/")
}
