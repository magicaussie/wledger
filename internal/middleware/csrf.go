package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"

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

// CSRFContext ensures an authenticated session has a CSRF token and exposes it
// to templates via the request context (see internal/csrf). Guests are left
// untouched, so no session is created for anonymous visitors.
func (m *Manager) CSRFContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.Session != nil && m.Session.GetInt64(r.Context(), config.SessionKeyUserID) != 0 {
			if tok, err := CSRFToken(r.Context(), m.Session); err == nil {
				r = r.WithContext(csrf.WithToken(r.Context(), tok))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireCSRF rejects a request whose CSRF token does not match the session's
// token. The token is read from the X-CSRF-Token header (used by HTMX requests)
// or, as a fallback, the csrf_token form field. Absent or invalid tokens are
// rejected with 403. This is the explicit CSRF defense for state-changing
// browser requests; it does not rely on SameSite alone.
func (m *Manager) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		submitted := r.Header.Get("X-CSRF-Token")
		if submitted == "" {
			_ = r.ParseForm()
			submitted = r.FormValue("csrf_token")
		}
		if !ValidateCSRF(r.Context(), m.Session, submitted) {
			m.UIError.Respond(w, r, nil, "Invalid or missing CSRF token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
