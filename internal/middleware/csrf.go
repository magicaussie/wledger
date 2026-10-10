package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"

	"github.com/alexedwards/scs/v2"
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
