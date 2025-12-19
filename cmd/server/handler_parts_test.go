package main

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	_ "image/png" // Ensure PNG decoder is registered

	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/images"
)

// Reusing setup logic from handler_hardware_test.go
func setupPartTest(t *testing.T) (*application, *sql.DB) {
	// Setup DB
	dbConn := openTestDB(t)
	setupTestSchema(t, dbConn)

	// Setup Logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Setup File System for Test
	// Ensure there is a clean state for uploads
	// The handlers hardcode "./app/uploads" for now, so I need to make sure directory exists relative to working directory
	_ = os.RemoveAll("./app/uploads")
	_ = os.MkdirAll("./app/uploads/images", 0755)
	_ = os.MkdirAll("./app/uploads/docs", 0755)

	// Init images package (ensure dir exists)
	_ = images.Init()

	queries := db.New(dbConn)

	app := &application{
		logger:   logger,
		queries:  queries,
		database: dbConn,
	}

	return app, dbConn
}

func cleanupPartTest() {
	_ = os.RemoveAll("./app/uploads")
}

// Helper to create a multipart request
func createMultipartRequest(t *testing.T, uri, method string, fields map[string]string, files map[string]string) *http.Request {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add Fields
	for k, v := range fields {
		_ = writer.WriteField(k, v)
	}

	// Add Files
	for fieldName, fileName := range files {
		part, err := writer.CreateFormFile(fieldName, fileName)
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		// Write dummy content
		if strings.HasSuffix(fileName, ".jpg") || strings.HasSuffix(fileName, ".png") {
			// Generate real image data
			img := image.NewRGBA(image.Rect(0, 0, 10, 10))
			// Fill with white
			for y := 0; y < 10; y++ {
				for x := 0; x < 10; x++ {
					img.Set(x, y, color.White)
				}
			}

			// Encode to JPEG (using JPEG for simplicity as processor supports it well)
			// even if filename is .png, processor checks extension.
			err := jpeg.Encode(part, img, nil)
			if err != nil {
				t.Fatalf("failed to encode test image: %v", err)
			}
		} else {
			io.WriteString(part, "dummy content for "+fileName)
		}
	}

	writer.Close()

	req := httptest.NewRequest(method, uri, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestPartCreate_HappyPath(t *testing.T) {
	app, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Prepare Request with .png to match the generated content in createMultipartRequest
	req := createMultipartRequest(t, "/parts", "POST", map[string]string{
		"name":         "Test Part",
		"barcode_data": "1001",
		"unit_cost":    "10.50",
	}, map[string]string{
		"image":     "test.png",
		"documents": "doc.pdf",
	})

	rr := httptest.NewRecorder()
	app.handlePartsCreate(rr, req)

	// Check Redirect
	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d. Body: %s", rr.Code, rr.Body.String())
	}

	// Verify DB
	var count int
	dbConn.QueryRow("SELECT count(*) FROM parts").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 part, got %d", count)
	}

	var docCount int
	dbConn.QueryRow("SELECT count(*) FROM part_docs").Scan(&docCount)
	if docCount != 1 {
		t.Errorf("expected 1 doc, got %d", docCount)
	}

	// Verify Files
	files, _ := os.ReadDir("./app/uploads/images")
	if len(files) == 0 {
		t.Error("expected image file to be created")
	}

	docs, _ := os.ReadDir("./app/uploads/docs")
	if len(docs) == 0 {
		t.Error("expected doc file to be created")
	}
}

func TestPartCreate_RollbackOnError(t *testing.T) {
	app, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Create a part to occupy barcode "1001"
	_, _ = app.queries.CreatePart(context.Background(), db.CreatePartParams{
		Name: "Existing Part", BarcodeData: sql.NullString{String: "1001", Valid: true},
	})

	// Try to create ANOTHER part with SAME barcode (DB Constraint Violation)
	// also include a file upload to test cleanup
	req := createMultipartRequest(t, "/parts", "POST", map[string]string{
		"name":         "Duplicate Part",
		"barcode_data": "1001",
	}, map[string]string{
		"image":     "dup.png",
		"documents": "dup_doc.pdf",
	})

	rr := httptest.NewRecorder()
	app.handlePartsCreate(rr, req)

	// Expect Error (Conflict or Internal Server Error handled by handler)
	// The handler catches UNIQUE constraint and returns 409 Conflict
	if rr.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict, got %d", rr.Code)
	}

	// Verify DB Rollback (Should still only be 1 part)
	var count int
	dbConn.QueryRow("SELECT count(*) FROM parts").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 part, got %d", count)
	}

	// Verify File Cleanup
	// The handler uploads files -> Starts TX -> Inserts -> Fails -> Defer Cleanup.
	files, _ := os.ReadDir("./app/uploads/images")
	if len(files) > 0 {
		t.Errorf("expected 0 images (cleanup failed), got %d: %v", len(files), files)
	}

	docs, _ := os.ReadDir("./app/uploads/docs")
	if len(docs) > 0 {
		t.Errorf("expected 0 docs (cleanup failed), got %d", len(docs))
	}
}

