package parts

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/tags"
)

// setupImageTestService builds a part service whose image storage is isolated in
// a temporary directory. Image paths in config are relative to the working
// directory, so the test chdirs into a temp dir to guarantee that no production
// image files are ever touched.
func setupImageTestService(t *testing.T) (Service, db.Store, context.Context) {
	t.Helper()

	t.Chdir(t.TempDir())

	if err := os.MkdirAll(config.DirUploadsImages, 0o755); err != nil {
		t.Fatalf("failed to create image dir: %v", err)
	}

	database, s, dbCleanup := setupTestDB(t)
	t.Cleanup(dbCleanup)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	tagSvc := tags.NewService(database, s)
	docSvc := documents.NewService(s, logger)
	svc := NewService(database, s, logger, tagSvc, docSvc)

	if _, err := s.CreateUser(context.Background(), db.CreateUserParams{
		Email:        "admin@test.com",
		PasswordHash: "hash",
		Role:         "admin",
	}); err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))
	return svc, s, ctx
}

// newTestImageUpload builds a DocUpload backed by a real multipart file (via the
// same FormFile path the HTTP handler uses) so that images.ProcessUpload can
// decode, resize and persist it.
func newTestImageUpload(t *testing.T, filename string) *DocUpload {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatalf("failed to encode test image: %v", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("image", filename)
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	if _, err := fw.Write(raw.Bytes()); err != nil {
		t.Fatalf("failed to write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("failed to close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "/", &body)
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("failed to parse multipart form: %v", err)
	}

	file, header, err := req.FormFile("image")
	if err != nil {
		t.Fatalf("failed to read form file: %v", err)
	}

	return &DocUpload{File: file, Header: header}
}

// imageFilePaths derives the on-disk main image and thumbnail paths from a web
// path such as "/uploads/images/part_123.jpg".
func imageFilePaths(webPath string) (main, thumb string) {
	fileName := filepath.Base(webPath)
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	return filepath.Join(config.DirUploadsImages, fileName),
		filepath.Join(config.DirUploadsImages, base+"_thumb.jpg")
}

func assertImageFilesExist(t *testing.T, webPath string, want bool) {
	t.Helper()
	main, thumb := imageFilePaths(webPath)
	for _, p := range []string{main, thumb} {
		_, err := os.Stat(p)
		exists := err == nil
		if exists != want {
			t.Errorf("file %s exists=%v, want %v", p, exists, want)
		}
	}
}

func listImageFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(config.DirUploadsImages)
	if err != nil {
		t.Fatalf("failed to read image dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// TestUpdatePart_FailurePreservesExistingImage reproduces defect D1: a failed
// update that does not upload a replacement image must not delete the existing
// image or thumbnail.
func TestUpdatePart_FailurePreservesExistingImage(t *testing.T) {
	svc, _, ctx := setupImageTestService(t)

	// A conflicting part owns the barcode we will try to reuse.
	if _, err := svc.CreatePart(ctx, CreatePartRequest{Name: "Conflict", BarcodeData: "DUP-BC"}); err != nil {
		t.Fatalf("failed to create conflicting part: %v", err)
	}

	// Target part with an existing image.
	id, err := svc.CreatePart(ctx, CreatePartRequest{
		Name:        "Target",
		BarcodeData: "ORIG-BC",
		Image:       newTestImageUpload(t, "original.png"),
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	before, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if !before.ImagePath.Valid || before.ImagePath.String == "" {
		t.Fatalf("expected part to have an image, got %+v", before.ImagePath)
	}
	originalImage := before.ImagePath.String
	assertImageFilesExist(t, originalImage, true)

	// Invalid update: duplicate barcode, no new image uploaded.
	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:          id,
		Name:        "Target",
		BarcodeData: "DUP-BC",
	})
	if err == nil {
		t.Fatal("expected UpdatePart to fail on duplicate barcode")
	}

	// Existing image and thumbnail must remain.
	assertImageFilesExist(t, originalImage, true)

	// Database must still reference the original image.
	after, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if after.ImagePath.String != originalImage {
		t.Errorf("expected image path %q to be preserved, got %q", originalImage, after.ImagePath.String)
	}
}

// TestUpdatePart_FailedReplacementCleansNewImage verifies that when a
// replacement image is uploaded but the database update fails, the newly
// generated files are removed while the original image is preserved.
func TestUpdatePart_FailedReplacementCleansNewImage(t *testing.T) {
	svc, _, ctx := setupImageTestService(t)

	if _, err := svc.CreatePart(ctx, CreatePartRequest{Name: "Conflict", BarcodeData: "DUP-BC"}); err != nil {
		t.Fatalf("failed to create conflicting part: %v", err)
	}

	id, err := svc.CreatePart(ctx, CreatePartRequest{
		Name:        "Target",
		BarcodeData: "ORIG-BC",
		Image:       newTestImageUpload(t, "original.png"),
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	before, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	originalImage := before.ImagePath.String
	if originalImage == "" {
		t.Fatal("expected part to have an original image")
	}

	filesBefore := listImageFiles(t)
	if len(filesBefore) != 2 {
		t.Fatalf("expected 2 image files (main + thumb) before update, got %v", filesBefore)
	}

	// Replacement upload that fails during DB persistence (duplicate barcode).
	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:          id,
		Name:        "Target",
		BarcodeData: "DUP-BC",
		Image:       newTestImageUpload(t, "replacement.png"),
	})
	if err == nil {
		t.Fatal("expected UpdatePart to fail on duplicate barcode")
	}

	// Original image must remain.
	assertImageFilesExist(t, originalImage, true)

	// Newly generated files must be cleaned up: directory contents unchanged.
	filesAfter := listImageFiles(t)
	if !reflect.DeepEqual(filesBefore, filesAfter) {
		t.Errorf("expected image files to be unchanged after failed replacement:\nbefore=%v\nafter=%v", filesBefore, filesAfter)
	}

	// Database must still reference the original image.
	after, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if after.ImagePath.String != originalImage {
		t.Errorf("expected image path %q to be preserved, got %q", originalImage, after.ImagePath.String)
	}
}

// TestUpdatePart_SuccessfulImageReplacement verifies that a successful
// replacement keeps the new image and removes the previous one.
func TestUpdatePart_SuccessfulImageReplacement(t *testing.T) {
	svc, _, ctx := setupImageTestService(t)

	id, err := svc.CreatePart(ctx, CreatePartRequest{
		Name:  "Target",
		Image: newTestImageUpload(t, "original.png"),
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	before, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	originalImage := before.ImagePath.String
	if originalImage == "" {
		t.Fatal("expected part to have an original image")
	}

	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:    id,
		Name:  "Target",
		Image: newTestImageUpload(t, "replacement.png"),
	})
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	after, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	replacementImage := after.ImagePath.String
	if replacementImage == "" {
		t.Fatal("expected part to reference a replacement image")
	}
	if replacementImage == originalImage {
		t.Fatalf("expected a new image path, got the original %q", originalImage)
	}

	// Replacement image and thumbnail exist.
	assertImageFilesExist(t, replacementImage, true)

	// Previous image and thumbnail removed.
	assertImageFilesExist(t, originalImage, false)

	// Only the replacement's two files remain.
	files := listImageFiles(t)
	if len(files) != 2 {
		t.Errorf("expected only the replacement image files to remain, got %v", files)
	}
}

// TestUpdatePart_UpdateWithoutImageChange verifies that updating ordinary fields
// leaves the existing image and thumbnail untouched.
func TestUpdatePart_UpdateWithoutImageChange(t *testing.T) {
	svc, _, ctx := setupImageTestService(t)

	id, err := svc.CreatePart(ctx, CreatePartRequest{
		Name:        "Target",
		Description: "before",
		Image:       newTestImageUpload(t, "original.png"),
	})
	if err != nil {
		t.Fatalf("CreatePart failed: %v", err)
	}

	before, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	originalImage := before.ImagePath.String
	if originalImage == "" {
		t.Fatal("expected part to have an original image")
	}
	filesBefore := listImageFiles(t)

	err = svc.UpdatePart(ctx, UpdatePartRequest{
		ID:          id,
		Name:        "Target Renamed",
		Description: "after",
	})
	if err != nil {
		t.Fatalf("UpdatePart failed: %v", err)
	}

	after, err := svc.GetPart(ctx, id)
	if err != nil {
		t.Fatalf("GetPart failed: %v", err)
	}
	if after.ImagePath.String != originalImage {
		t.Errorf("expected image path %q to be unchanged, got %q", originalImage, after.ImagePath.String)
	}
	if after.Name != "Target Renamed" {
		t.Errorf("expected name to be updated, got %q", after.Name)
	}

	assertImageFilesExist(t, originalImage, true)
	filesAfter := listImageFiles(t)
	if !reflect.DeepEqual(filesBefore, filesAfter) {
		t.Errorf("expected image files to be unchanged:\nbefore=%v\nafter=%v", filesBefore, filesAfter)
	}
}
