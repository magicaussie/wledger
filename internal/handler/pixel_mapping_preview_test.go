package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestControllerOutputMappingPreviewUsesCanonicalGoPixelMap(t *testing.T) {
	requestBody := []byte(`{"controller_type":"wled","outputs":[{"display_order":0,"name":"Vertical mixed string","pixel_count":6,"direction":"forward","pattern":"custom","rows":2,"columns":3,"custom_order":[0,2,4,1,3,5]}]}`)
	request := httptest.NewRequest(http.MethodPost, "/hardware/output-mapping-preview", bytes.NewReader(requestBody))
	response := httptest.NewRecorder()
	(&Handler{}).HandleControllerOutputMappingPreview(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", response.Code, response.Body.String())
	}
	var actual []outputMappingPreview
	if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	want := []outputMappingPreview{{
		DisplayOrder: 0, PixelCount: 6,
		Pixels: []outputMappingPreviewPixel{
			{Logical: 1, Physical: 1}, {Logical: 2, Physical: 3}, {Logical: 3, Physical: 5},
			{Logical: 4, Physical: 2}, {Logical: 5, Physical: 4}, {Logical: 6, Physical: 6},
		},
	}}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("canonical preview = %+v, want %+v", actual, want)
	}
}

func TestControllerOutputMappingPreviewPaginatesExactLaterPixels(t *testing.T) {
	requestBody := []byte(`{"controller_type":"wled","outputs":[{"display_order":0,"name":"Vertical mixed string","pixel_count":6,"pattern":"custom","rows":2,"columns":3,"custom_order":[0,2,4,1,3,5]}],"preview_order":0,"preview_start":3}`)
	request := httptest.NewRequest(http.MethodPost, "/hardware/output-mapping-preview", bytes.NewReader(requestBody))
	response := httptest.NewRecorder()
	(&Handler{}).HandleControllerOutputMappingPreview(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", response.Code, response.Body.String())
	}
	var actual []outputMappingPreview
	if err := json.Unmarshal(response.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	want := []outputMappingPreview{{
		DisplayOrder: 0, PixelCount: 6,
		Pixels: []outputMappingPreviewPixel{{Logical: 4, Physical: 2}, {Logical: 5, Physical: 4}, {Logical: 6, Physical: 6}},
	}}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("later-page canonical preview = %+v, want %+v", actual, want)
	}
}

func TestControllerOutputMappingPreviewRejectsInvalidCustomPermutation(t *testing.T) {
	requestBody := []byte(`{"outputs":[{"display_order":0,"name":"Broken","pixel_count":3,"pattern":"custom","custom_order":[0,0,2]}]}`)
	request := httptest.NewRequest(http.MethodPost, "/hardware/output-mapping-preview", bytes.NewReader(requestBody))
	response := httptest.NewRecorder()
	(&Handler{}).HandleControllerOutputMappingPreview(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid mapping preview status = %d, body=%s; want 400", response.Code, response.Body.String())
	}
}
