// Package api exposes a small machine-to-machine JSON API used by Home
// Assistant integrations and the bundled MCP server. Authentication is via a
// static bearer token configured through the WLEDGER_API_TOKEN environment
// variable; when the token is empty the API routes are not mounted.
//
// Endpoints (all require "Authorization: Bearer <token>"):
//
//	POST /api/v1/global-off                 Turn off all controllers' LEDs
//	POST /api/v1/parts/{id}/locate          Locate a part by id (flash its bins)
//	POST /api/v1/bins/{id}/locate           Locate a bin by id (flash its LEDs)
//	GET  /api/v1/parts?q=                   Search parts (name/part no/barcode)
//	GET  /api/v1/parts/{id}                 Part detail incl. stock/locations
//	GET  /api/v1/hardware                   List controllers
//	GET  /api/v1/health                     Liveness probe
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// Handler holds the dependencies needed by the API handlers.
type Handler struct {
	queries db.Store
	wled    wled.Service
	parts   parts.Service
}

// NewHandler creates the API handler.
func NewHandler(queries db.Store, wledService wled.Service, partsService parts.Service) *Handler {
	return &Handler{queries: queries, wled: wledService, parts: partsService}
}

// Token returns the configured API token, or "" if the API is disabled.
func Token() string {
	return os.Getenv("WLEDGER_API_TOKEN")
}

// Enabled reports whether the API token is configured.
func Enabled() bool { return Token() != "" }

// Routes mounts the API sub-router under /api/v1 with token auth.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(h.tokenMiddleware)
	r.Get("/health", h.health)
	r.Post("/global-off", h.globalOff)
	r.Post("/parts/{id}/locate", h.locatePart)
	r.Post("/bins/{id}/locate", h.locateBin)
	r.Get("/parts", h.searchParts)
	r.Get("/parts/{id}", h.getPart)
	r.Get("/hardware", h.listHardware)
	return r
}

// tokenMiddleware enforces the bearer token with a constant-time comparison.
func (h *Handler) tokenMiddleware(next http.Handler) http.Handler {
	token := Token()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		given := r.Header.Get("Authorization")
		given = strings.TrimPrefix(given, "Bearer ")
		given = strings.TrimPrefix(given, "Token ")
		if subtle.ConstantTimeCompare([]byte(given), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type responseError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, responseError{Error: msg})
}

func pathID(r *http.Request) (int64, error) {
	idStr := chi.URLParam(r, "id")
	if idStr == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(idStr, 10, 64)
}
