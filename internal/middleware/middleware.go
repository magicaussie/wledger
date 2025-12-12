package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/db"
)

type ContextKey string

const (
	UserContextKey ContextKey = "user_id"
)

type Manager struct {
	Queries *db.Queries
	Session *scs.SessionManager
	Logger  *slog.Logger
}

func New(q *db.Queries, sm *scs.SessionManager, l *slog.Logger) *Manager {
	return &Manager{
		Queries: q,
		Session: sm,
		Logger:  l,
	}
}

func (m *Manager) RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(ww, r)
		m.Logger.Info(
			"http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.status,
			"duration", time.Since(start),
			"ip", r.RemoteAddr,
		)
	})
}

// RequireAuth forces a login for WRITE/Protected operations
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Use GetInt to match how Login stores user_id
		userID := m.Session.GetInt(r.Context(), "user_id")
		if userID == 0 {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Cast to int64 for DB compatibility
		ctx := context.WithValue(r.Context(), UserContextKey, int64(userID))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireReadAuth checks the DB setting. If "Require Auth" is ON, it behaves like RequireAuth.
func (m *Manager) RequireReadAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If user is already logged in, allow access
		userID := m.Session.GetInt(r.Context(), "user_id")
		if userID > 0 {
			ctx := context.WithValue(r.Context(), UserContextKey, int64(userID))
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// If not logged in, check DB settings
		s, err := m.Queries.GetSettings(r.Context())
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// If Settings say "Require Auth", redirect guest
		if s.RequireAuthForRead.Bool {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Otherwise, public access is allowed
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) FirstRunCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/setup" || strings.HasPrefix(path, "/static/") || path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		count, err := m.Queries.CountUsers(r.Context())
		if err != nil {
			m.Logger.Error("failed to count users", "error", err)
			next.ServeHTTP(w, r)
			return
		}

		if count == 0 {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
