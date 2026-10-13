package router

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type mutationRoute struct {
	method string
	path   string
}

var routeParameter = regexp.MustCompile(`\{[^/]+\}`)

func collectMutationRoutes(t *testing.T, router chi.Router) []mutationRoute {
	t.Helper()
	seen := make(map[mutationRoute]struct{})
	var routes []mutationRoute
	add := func(method, pattern string) {
		if !isBrowserMutationMethod(method) || strings.HasPrefix(pattern, "/api/v1/") {
			return
		}
		path := routeParameter.ReplaceAllString(pattern, "1")
		route := mutationRoute{method: method, path: path}
		if _, ok := seen[route]; ok {
			return
		}
		seen[route] = struct{}{}
		routes = append(routes, route)
	}
	if err := chi.Walk(router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == "*" {
			if pattern == "/parts/bulk" {
				add(http.MethodPost, pattern)
				add(http.MethodDelete, pattern)
			}
			return nil
		}
		add(method, pattern)
		return nil
	}); err != nil {
		t.Fatalf("walk browser routes: %v", err)
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].path == routes[j].path {
			return routes[i].method < routes[j].method
		}
		return routes[i].path < routes[j].path
	})
	if len(routes) < 40 {
		t.Fatalf("route walk found only %d browser mutation routes; update the audit test if route registration changed", len(routes))
	}
	return routes
}

func isBrowserMutationMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func sendMutation(env *authTestEnv, route mutationRoute, cookies []*http.Cookie, headerToken, body string, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(route.method, route.path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if headerToken != "" {
		req.Header.Set("X-CSRF-Token", headerToken)
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, req)
	return response
}

func csrfTokenFromHTML(t *testing.T, body string) string {
	t.Helper()
	const marker = `name="csrf_token" value="`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatal("rendered form does not contain csrf_token")
	}
	valueStart := start + len(marker)
	end := strings.IndexByte(body[valueStart:], '"')
	if end < 0 || end == 0 {
		t.Fatal("rendered CSRF token is empty or malformed")
	}
	return body[valueStart : valueStart+end]
}

func mutationDBSnapshot(t *testing.T, conn *sql.DB) string {
	t.Helper()
	tables, err := conn.Query("SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			tables.Close()
			t.Fatalf("scan table name: %v", err)
		}
		if name != "sessions" {
			names = append(names, name)
		}
	}
	if err := tables.Close(); err != nil {
		t.Fatalf("close tables: %v", err)
	}

	type tableState struct {
		Name    string
		Columns []string
		Rows    [][]any
	}
	state := make([]tableState, 0, len(names))
	for _, name := range names {
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		rows, err := conn.Query("SELECT * FROM " + quoted)
		if err != nil {
			t.Fatalf("read table %s: %v", name, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatalf("read columns %s: %v", name, err)
		}
		entry := tableState{Name: name, Columns: columns}
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatalf("scan table %s: %v", name, err)
			}
			entry.Rows = append(entry.Rows, values)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close table %s: %v", name, err)
		}
		state = append(state, entry)
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("encode database snapshot: %v", err)
	}
	return string(encoded)
}

func directorySnapshot(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	result := make(map[string][32]byte)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return result
	} else if err != nil {
		t.Fatalf("stat %s: %v", root, err)
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[relative] = sha256.Sum256(content)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return result
}

func TestEveryBrowserMutationRouteRejectsMissingAndInvalidCSRFBeforeSideEffects(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "csrf-admin@test.com", "admin")
	cookies := env.login(t, "csrf-admin@test.com")
	routes := collectMutationRoutes(t, env.router)

	beforeDB := mutationDBSnapshot(t, env.conn)
	beforeFiles := directorySnapshot(t, filepath.Join(workingDir, "app", "uploads"))
	for _, route := range routes {
		t.Run(fmt.Sprintf("missing/%s %s", route.method, route.path), func(t *testing.T) {
			response := sendMutation(env, route, cookies, "", "", "")
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", response.Code, response.Body.String())
			}
		})
		t.Run(fmt.Sprintf("invalid/%s %s", route.method, route.path), func(t *testing.T) {
			response := sendMutation(env, route, cookies, "invalid-csrf-token", "", "")
			if response.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body=%s", response.Code, response.Body.String())
			}
		})
	}
	if after := mutationDBSnapshot(t, env.conn); after != beforeDB {
		t.Fatal("a missing/invalid CSRF request mutated persistent database state")
	}
	if after := directorySnapshot(t, filepath.Join(workingDir, "app", "uploads")); !reflect.DeepEqual(after, beforeFiles) {
		t.Fatal("a missing/invalid CSRF request modified uploaded files")
	}
	if got := env.wled.total(); got != 0 {
		t.Fatalf("missing/invalid CSRF triggered %d mock WLED operation(s)", got)
	}
}

