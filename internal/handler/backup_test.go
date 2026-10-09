package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/backup"
	"github.com/tuxedocurly/wledger/internal/uierror"
)

// fakeBackupService returns a fixed result from Restore so the handler's
// outcome rendering can be exercised without a real backup.
type fakeBackupService struct {
	err error
}

func (f *fakeBackupService) Export(ctx context.Context, w io.Writer) error { return f.err }

func (f *fakeBackupService) Restore(ctx context.Context, r io.ReaderAt, size int64) error {
	return f.err
}

// postBackupRestore submits an admin backup restore request and returns the
// rendered response.
func postBackupRestore(t *testing.T, svc backup.Service) *httptest.ResponseRecorder {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	h := &Handler{
		Logger:  logger,
		UIError: uierror.New(logger),
		Backup:  svc,
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("backup_file", "backup.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("dummy")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/backup/restore", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Role: "admin"}))

	rr := httptest.NewRecorder()
	h.HandleBackupRestore(rr, req)
	return rr
}

// TestHandleBackupRestore_CommittedWithWarning verifies that a committed restore
// whose post-restore processing did not complete cleanly is reported as a
// warning, never as a failure.
func TestHandleBackupRestore_CommittedWithWarning(t *testing.T) {
	rr := postBackupRestore(t, &fakeBackupService{
		err: &backup.RestoreCommittedError{Reason: "restored LED coordinate space is unresolved; drawer allocations were not derived"},
	})
	body := rr.Body.String()
	if !strings.Contains(body, "Restore completed with warning") {
		t.Errorf("expected warning message, got: %s", body)
	}
	if strings.Contains(body, "Restore failed") {
		t.Errorf("committed restore must not be reported as failed: %s", body)
	}
}

// TestHandleBackupRestore_Failed verifies that a restore which did not complete
// is reported as a failure.
func TestHandleBackupRestore_Failed(t *testing.T) {
	rr := postBackupRestore(t, &fakeBackupService{err: errors.New("boom")})
	body := rr.Body.String()
	if !strings.Contains(body, "Restore failed") {
		t.Errorf("expected failure message, got: %s", body)
	}
}

// TestHandleBackupRestore_Success verifies the clean success path.
func TestHandleBackupRestore_Success(t *testing.T) {
	rr := postBackupRestore(t, &fakeBackupService{})
	body := rr.Body.String()
	if !strings.Contains(body, "System restored successfully") {
		t.Errorf("expected success message, got: %s", body)
	}
}
