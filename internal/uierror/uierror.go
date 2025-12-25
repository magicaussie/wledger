package uierror

import (
	"log/slog"
	"net/http"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/web/pages"
)

// Responder handles logging and rendering errors for the UI.
// It supports both full-page error rendering and HTMX partials.
type Responder struct {
	logger *slog.Logger
}

// New creates a new UI error Responder.
func New(logger *slog.Logger) *Responder {
	return &Responder{
		logger: logger,
	}
}

// Respond standardizes error handling across handlers and middleware.
// It logs the error with context and renders a user-friendly response.
// If the request is an HTMX request, it returns an HTMX-compatible partial.
func (r *Responder) Respond(w http.ResponseWriter, req *http.Request, err error, message string, code int) {
	// Log the error with context
	r.logger.Error(message, "err", err, "path", req.URL.Path, "method", req.Method)

	// Check for HTMX request
	isHTMX := req.Header.Get("HX-Request") == "true"

	if isHTMX {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(code)
		pages.ErrorPartial(message).Render(req.Context(), w)
		return
	}

	// Standard Response (Full Page)
	user := auth.GetUserFromRequest(req)
	w.WriteHeader(code)
	pages.ErrorPage(user, message, code).Render(req.Context(), w)
}