func TestValidCSRFReachesEveryBrowserMutationHandler(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "csrf-valid-admin@test.com", "admin")
	cookies := env.login(t, "csrf-valid-admin@test.com")
	token := env.csrfToken(t, cookies)
	routes := collectMutationRoutes(t, env.router)
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].path == "/logout" {
			return false
		}
		if routes[j].path == "/logout" {
			return true
		}
		return false
	})

	for _, route := range routes {
		if route.path == "/logout" {
			continue
		}
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			headerToken, body, contentType := token, "", ""
			if route.path == "/hardware/conversion" {
				headerToken = ""
				body = url.Values{"csrf_token": {token}}.Encode()
				contentType = "application/x-www-form-urlencoded"
			}
			response := sendMutation(env, route, cookies, headerToken, body, contentType)
			if strings.Contains(response.Body.String(), "Invalid or missing CSRF token") {
				t.Fatalf("valid token was rejected: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}

	logout := mutationRoute{method: http.MethodPost, path: "/logout"}
	response := sendMutation(env, logout, cookies, token, "", "")
	if response.Code != http.StatusSeeOther {
		t.Fatalf("valid-token logout status = %d, want 303: %s", response.Code, response.Body.String())
	}
}

func TestPublicLoginAndSetupFormsRequireSessionCSRF(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()

	setupGet := httptest.NewRecorder()
	env.router.ServeHTTP(setupGet, httptest.NewRequest(http.MethodGet, "/setup", nil))
	setupCookies := setupGet.Result().Cookies()
	if setupGet.Code != http.StatusOK || len(setupCookies) == 0 || !strings.Contains(setupGet.Body.String(), `name="csrf_token"`) {
		t.Fatalf("setup form did not render with a token: status=%d cookies=%d", setupGet.Code, len(setupCookies))
	}
	setupToken := csrfTokenFromHTML(t, setupGet.Body.String())
	if response := sendMutation(env, mutationRoute{method: http.MethodPost, path: "/setup"}, setupCookies, "", "email=first%40example.invalid&password=valid-password", "application/x-www-form-urlencoded"); response.Code != http.StatusForbidden {
		t.Fatalf("setup without token = %d, want 403", response.Code)
	}
	if count, err := env.store.CountUsers(t.Context()); err != nil || count != 0 {
		t.Fatalf("missing-token setup attempt created users: count=%d err=%v", count, err)
	}

	env.createUser(t, "login-csrf@test.com", "admin")
	setupForm := url.Values{"email": {"first@example.invalid"}, "password": {"valid-password"}, "csrf_token": {setupToken}}
	if response := sendMutation(env, mutationRoute{method: http.MethodPost, path: "/setup"}, setupCookies, "", setupForm.Encode(), "application/x-www-form-urlencoded"); strings.Contains(response.Body.String(), "Invalid or missing CSRF token") {
		t.Fatalf("valid setup token was rejected: %s", response.Body.String())
	}
	loginGet := httptest.NewRecorder()
	env.router.ServeHTTP(loginGet, httptest.NewRequest(http.MethodGet, "/login", nil))
	loginCookies := loginGet.Result().Cookies()
	if loginGet.Code != http.StatusOK || len(loginCookies) == 0 || !strings.Contains(loginGet.Body.String(), `name="csrf_token"`) {
		t.Fatalf("login form did not render with a token: status=%d cookies=%d", loginGet.Code, len(loginCookies))
	}
	if response := sendMutation(env, mutationRoute{method: http.MethodPost, path: "/login"}, loginCookies, "", "email=unknown%40example.invalid&password=bad", "application/x-www-form-urlencoded"); response.Code != http.StatusForbidden {
		t.Fatalf("login without token = %d, want 403", response.Code)
	}
	loginForm := url.Values{"email": {"unknown@example.invalid"}, "password": {"bad"}, "csrf_token": {csrfTokenFromHTML(t, loginGet.Body.String())}}
	if response := sendMutation(env, mutationRoute{method: http.MethodPost, path: "/login"}, loginCookies, "", loginForm.Encode(), "application/x-www-form-urlencoded"); response.Code == http.StatusForbidden && strings.Contains(response.Body.String(), "Invalid or missing CSRF token") {
		t.Fatalf("valid login form token was rejected: %s", response.Body.String())
	}
}

func TestMultipartCSRFRejectionCleansTemporaryUpload(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	tmpDir := filepath.Join(workingDir, "tmp")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmpDir)

	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "csrf-upload-admin@test.com", "admin")
	cookies := env.login(t, "csrf-upload-admin@test.com")
	beforeDB := mutationDBSnapshot(t, env.conn)
	beforeUploads := directorySnapshot(t, filepath.Join(workingDir, "app", "uploads"))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("backup_file", "large-test-backup.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(bytes.Repeat([]byte("x"), 2<<20)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/settings/backup/restore", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("tokenless multipart restore = %d, want 403", response.Code)
	}
	if after := mutationDBSnapshot(t, env.conn); after != beforeDB {
		t.Fatal("tokenless multipart restore mutated the database")
	}
	if after := directorySnapshot(t, filepath.Join(workingDir, "app", "uploads")); !reflect.DeepEqual(after, beforeUploads) {
		t.Fatal("tokenless multipart restore modified application uploads")
	}
	if leftovers, err := os.ReadDir(tmpDir); err != nil || len(leftovers) != 0 {
		t.Fatalf("CSRF-rejected multipart temporary files were not cleaned: entries=%v err=%v", leftovers, err)
	}
}

func TestBearerAPIUsesItsOwnAuthenticationWithoutBrowserCSRF(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "api-csrf@test.com", "admin")
	t.Setenv("WLEDGER_API_TOKEN", "route-test-bearer")
	env.router = New(env.mw, env.session, env.handler)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/global-off", nil)
	request.Header.Set("Authorization", "Bearer route-test-bearer")
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("bearer API without browser CSRF = %d, want 200: %s", response.Code, response.Body.String())
	}
	if env.wled.globalOff != 1 {
		t.Fatalf("authorized API GlobalOff calls = %d, want 1", env.wled.globalOff)
	}

	unauthorized := httptest.NewRequest(http.MethodPost, "/api/v1/global-off", nil)
	unauthorizedResponse := httptest.NewRecorder()
	env.router.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("API without bearer token = %d, want 401", unauthorizedResponse.Code)
	}
	if env.wled.globalOff != 1 {
		t.Fatalf("unauthorized API changed mock WLED call count to %d", env.wled.globalOff)
	}
}
