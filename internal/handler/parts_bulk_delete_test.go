package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestHandlePartsBulkDelete(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	ctx := context.Background()

	// Create test parts
	p1, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Part 1"})
	p2, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Part 2"})
	p3, _ := h.Queries.CreatePart(ctx, db.CreatePartParams{Name: "Part 3"})

	// Prepare request
	form := url.Values{}
	form.Add("ids", fmt.Sprintf("%d", p1))
	form.Add("ids", fmt.Sprintf("%d", p2))

	req := httptest.NewRequest("POST", "/parts/bulk", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	// Mock User (Admin)
	user := auth.User{ID: 1, Email: "admin@example.com", Role: "admin"}
	req = req.WithContext(auth.WithUser(req.Context(), user))

	rr := httptest.NewRecorder()
	h.HandlePartsBulkDelete(rr, req)

	// Verify Response
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	redirect := rr.Header().Get("HX-Redirect")
	if redirect != "/parts" {
		t.Errorf("expected HX-Redirect to /parts, got: %s", redirect)
	}

	// Verify DB
	var count int
	dbConn.QueryRow("SELECT count(*) FROM parts").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 part remaining, got %d", count)
	}

	// Verify specific part remains
	var remainingID int64
	err := dbConn.QueryRow("SELECT id FROM parts").Scan(&remainingID)
	if err != nil || remainingID != p3 {
		t.Errorf("expected Part 3 to remain, got ID %d", remainingID)
	}
}

func TestHandlePartsBulkDelete_Unauthorized(t *testing.T) {
	h, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	req := httptest.NewRequest("DELETE", "/parts/bulk", nil)

	// Mock User (Viewer - no write access)
	user := auth.User{ID: 2, Email: "viewer@example.com", Role: "viewer"}
	req = req.WithContext(auth.WithUser(req.Context(), user))

	rr := httptest.NewRecorder()
	h.HandlePartsBulkDelete(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}
