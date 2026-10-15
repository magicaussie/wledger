package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestSetupCreatesExactlyOneAdministratorUnderConcurrency verifies that
// concurrent first-run setup requests cannot both create an administrator. The
// guarded insert makes the "first user wins" decision atomically.
func TestSetupCreatesExactlyOneAdministratorUnderConcurrency(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()

	type attempt struct {
		cookies []*http.Cookie
		token   string
	}
	prepare := func() attempt {
		get := httptest.NewRecorder()
		env.router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/setup", nil))
		cookies := get.Result().Cookies()
		if get.Code != http.StatusOK || len(cookies) == 0 {
			t.Fatalf("setup GET = %d with %d cookies", get.Code, len(cookies))
		}
		match := regexp.MustCompile(`name="csrf_token" value="([^"]+)"`).FindStringSubmatch(get.Body.String())
		if len(match) != 2 {
			t.Fatalf("setup page is missing a csrf token")
		}
		return attempt{cookies: cookies, token: match[1]}
	}
	first := prepare()
	second := prepare()

	start := make(chan struct{})
	codes := make(chan int, 2)
	post := func(at attempt, email string) {
		<-start
		form := url.Values{"email": {email}, "password": {"secret123"}, "csrf_token": {at.token}}
		req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, c := range at.cookies {
			req.AddCookie(c)
		}
		rr := httptest.NewRecorder()
		env.router.ServeHTTP(rr, req)
		codes <- rr.Code
	}
	go post(first, "first@test.com")
	go post(second, "second@test.com")
	close(start)

	seen := map[int]int{}
	for i := 0; i < 2; i++ {
		seen[<-codes]++
	}

	count, err := env.store.CountUsers(context.Background())
	if err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 1 {
		t.Fatalf("users after concurrent setup = %d, want exactly 1 (codes=%v)", count, seen)
	}
	if seen[http.StatusSeeOther] != 1 {
		t.Fatalf("expected exactly one successful setup (303), got %v", seen)
	}
	if seen[http.StatusForbidden] != 1 {
		t.Fatalf("expected exactly one rejected setup (403), got %v", seen)
	}
}
