package config

import "testing"

// TestCookieSecure verifies the session cookie Secure flag defaults to true and
// is disabled only for explicit, deliberate local-HTTP opt-in values.
func TestCookieSecure(t *testing.T) {
	// Default (unset) must be Secure.
	t.Setenv(InsecureCookiesEnv, "")
	if !CookieSecure() {
		t.Error("default CookieSecure() = false, want true")
	}

	// Truthy opt-out values disable Secure.
	for _, v := range []string{"1", "true", "TRUE", "True", "yes", "on", "On", " 1 "} {
		t.Setenv(InsecureCookiesEnv, v)
		if CookieSecure() {
			t.Errorf("CookieSecure() = true for %q, want false", v)
		}
	}

	// Anything else keeps Secure.
	for _, v := range []string{"0", "false", "no", "off", "random", "2"} {
		t.Setenv(InsecureCookiesEnv, v)
		if !CookieSecure() {
			t.Errorf("CookieSecure() = false for %q, want true", v)
		}
	}
}
