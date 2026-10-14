package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/categories"
	"github.com/tuxedocurly/wledger/internal/config"
)

func postPartForm(t *testing.T, h *Handler, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/parts", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	h.HandlePartsCreate(response, request)
	return response
}

func TestProductFormSavesCategoryAndTypedSpecifications(t *testing.T) {
	h, conn := setupPartTest(t)
	defer conn.Close()
	defer cleanupPartTest()
	ctx := context.Background()
	svc := categories.NewService(conn)
	categoryID, err := svc.Save(ctx, categories.Category{Name: "Motors"})
	if err != nil {
		t.Fatal(err)
	}
	attributeID, err := svc.SaveAttribute(ctx, categories.AttributeInput{CategoryID: categoryID, Name: "Speed", DataType: categories.NumberType, Unit: "rpm", Required: true})
	if err != nil {
		t.Fatal(err)
	}

	response := postPartForm(t, h, map[string]string{"name": "Motor", "category_id": strconv.FormatInt(categoryID, 10), "spec_" + strconv.FormatInt(attributeID, 10): "1200"})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var partID int64
	if err := conn.QueryRowContext(ctx, `SELECT id FROM parts WHERE name = 'Motor'`).Scan(&partID); err != nil {
		t.Fatal(err)
	}
	assigned, err := svc.ForPart(ctx, partID)
	if err != nil || assigned.ID != categoryID {
		t.Fatalf("product category=%#v err=%v", assigned, err)
	}
	values, err := svc.Specifications(ctx, partID)
	if err != nil || len(values) != 1 || values[0].InputValue() != "1200" {
		t.Fatalf("product specifications=%#v err=%v", values, err)
	}

	if response := postPartForm(t, h, map[string]string{"name": "Invalid motor", "category_id": strconv.FormatInt(categoryID, 10)}); response.Code != http.StatusBadRequest {
		t.Fatalf("missing required specification status=%d, want 400", response.Code)
	}
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM parts WHERE name = 'Invalid motor'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid form created a product: count=%d err=%v", count, err)
	}

	router := chi.NewRouter()
	router.Get("/parts/{id}/edit", h.HandlePartEdit)
	edit := httptest.NewRecorder()
	router.ServeHTTP(edit, httptest.NewRequest(http.MethodGet, "/parts/"+strconv.FormatInt(partID, 10)+"/edit", nil))
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), `name="category_id"`) || !strings.Contains(edit.Body.String(), `name="spec_`+strconv.FormatInt(attributeID, 10)+`"`) {
		t.Fatalf("edit form did not render selected category and specification: status=%d body=%s", edit.Code, edit.Body.String())
	}
}

func postCategoryForm(t *testing.T, h *Handler, path string, fields map[string]string, includeImage bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if includeImage {
		file, err := writer.CreateFormFile("image", "category.png")
		if err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		img.Set(1, 1, color.RGBA{R: 80, G: 120, B: 200, A: 255})
		if err := png.Encode(file, img); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	h.HandleCategoryCreate(response, request)
	return response
}

func TestCategoryImageUploadAndRemoval(t *testing.T) {
	h, conn := setupPartTest(t)
	defer conn.Close()
	defer cleanupPartTest()
	created := postCategoryForm(t, h, "/categories", map[string]string{"name": "Materials"}, true)
	if created.Code != http.StatusSeeOther {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(created.Header().Get("Location"), "/categories/"), 10, 64)
	if err != nil {
		t.Fatalf("invalid create redirect: %q", created.Header().Get("Location"))
	}
	category, err := categories.NewService(conn).Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(category.ImagePath, config.UrlPrefixImages) {
		t.Fatalf("category image path = %q", category.ImagePath)
	}
	imagePath := filepath.Join(config.DirUploadsImages, filepath.Base(category.ImagePath))
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("uploaded category image missing: %v", err)
	}

	router := chi.NewRouter()
	router.Post("/categories/{id}/update", h.HandleCategoryUpdate)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"name": "Materials", "remove_image": "true"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/categories/"+strconv.FormatInt(id, 10)+"/update", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("remove image status=%d body=%s", response.Code, response.Body.String())
	}
	category, err = categories.NewService(conn).Get(context.Background(), id)
	if err != nil || category.ImagePath != "" {
		t.Fatalf("category image after removal = %q err=%v", category.ImagePath, err)
	}
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("category image file still exists: %v", err)
	}
}

func TestCategoryLabelsResolveAndNavigateToCategory(t *testing.T) {
	h, conn := setupPartTest(t)
	defer conn.Close()
	defer cleanupPartTest()

	itemID, err := categories.NewService(conn).Save(context.Background(), categories.Category{Name: "Fasteners"})
	if err != nil {
		t.Fatal(err)
	}
	code := CategoryScanCode(itemID)
	resolution, err := h.resolveScanCode(httptest.NewRequest(http.MethodGet, "/", nil), code)
	if err != nil || resolution.Type != "category" || resolution.ID != itemID {
		t.Fatalf("resolve = %#v, %v", resolution, err)
	}

	scanRequest := httptest.NewRequest(http.MethodGet, "/scan?q="+url.QueryEscape(code), nil)
	scanResponse := httptest.NewRecorder()
	h.HandleScan(scanResponse, scanRequest)
	if scanResponse.Code != http.StatusSeeOther || scanResponse.Header().Get("Location") != "/categories/"+strconv.FormatInt(itemID, 10) {
		t.Fatalf("scan response = %d %q", scanResponse.Code, scanResponse.Header().Get("Location"))
	}

	resolveRequest := httptest.NewRequest(http.MethodGet, "/scan/resolve?q="+url.QueryEscape(code), nil)
	resolveResponse := httptest.NewRecorder()
	h.HandleScanResolve(resolveResponse, resolveRequest)
	var got scanResolution
	if resolveResponse.Code != http.StatusOK || json.Unmarshal(resolveResponse.Body.Bytes(), &got) != nil || got.Type != "category" || got.ID != itemID {
		t.Fatalf("resolver response = %d %s", resolveResponse.Code, resolveResponse.Body.String())
	}

	r := chi.NewRouter()
	r.Get("/categories/{id}/qr", h.HandleCategoryQR)
	r.Get("/scan/barcode/{type}/{id}", h.HandleEntityBarcode)
	r.Get("/labels/{type}/{id}", h.HandleScanLabel)
	for _, path := range []string{
		"/categories/" + strconv.FormatInt(itemID, 10) + "/qr",
		"/scan/barcode/category/" + strconv.FormatInt(itemID, 10),
		"/labels/category/" + strconv.FormatInt(itemID, 10),
	} {
		response := httptest.NewRecorder()
		r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("%s: status=%d body=%s", path, response.Code, response.Body.String())
			continue
		}
		if strings.Contains(path, "/qr") && (response.Header().Get("Content-Type") != "image/png" || response.Body.Len() == 0) {
			t.Errorf("%s did not return a QR PNG", path)
		}
		if strings.Contains(path, "/labels/") && (!strings.Contains(response.Body.String(), "QR code") || !strings.Contains(response.Body.String(), "Code 128")) {
			t.Errorf("%s did not render both label codes", path)
		}
	}

	missing := httptest.NewRecorder()
	h.HandleScan(missing, httptest.NewRequest(http.MethodGet, "/scan?q="+url.QueryEscape("wledger:category:987654"), nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown category scan status=%d, want 404", missing.Code)
	}
}
