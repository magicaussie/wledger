package handler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/db"
)

func TestControllerCreateAndEditRejectInvalidPorts(t *testing.T) {
	h, store, conn := setupHardwareHandler(t)
	defer conn.Close()
	ctx := context.Background()
	controller, err := store.CreateController(ctx, db.CreateControllerParams{
		Name: "Existing", IpAddress: "192.0.2.70", Port: sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	createRouter := chi.NewRouter()
	createRouter.Post("/hardware", h.HandleHardwareCreate)
	editRouter := chi.NewRouter()
	editRouter.Post("/hardware/{id}/update", h.HandleHardwareUpdate)
	ports := []string{"0", "-1", "65536", "70000", "not-a-number", ""}
	for _, port := range ports {
		t.Run("create_port_"+strings.ReplaceAll(port, "-", "minus"), func(t *testing.T) {
			form := url.Values{"name": {"New"}, "ip_address": {"192.0.2.71"}, "port": {port}}
			req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			createRouter.ServeHTTP(response, req)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "port must be an integer from 1 to 65535") && !strings.Contains(response.Body.String(), "port is required") {
				t.Fatalf("create port %q = %d: %s", port, response.Code, response.Body.String())
			}
		})
		t.Run("edit_port_"+strings.ReplaceAll(port, "-", "minus"), func(t *testing.T) {
			form := url.Values{"name": {"Existing"}, "ip_address": {"192.0.2.70"}, "port": {port}}
			req := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.FormatInt(controller.ID, 10)+"/update", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			editRouter.ServeHTTP(response, req)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "port must be an integer from 1 to 65535") && !strings.Contains(response.Body.String(), "port is required") {
				t.Fatalf("edit port %q = %d: %s", port, response.Code, response.Body.String())
			}
		})
	}
	controllers, err := store.GetControllers(ctx)
	if err != nil || len(controllers) != 1 || controllers[0].ID != controller.ID || controllers[0].IpAddress != "192.0.2.70" {
		t.Fatalf("invalid port requests changed controller records: %#v err=%v", controllers, err)
	}
}

func TestControllerEditFormAndActionableAddressValidation(t *testing.T) {
	h, store, conn := setupHardwareHandler(t)
	defer conn.Close()
	ctx := context.Background()
	controller, err := store.CreateController(ctx, db.CreateControllerParams{
		Name: "Existing", IpAddress: "wled.home", Port: sql.NullInt64{Int64: 8080, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	getRouter := chi.NewRouter()
	getRouter.Get("/hardware/{id}/edit", h.HandleHardwareEdit)
	getRequest := httptest.NewRequest(http.MethodGet, "/hardware/"+strconv.FormatInt(controller.ID, 10)+"/edit", nil)
	getResponse := httptest.NewRecorder()
	getRouter.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), "value=\"wled.home\"") || !strings.Contains(getResponse.Body.String(), "value=\"8080\"") {
		t.Fatalf("edit form did not display current endpoint settings: %d %s", getResponse.Code, getResponse.Body.String())
	}

	updateRouter := chi.NewRouter()
	updateRouter.Post("/hardware/{id}/update", h.HandleHardwareUpdate)
	form := url.Values{"name": {"Existing"}, "ip_address": {"http://user:pass@wled.home/path"}, "port": {"8080"}}
	request := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.FormatInt(controller.ID, 10)+"/update", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	updateRouter.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "without a URL scheme, path, credentials, or port") {
		t.Fatalf("invalid address response = %d: %s", response.Code, response.Body.String())
	}
	stored, err := store.GetController(ctx, controller.ID)
	if err != nil || stored.IpAddress != "wled.home" || stored.Port.Int64 != 8080 {
		t.Fatalf("invalid address changed stored endpoint: %+v err=%v", stored, err)
	}
}
