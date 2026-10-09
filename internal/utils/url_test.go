package utils

import "testing"

func TestValidateHTTPURL_Valid(t *testing.T) {
	valid := []string{
		"https://example.com",
		"http://example.com",
		"https://example.com/",
		"https://example.com/path/to/page",
		"https://example.com/search?q=resistor&page=2",
		"https://example.com/a%20b/%2F?q=x%20y",
		"HTTPS://Example.COM/Path",
		"Http://example.com",
		"hTtPs://example.com",
		"https://example.com:8443/path",
		"https://user:pass@example.com/path",
		"https://[2001:db8::1]:8080/",
		"https://example.com/#fragment",
	}

	for _, u := range valid {
		if err := ValidateHTTPURL(u); err != nil {
			t.Errorf("ValidateHTTPURL(%q) = %v, want nil", u, err)
		}
	}
}

func TestValidateHTTPURL_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"JaVaScRiPt:alert(1)",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"//example.com",
		"//example.com/path",
		"http://",
		"https://",
		"https:///path",
		"not-a-url",
		"example.com",
		"/relative/path",
		"../relative",
		"http:example.com",
		"mailto:user@example.com",
		"ftp://example.com",
		"tel:+123456",
		"https://exa mple.com",
		"https://example.com/\t",
		"java\tscript:alert(1)",
		" javascript:alert(1)",
		"https://example.com ",
		"https://example.com/\n",
		"https://example.com/\x00",
		"https://example.com\\@evil.com",
		"https:\\\\evil.com",
	}

	for _, u := range invalid {
		if err := ValidateHTTPURL(u); err == nil {
			t.Errorf("ValidateHTTPURL(%q) = nil, want error", u)
		}
	}
}

func TestSafeHTTPURL(t *testing.T) {
	if got, ok := SafeHTTPURL("https://example.com/x"); !ok || got != "https://example.com/x" {
		t.Errorf("SafeHTTPURL(valid) = (%q, %v), want (%q, true)", got, ok, "https://example.com/x")
	}
	if got, ok := SafeHTTPURL("javascript:alert(1)"); ok || got != "" {
		t.Errorf("SafeHTTPURL(unsafe) = (%q, %v), want (\"\", false)", got, ok)
	}
}
