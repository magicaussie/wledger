package config

import (
	"os"
	"strings"
)

// InsecureCookiesEnv optionally disables the Secure flag on the session cookie.
//
// It exists only for deliberate local HTTP development. Leave it unset in
// production: session cookies then carry Secure, which is correct even when the
// app runs behind a TLS-terminating reverse proxy (the browser-facing
// connection is HTTPS, so the browser will store and return the cookie).
const InsecureCookiesEnv = "WLEDGER_INSECURE_COOKIES"

// CookieSecure reports whether the session cookie should be marked Secure.
//
// The default is true. It is disabled only when WLEDGER_INSECURE_COOKIES is set
// to a truthy value ("1", "true", "yes", "on", case-insensitive), which is
// intended solely for local HTTP development.
func CookieSecure() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(InsecureCookiesEnv))) {
	case "1", "true", "yes", "on":
		return false
	default:
		return true
	}
}
