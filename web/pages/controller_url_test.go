package pages

import (
	"database/sql"
	"net/url"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestControllerManagementURL(t *testing.T) {
	tests := []struct {
		name    string
		address string
		port    sql.NullInt64
		want    string
	}{
		{"custom port", "192.0.2.10", sql.NullInt64{Int64: 8081, Valid: true}, "http://192.0.2.10:8081"},
		{"default port", "wled.home", sql.NullInt64{}, "http://wled.home:80"},
		{"IPv6", "2001:db8::10", sql.NullInt64{Int64: 8123, Valid: true}, "http://[2001:db8::10]:8123"},
		{"bracketed IPv6", "[2001:db8::10]", sql.NullInt64{Int64: 8123, Valid: true}, "http://[2001:db8::10]:8123"},
		{"legacy embedded port", "wled.home:8080", sql.NullInt64{Int64: 80, Valid: true}, "http://wled.home:8080"},
		{"invalid port fallback", "wled.home", sql.NullInt64{Int64: 70000, Valid: true}, "http://wled.home:80"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := controllerManagementURL(db.Controller{IpAddress: test.address, Port: test.port})
			if got != test.want {
				t.Fatalf("controllerManagementURL() = %q, want %q", got, test.want)
			}
			parsed, err := url.Parse(got)
			if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
				t.Fatalf("controller URL %q is invalid: %v", got, err)
			}
		})
	}
}
