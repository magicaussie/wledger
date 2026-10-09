package utils

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// ValidateHTTPURL reports whether raw is a well-formed absolute HTTP or HTTPS
// URL that is safe to persist and render as a hyperlink.
//
// Only the http and https schemes are accepted. Everything else is rejected,
// including javascript:, data:, vbscript:, file:, protocol-relative URLs such
// as "//example.com" and relative URLs. URLs containing whitespace or control
// characters are also rejected, because browsers strip or ignore those
// characters, which enables scheme obfuscation such as "java\tscript:".
//
// Validation is performed with net/url rather than substring or regular
// expression matching, so mixed-case schemes ("JaVaScRiPt:") are handled
// correctly.
func ValidateHTTPURL(raw string) error {
	if raw == "" {
		return errors.New("URL is empty")
	}

	for _, r := range raw {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return errors.New("URL must not contain whitespace or control characters")
		}
	}

	// Browsers normalise backslashes to forward slashes, which can be used to
	// disguise the authority component. Reject them outright.
	if strings.ContainsRune(raw, '\\') {
		return errors.New("URL must not contain backslashes")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("URL is malformed: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		return errors.New("URL must be an absolute http:// or https:// URL")
	}
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("URL scheme %q is not allowed (only http and https)", u.Scheme)
	}

	if u.Host == "" || u.Hostname() == "" {
		return errors.New("URL must include a hostname")
	}

	return nil
}

// SafeHTTPURL returns raw and true when it is a valid HTTP(S) URL, and
// ("", false) otherwise. It is intended for rendering stored values that may
// predate input validation, so that unsafe legacy links are never emitted as
// executable hrefs.
func SafeHTTPURL(raw string) (string, bool) {
	if ValidateHTTPURL(raw) != nil {
		return "", false
	}
	return raw, true
}
