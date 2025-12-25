package uierror

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func setupResponderTest(t *testing.T) (*Responder, func()) {
	dbConn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	r := New(logger)

	return r, func() {
		dbConn.Close()
	}
}

func TestResponder_Respond(t *testing.T) {
	r, cleanup := setupResponderTest(t)
	defer cleanup()

	t.Run("Standard Response (non-HTMX)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rr := httptest.NewRecorder()

		testErr := errors.New("something went wrong")
		r.Respond(rr, req, testErr, "Failed to load page", http.StatusInternalServerError)

		if rr.Code != http.StatusInternalServerError {
			t.Errorf("expected status 500, got %d", rr.Code)
		}
	})

	t.Run("HTMX Response", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("HX-Request", "true")
		rr := httptest.NewRecorder()

		testErr := errors.New("htmx error")
		r.Respond(rr, req, testErr, "HTMX failed", http.StatusBadRequest)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rr.Code)
		}
	})
}
