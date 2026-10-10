package handler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

// TestLoginRotatesCSRFToken verifies that authenticating discards any CSRF token
// obtained before login, so it cannot be reused with the session's new privileges.
func TestLoginRotatesCSRFToken(t *testing.T) {
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	store := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sm := scs.New()
	h := &Handler{Logger: logger, Queries: store, Database: dbConn, Session: sm, UIError: uierror.New(logger)}

	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := store.CreateUser(context.Background(), db.CreateUserParams{
		Email: "admin@test.com", PasswordHash: hash, Role: "admin",
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	r := chi.NewRouter()
	r.Use(sm.LoadAndSave)
	r.Get("/probe", func(w http.ResponseWriter, req *http.Request) {
		tok, err := middleware.CSRFToken(req.Context(), sm)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(tok))
	})
	r.Post("/check", func(w http.ResponseWriter, req *http.Request) {
		if middleware.ValidateCSRF(req.Context(), sm, req.FormValue("t")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	})
	r.Post("/login", h.HandleLoginPost)

	// Obtain a pre-login CSRF token and its session cookie.
	prr := httptest.NewRecorder()
	r.ServeHTTP(prr, httptest.NewRequest(http.MethodGet, "/probe", nil))
	token1 := prr.Body.String()
	cookie1 := prr.Result().Cookies()
	if token1 == "" || len(cookie1) == 0 {
		t.Fatalf("no pre-login token/cookie (token=%q cookies=%d)", token1, len(cookie1))
	}

	// Log in, carrying the pre-login session.
	form := url.Values{"email": {"admin@test.com"}, "password": {"secret123"}}
	lr := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	lr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookie1 {
		lr.AddCookie(c)
	}
	lrr := httptest.NewRecorder()
	r.ServeHTTP(lrr, lr)
	if lrr.Code != http.StatusSeeOther {
		t.Fatalf("login = %d, want 303: %s", lrr.Code, lrr.Body.String())
	}
	cookies := lrr.Result().Cookies()
	if len(cookies) == 0 {
		cookies = cookie1 // RenewToken updates the session data in place
	}

	// The old token must not validate in the authenticated session.
	cr := httptest.NewRequest(http.MethodPost, "/check", strings.NewReader(url.Values{"t": {token1}}.Encode()))
	cr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		cr.AddCookie(c)
	}
	crr := httptest.NewRecorder()
	r.ServeHTTP(crr, cr)
	if crr.Code != http.StatusForbidden {
		t.Fatalf("pre-login token validated after login (%d), want 403", crr.Code)
	}
}
