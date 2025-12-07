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

// ContextKey is a custom type to prevent context key collisions
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

// ReuqestLogger logs every request with method, path, and duration
func (m *Manager) RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// wrapper to capture the status code
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

// RequireAuth checks if a user is logged in. If not, redirects to /login
func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// check session for user_id
		userID := m.Session.GetInt(r.Context(), "user_id")
		if userID == 0 {
			// Not logged in -> Redirect
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Add user_id to context for handlers to use
		ctx := context.WithValue(r.Context(), UserContextKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// FirstRunCheck detects if the system has NO users and forces a redirect to /setup
func (m *Manager) FirstRunCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow list paths that MUST work without users
		path := r.URL.Path
		if path == "/setup" || strings.HasPrefix(path, "/static/") || path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Check user count
		count, err := m.Queries.CountUsers(r.Context())
		if err != nil {
			m.Logger.Error("failed to count users", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// If no users redirect to /setup
		if count == 0 {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// InjectColors inserts global color settings into the HTML for every request
// TODO: implement this fully later. Stubbing it for now to prevent errors
func (m *Manager) InjectColors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Placeholder for now
		next.ServeHTTP(w, r)
	})
}

// statusWriter captures the HTTP status code
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
