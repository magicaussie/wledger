package main

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// authMiddleware enforces inbound bearer authentication and Origin validation
// for the network MCP transports (SSE and Streamable HTTP).
//
// The token is the same WLEDGER_API_TOKEN used by the /api/v1 API; there is no
// separate MCP credential. The middleware fails closed: when token is empty no
// request is ever authorised.
//
// Authorization parsing is strict: exactly one Authorization header is
// required, carrying exactly "Bearer <token>" or "Token <token>". The scheme
// is matched case-insensitively (RFC 7235 auth-scheme semantics), so "bearer",
// "BEARER" and "Token" are all accepted, but only those two schemes are. Raw
// tokens, nested prefixes, empty credentials, extra whitespace and duplicate
// headers are all rejected. The credential is compared in constant time.
//
// Origin handling: when the request carries an Origin header (i.e. it comes
// from a browser), it must be a well-formed absolute http(s) origin matching
// either an explicitly configured allowed origin or a trusted loopback origin.
// Requests without an Origin header (non-browser MCP clients) proceed to
// bearer authentication. Disallowed origins are rejected with 403 before any
// credential is examined.
//
// This is deliberately not a CORS mechanism: CORS only governs whether a
// browser may read a response, not whether a request is authorised.
func authMiddleware(token string, allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r.Header.Values("Origin"), allowedOrigins) {
			http.Error(w, "forbidden: origin not allowed", http.StatusForbidden)
			return
		}

		// Fail closed when no token is configured. Never treat an empty
		// configured token as a valid credential.
		if token == "" {
			writeUnauthorized(w)
			return
		}

		// Exactly one Authorization header is required; duplicates are
		// rejected rather than picking one.
		auths := r.Header.Values("Authorization")
		if len(auths) != 1 {
			writeUnauthorized(w)
			return
		}
		cred, ok := parseBearer(auths[0])
		if !ok {
			writeUnauthorized(w)
			return
		}
		if subtle.ConstantTimeCompare([]byte(cred), []byte(token)) != 1 {
			writeUnauthorized(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// writeUnauthorized emits a 401 without echoing any credential material.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// parseBearer strictly parses an Authorization header value of the form
// "Bearer <token>" or "Token <token>". The scheme is matched
// case-insensitively per RFC 7235 (auth-scheme is case-insensitive), so
// "bearer"/"BEARER"/"Token" are accepted, but only those two schemes are. It
// rejects raw tokens without a scheme, nested prefixes ("Bearer Token x"),
// empty credentials, extra whitespace and any other scheme.
func parseBearer(h string) (string, bool) {
	i := strings.IndexByte(h, ' ')
	if i <= 0 {
		return "", false
	}
	scheme := h[:i]
	if !strings.EqualFold(scheme, "Bearer") && !strings.EqualFold(scheme, "Token") {
		return "", false
	}
	cred := h[i+1:]
	if cred == "" {
		return "", false
	}
	// A credential must be a single token: no further spaces or tabs.
	if strings.ContainsAny(cred, " \t") {
		return "", false
	}
	return cred, true
}

// parseAllowedOrigins splits a comma-separated MCP_ALLOWED_ORIGINS value into
// a trimmed, non-empty list.
func parseAllowedOrigins(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// originAllowed reports whether a request carrying the given Origin header
// values may proceed.
//
//   - No Origin header at all (non-browser client) is allowed.
//   - More than one Origin header is rejected.
//   - A present Origin must be a well-formed absolute http(s) origin with no
//     userinfo, path, query or fragment, and a valid hostname and port.
//   - The origin must be a trusted loopback origin or an explicitly configured
//     allowed origin.
func originAllowed(origins []string, allowed []string) bool {
	if len(origins) == 0 {
		return true
	}
	if len(origins) > 1 {
		return false
	}
	origin := origins[0]
	u, ok := parseOrigin(origin)
	if !ok {
		return false
	}
	if isLoopbackHostname(u.Hostname()) {
		return true
	}
	for _, a := range allowed {
		if normalizeOrigin(origin) == normalizeOrigin(a) {
			return true
		}
	}
	return false
}

// parseOrigin strictly parses an Origin header value. It requires an absolute
// http(s) origin with a valid hostname and optional valid port, and rejects
// userinfo, paths, queries, fragments and opaque forms.
func parseOrigin(origin string) (*url.URL, bool) {
	if origin == "" {
		return nil, false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return nil, false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, false
	}
	if u.Opaque != "" || u.User != nil {
		return nil, false
	}
	if u.Host == "" {
		return nil, false
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, false
	}
	if !validHostname(u.Hostname()) {
		return nil, false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, false
		}
	}
	return u, true
}

// validHostname reports whether host is a syntactically valid IPv4/IPv6
// address or DNS hostname.
func validHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			case c == '-' && i != 0 && i != len(label)-1:
			default:
				return false
			}
		}
	}
	return true
}

// isLoopbackHostname reports whether host is a loopback hostname or address.
func isLoopbackHostname(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// normalizeOrigin lower-cases and trims a trailing slash so configured and
// presented origins compare consistently.
func normalizeOrigin(o string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(o), "/"))
}
