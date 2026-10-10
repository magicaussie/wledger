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

// fakeBackupService returns a fixed result from Export/Restore so the handler's
// outcome rendering can be exercised without a real backup.
type fakeBackupService struct {
	err  error
	data []byte
}

func (f *fakeBackupService) Export(ctx context.Context, w io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.err != nil {
		return f.err
	}
	if f.data != nil {
		_, _ = w.Write(f.data)
	}
	return nil
}

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

// getBackupDownload submits an admin backup download request and returns the
// response.
func getBackupDownload(t *testing.T, svc backup.Service) *httptest.ResponseRecorder {
	return getBackupDownloadCtx(t, context.Background(), svc)
}

// getBackupDownloadCtx is getBackupDownload with an explicit request context.
func getBackupDownloadCtx(t *testing.T, ctx context.Context, svc backup.Service) *httptest.ResponseRecorder {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := &Handler{
		Logger:  logger,
		UIError: uierror.New(logger),
		Backup:  svc,
	}
	reqCtx := auth.WithUser(ctx, auth.User{ID: 1, Role: "admin"})
	req := httptest.NewRequest(http.MethodGet, "/settings/backup/download", nil).WithContext(reqCtx)
	rr := httptest.NewRecorder()
	h.HandleBackupDownload(rr, req)
	return rr
}

// TestHandleBackupDownload_Success verifies a completed export is served with the
// download headers and the archive body.
func TestHandleBackupDownload_Success(t *testing.T) {
	payload := []byte("PK\x03\x04fake-archive")
	rr := getBackupDownload(t, &fakeBackupService{data: payload})
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	if !strings.Contains(rr.Header().Get("Content-Disposition"), "attachment") {
		t.Errorf("Content-Disposition = %q, want an attachment", rr.Header().Get("Content-Disposition"))
	}
	if !bytes.Equal(rr.Body.Bytes(), payload) {
		t.Errorf("body = %q, want the archive bytes", rr.Body.Bytes())
	}
}

// TestHandleBackupDownload_FailureDoesNotServeArchive verifies that a failed
// export is reported as an error and never offered to the administrator as an
// apparently valid archive.
func TestHandleBackupDownload_FailureDoesNotServeArchive(t *testing.T) {
	rr := getBackupDownload(t, &fakeBackupService{err: errors.New("boom")})
	if rr.Code == http.StatusOK {
		t.Errorf("a failed export must not return 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct == "application/zip" {
		t.Error("a failed export must not advertise an archive")
	}
	if !strings.Contains(rr.Body.String(), "Failed to generate backup") {
		t.Errorf("expected an error response, got: %s", rr.Body.String())
	}
}

// TestHandleBackupDownload_TempFileCleanedUp verifies the temporary ZIP used to
// stage the download is removed on success, on export failure and when the
// request is cancelled.
func TestHandleBackupDownload_TempFileCleanedUp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	assertNoTempFiles := func(t *testing.T, stage string) {
		t.Helper()
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read temp dir: %v", err)
		}
		if len(entries) != 0 {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("%s: temporary backup files were not cleaned up: %v", stage, names)
		}
	}

	t.Run("success", func(t *testing.T) {
		rr := getBackupDownload(t, &fakeBackupService{data: []byte("PK\x03\x04archive")})
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
		assertNoTempFiles(t, "success")
	})

	t.Run("export failure", func(t *testing.T) {
		_ = getBackupDownload(t, &fakeBackupService{err: errors.New("boom")})
		assertNoTempFiles(t, "export failure")
	})

	t.Run("cancelled request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = getBackupDownloadCtx(t, ctx, &fakeBackupService{data: []byte("PK\x03\x04archive")})
		assertNoTempFiles(t, "cancelled request")
	})
}
