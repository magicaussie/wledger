package handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitRequestBodyRejectsDeclaredOversize(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("unused"))
	req.ContentLength = 11
	rec := httptest.NewRecorder()
	if limitRequestBody(rec, req, 10) {
		t.Fatal("expected oversized Content-Length to be rejected")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestLimitRequestBodyCapsUnknownLengthStream(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("more than limit"))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	if !limitRequestBody(rec, req, 4) {
		t.Fatal("unknown-length body should be wrapped for streaming enforcement")
	}
	_, err := io.ReadAll(req.Body)
	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("read error = %v, want *http.MaxBytesError", err)
	}
}
