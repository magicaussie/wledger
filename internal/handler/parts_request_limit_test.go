package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tuxedocurly/wledger/internal/config"
)

// TestPartHandlersRejectOversizedBody verifies that part create and update apply
// a hard body limit before parsing the multipart form. Previously these handlers
// relied on ParseMultipartForm's memory threshold, which is not a size limit, so
// an oversized upload (for example one carrying a valid CSRF header that skips
// the middleware's bounded parse) could be spooled without bound.
func TestPartHandlersRejectOversizedBody(t *testing.T) {
	cases := map[string]func(*Handler, http.ResponseWriter, *http.Request){
		"create": (*Handler).HandlePartsCreate,
		"update": (*Handler).HandlePartUpdate,
	}
	for name, handle := range cases {
		t.Run(name, func(t *testing.T) {
			h := &Handler{}
			req := httptest.NewRequest(http.MethodPost, "/parts", nil)
			req.ContentLength = config.MaxUploadSizeParts + 1
			rec := httptest.NewRecorder()
			handle(h, rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413", rec.Code)
			}
		})
	}
}