func TestPartUpdate_RollbackOnError(t *testing.T) {
	app, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Create Initial Part with Image
	// a file was put there to simulate existing state
	_ = os.MkdirAll("./app/uploads/images", 0755)
	initialImgName := "initial.jpg"
	_ = os.WriteFile("./app/uploads/images/"+initialImgName, []byte("dummy"), 0644)

	id, err := app.queries.CreatePart(context.Background(), db.CreatePartParams{
		Name:      "Original Part",
		ImagePath: sql.NullString{String: "/uploads/images/" + initialImgName, Valid: true},
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Create another part to conflict with
	_, _ = app.queries.CreatePart(context.Background(), db.CreatePartParams{
		Name: "Blocker", BarcodeData: sql.NullString{String: "9999", Valid: true},
	})

	// Update "Original Part" to have barcode "9999" (Conflict)
	// AND upload a NEW image "new.png"
	req := createMultipartRequest(t, "/parts/"+strconv.Itoa(int(id))+"/update", "POST", map[string]string{
		"name":         "Updated Name",
		"barcode_data": "9999", // Conflict!
	}, map[string]string{
		"image": "new.png",
	})

	// Setup Router to handle params
	r := chi.NewRouter()
	r.Post("/parts/{id}/update", app.handlePartUpdate)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Expect Error (500 because UpdatePart doesn't explicitly check UNIQUE error string like Create does, it just logs and 500s)
	// TODO: fix this experience by handling UNIQUE constraint in UpdatePart like CreatePart does.
	// ensure UI shows proper error to the user.
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rr.Code)
	}

	// Verify DB state (Should match Original)
	p, _ := app.queries.GetPart(context.Background(), id)
	if p.Name != "Original Part" {
		t.Errorf("DB modified despite rollback! Name: %s", p.Name)
	}
	if p.ImagePath.String != "/uploads/images/"+initialImgName {
		t.Errorf("Image path modified! %s", p.ImagePath.String)
	}

	// Verify File System
	// "initial.jpg" MUST exist (Old image preserved)
	if _, err := os.Stat("./app/uploads/images/" + initialImgName); os.IsNotExist(err) {
		t.Error("Original image was deleted!")
	}

	// "new.png" (processed name) MUST NOT exist (New image cleaned up)
	// check the directory for any *other* files.
	files, _ := os.ReadDir("./app/uploads/images")
	for _, f := range files {
		if f.Name() != initialImgName && !strings.Contains(f.Name(), "_thumb") {
			// Note: processUpload creates _thumb. Check if it's the initial one or new one.
			t.Errorf("Found unexpected file (cleanup failed): %s", f.Name())
		}
	}
}

func TestPartUpdate_HappyPath_ImageSwap(t *testing.T) {
	app, dbConn := setupPartTest(t)
	defer dbConn.Close()
	defer cleanupPartTest()

	// Create Initial Part with Image (Fake content)
	initialImgName := "part_old.jpg"
	_ = os.WriteFile("./app/uploads/images/"+initialImgName, []byte("dummy"), 0644)
	_ = os.WriteFile("./app/uploads/images/part_old_thumb.jpg", []byte("dummy"), 0644) // Fake thumb

	id, _ := app.queries.CreatePart(context.Background(), db.CreatePartParams{
		Name:      "Old Name",
		ImagePath: sql.NullString{String: "/uploads/images/" + initialImgName, Valid: true},
	})

	// Update with NEW image
	req := createMultipartRequest(t, "/parts/"+strconv.Itoa(int(id))+"/update", "POST", map[string]string{
		"name": "New Name",
	}, map[string]string{
		"image": "new_swap.png",
	})

	r := chi.NewRouter()
	r.Post("/parts/{id}/update", app.handlePartUpdate)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rr.Code)
	}

	// Verify DB
	p, _ := app.queries.GetPart(context.Background(), id)
	if p.Name != "New Name" {
		t.Error("Name not updated")
	}
	if p.ImagePath.String == "/uploads/images/"+initialImgName {
		t.Error("Image path not updated")
	}

	// Verify FS
	// Old Image GONE
	if _, err := os.Stat("./app/uploads/images/" + initialImgName); !os.IsNotExist(err) {
		t.Error("Old image was NOT deleted")
	}
	// New Image EXISTS
	// The new path in DB should exist on disk
	newDiskPath := "." + strings.Replace(p.ImagePath.String, "/uploads", "/app/uploads", 1)
	if _, err := os.Stat(newDiskPath); os.IsNotExist(err) {
		t.Errorf("New image not found at %s", newDiskPath)
	}
}
