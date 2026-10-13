package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// openTestDB opens an in-memory database with Foreign Keys enabled.
func openTestDB(t *testing.T) *sql.DB {
	dsn := "file::memory:?cache=shared&_foreign_keys=on"
	dbConn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	return dbConn
}

// setupTestSchema applies migrations using db.Migrate
func setupTestSchema(t *testing.T, dbConn *sql.DB) {
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
}

func TestControllerDeleteCascadesToBins(t *testing.T) {
	// Setup Environment
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	uiError := uierror.New(logger)
	wClient := wled.NewClient()
	wService := wled.NewService(s, wClient, logger)
	hwService := hardware.NewService(s, wClient, logger)
	settService := settings.NewService(s)

	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		UIError:  uiError,
		Hardware: hwService,
		Settings: settService,
		WLED:     wService,
	}

	ctx := context.Background()

	// Create Controller
	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{
		Name:      "TestController",
		IpAddress: "192.168.1.100",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to create controller: %v", err)
	}

	// Create Container
	cont, err := s.CreateContainer(ctx, db.CreateContainerParams{
		Name: "C1", ControllerID: ctrl.ID, SegmentID: 0,
	})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create Bins (Simulate an 8-LED strip)
	for i := 0; i < 8; i++ {
		_, err := s.CreateBin(ctx, db.CreateBinParams{
			Name:        "Bin-" + strconv.Itoa(i),
			ContainerID: sql.NullInt64{Int64: cont, Valid: true},
			LedIndex:    sql.NullInt64{Int64: int64(i), Valid: true},
			Width:       sql.NullInt64{Int64: 1, Valid: true},
			GridX:       sql.NullInt64{Int64: int64(i), Valid: true},
			GridY:       sql.NullInt64{Int64: 0, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin %d: %v", i, err)
		}
	}

	// Verify Bins exist
	binsBefore, err := s.GetBinsByContainer(ctx, sql.NullInt64{Int64: cont, Valid: true})
	if err != nil {
		t.Fatalf("failed to fetch bins: %v", err)
	}
	if len(binsBefore) != 8 {
		t.Fatalf("expected 8 bins, got %d", len(binsBefore))
	}

	// Delete Controller via HTTP Handler
	// This exercises the `HandleHardwareDelete` method, ensuring the transaction logic works
	r := chi.NewRouter()
	r.Post("/hardware/{id}/delete", h.HandleHardwareDelete)

	target := "/hardware/" + strconv.Itoa(int(ctrl.ID)) + "/delete"
	req := httptest.NewRequest(http.MethodPost, target, nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	// Check Handler Response
	if rr.Code != http.StatusSeeOther {
		t.Errorf("Handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	// Assertions

	// Check Bins by Container (Should be 0)
	binsAfter, err := s.GetBinsByContainer(ctx, sql.NullInt64{Int64: cont, Valid: true})
	if err != nil {
		t.Fatalf("failed to fetch bins after delete: %v", err)
	}
	if len(binsAfter) != 0 {
		t.Errorf("expected 0 bins for controller, got %d", len(binsAfter))
	}

	// Check Global Bin Count (Should be 0 - ensuring no orphans/ghosts)
	// This confirms that bins were DELETED, not just set to NULL
	var count int
	err = dbConn.QueryRow("SELECT count(*) FROM bins").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count bins: %v", err)
	}
	if count != 0 {
		t.Errorf("Ghost Bins Detected! Expected 0 total bins, got %d. They may have been orphaned.", count)
	}
}

// setupHardwareHandler builds a Handler wired with a hardware service for grid
// save tests.
func setupHardwareHandler(t *testing.T) (*Handler, db.Store, *sql.DB) {
	t.Helper()
	dbConn := openTestDB(t)
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	uiError := uierror.New(logger)
	wClient := wled.NewClient()
	hwService := hardware.NewService(s, wClient, logger)

	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		UIError:  uiError,
		Hardware: hwService,
	}
	return h, s, dbConn
}

// postGridSave submits a grid save request and returns the response recorder.
func postGridSave(t *testing.T, h *Handler, controllerID int64, gridData, configData string) *httptest.ResponseRecorder {
	t.Helper()
	return postGridSaveWithSpace(t, h, controllerID, gridData, configData, "")
}

// postGridSaveWithSpace submits a grid save tagged with the coordinate space the
// payload was authored in (empty omits the field).
func postGridSaveWithSpace(t *testing.T, h *Handler, controllerID int64, gridData, configData, space string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/hardware/{id}/grid", h.HandleHardwareGridSave)

	form := url.Values{}
	form.Add("grid_data", gridData)
	form.Add("config_data", configData)
	if space != "" {
		form.Add("bin_index_space", space)
	}

	req := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.Itoa(int(controllerID))+"/grid", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// TestHandleHardwareGridSave_InvalidAllocationReturns400 verifies that invalid
// drawer allocations or bin mappings are reported as a client error (400).
func TestHandleHardwareGridSave_InvalidAllocationReturns400(t *testing.T) {
	h, s, dbConn := setupHardwareHandler(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	// Overlapping drawer allocations are invalid.
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}},{"id":null,"name":"B","segment_id":0,"led_start":5,"led_count":10,"config":{"type":"linear","total":10}}]`
	rr := postGridSave(t, h, ctrl.ID, `[]`, configData)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid allocation, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleHardwareGridSave_InvalidBinMappingReturns400 verifies that a bin
// outside its drawer's allocation is reported as a client error (400).
func TestHandleHardwareGridSave_InvalidBinMappingReturns400(t *testing.T) {
	h, s, dbConn := setupHardwareHandler(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":15,"width":1,"name":"bad"}]`
	rr := postGridSave(t, h, ctrl.ID, gridData, configData)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid bin mapping, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleHardwareGridSave_ValidReturnsSeeOther verifies that a valid
// submission still redirects.
func TestHandleHardwareGridSave_ValidReturnsSeeOther(t *testing.T) {
	h, s, dbConn := setupHardwareHandler(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"a1"}]`
	rr := postGridSave(t, h, ctrl.ID, gridData, configData)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 for valid grid, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleHardwareGridSave_InternalErrorReturns500 verifies that a genuine
// internal failure is still reported as 500, not 400.
func TestHandleHardwareGridSave_InternalErrorReturns500(t *testing.T) {
	h, s, dbConn := setupHardwareHandler(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	rr := postGridSave(t, h, ctrl.ID, `not json`, `[]`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for malformed payload, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleHardwareGridSave_DrawerSpaceSucceeds verifies that a drawer-relative
// database accepts a drawer-relative grid save (redirecting on success).
func TestHandleHardwareGridSave_DrawerSpaceSucceeds(t *testing.T) {
	h, s, dbConn := setupHardwareHandler(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	if err := ledspace.Set(ctx, s, ledspace.Drawer); err != nil {
		t.Fatalf("set space: %v", err)
	}

	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":3,"width":1,"name":"a1"}]`
	rr := postGridSave(t, h, ctrl.ID, gridData, configData)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 for drawer-space save, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleHardwareGridSave_UnresolvedSpaceReturns400 verifies that a grid save
// into an unresolved database is reported as a client error (400), not a 500,
// and leaves the database unchanged.
func TestHandleHardwareGridSave_UnresolvedSpaceReturns400(t *testing.T) {
	h, s, dbConn := setupHardwareHandler(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	if err := ledspace.Set(ctx, s, ledspace.Unresolved); err != nil {
		t.Fatalf("set space: %v", err)
	}

	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"a1"}]`
	rr := postGridSave(t, h, ctrl.ID, gridData, configData)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unresolved space, got %d: %s", rr.Code, rr.Body.String())
	}

	// The rejection must leave the database unchanged.
	containers, _ := s.GetContainersByController(ctx, ctrl.ID)
	if len(containers) != 0 {
		t.Fatalf("rejected save modified the database: %+v", containers)
	}
}

func TestHardwareAuditLogging(t *testing.T) {
	// Setup
	dbConn := openTestDB(t)
	defer dbConn.Close()
	setupTestSchema(t, dbConn)

	s := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	session := scs.New()
	uiError := uierror.New(logger)
	wClient := wled.NewClient()
	wService := wled.NewService(s, wClient, logger)
	hwService := hardware.NewService(s, wClient, logger)
	settService := settings.NewService(s)

	h := &Handler{
		Logger:   logger,
		Queries:  s,
		Database: dbConn,
		Session:  session,
		UIError:  uiError,
		Hardware: hwService,
		Settings: settService,
		WLED:     wService,
	}

	// Mock Admin Context
	s.CreateUser(context.Background(), db.CreateUserParams{Email: "admin@test.com", Role: "admin"})
	ctx := context.WithValue(context.Background(), middleware.UserContextKey, int64(1))

	// Create Controller
	r := chi.NewRouter()
	r.Post("/hardware", h.HandleHardwareCreate)

	form := url.Values{}
	form.Add("name", "Audit Ctrl")
	form.Add("ip_address", "10.0.0.1")
	form.Add("port", "80")

	req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Verify Create Log
	logs, _ := s.GetAllAuditLogs(ctx)
	if len(logs) != 1 {
		t.Fatalf("expected 1 log after create, got %d", len(logs))
	}
	createLog := logs[0]
	var createNew map[string]any
	json.Unmarshal(createLog.NewValue, &createNew)
	if createNew["name"] != "Audit Ctrl" || createNew["ip_address"] != "10.0.0.1" {
		t.Errorf("expected summary in create log, got %s", string(createLog.NewValue))
	}

	// Update Grid (Grid Save)
	ctrl, _ := s.GetControllers(ctx)
	id := ctrl[0].ID

	// Fetch the default container created during controller creation
	containers, _ := s.GetContainersByController(ctx, id)
	contID := containers[0].ID

	r2 := chi.NewRouter()
	r2.Post("/hardware/{id}/grid", h.HandleHardwareGridSave)

	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":0,"name":"A1"}]`
	configData := fmt.Sprintf(`[{"id":%d,"name":"Audit Ctrl (Main)","segment_id":0,"config":{"type":"grid","rows":1,"cols":1}}]`, contID)
	form2 := url.Values{}
	form2.Add("grid_data", gridData)
	form2.Add("config_data", configData)

	req2 := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.Itoa(int(id))+"/grid", strings.NewReader(form2.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2 = req2.WithContext(ctx)
	rr2 := httptest.NewRecorder()
	r2.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusSeeOther {
		t.Fatalf("Grid save failed: %d - %s", rr2.Code, rr2.Body.String())
	}

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 2 {
		t.Fatalf("expected 2 logs after grid update, got %d", len(logs))
	}
	gridLog := logs[1]

	if len(gridLog.NewValue) < 5 { // Check if empty
		t.Errorf("expected rich log for grid update, got empty")
	}

	// Delete Controller
	r3 := chi.NewRouter()
	r3.Post("/hardware/{id}/delete", h.HandleHardwareDelete)
	req3 := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.Itoa(int(id))+"/delete", nil)
	req3 = req3.WithContext(ctx)
	rr3 := httptest.NewRecorder()
	r3.ServeHTTP(rr3, req3)

	logs, _ = s.GetAllAuditLogs(ctx)
	if len(logs) != 3 {
		t.Fatalf("expected 3 logs after delete, got %d", len(logs))
	}
	deleteLog := logs[2]
	var deleteOld map[string]any
	json.Unmarshal(deleteLog.OldValue, &deleteOld)
	if deleteOld["name"] != "Audit Ctrl" {
		t.Errorf("expected summary in delete log, got %s", string(deleteLog.OldValue))
	}
}

// TestHandleHardwareGridSave_StaleSpaceAfterConversionReturns400 reproduces the
// cross-coordinate-space race: a grid page loaded in segment mode is replayed
// after an administrator conversion. The stale, segment-tagged payload must be
// refused rather than interpreted as drawer-relative.
func TestHandleHardwareGridSave_StaleSpaceAfterConversionReturns400(t *testing.T) {
	h, s, dbConn := setupHardwareHandler(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":3,"width":1,"name":"a1"}]`

	// The page was loaded (and saved) in segment mode.
	if rr := postGridSaveWithSpace(t, h, ctrl.ID, gridData, configData, ledspace.Segment); rr.Code != http.StatusSeeOther {
		t.Fatalf("segment save = %d, want 303: %s", rr.Code, rr.Body.String())
	}

	// An administrator converts the database to drawer-relative.
	if _, err := hardware.ConvertToDrawerRelative(ctx, s, nil); err != nil {
		t.Fatalf("convert: %v", err)
	}
	if space, _ := ledspace.Current(ctx, s); space != ledspace.Drawer {
		t.Fatalf("space = %q, want drawer", space)
	}

	// Replaying the stale segment-tagged payload is refused, and the stored state is
	// unchanged (still the drawer-relative index from the conversion).
	rr := postGridSaveWithSpace(t, h, ctrl.ID, gridData, configData, ledspace.Segment)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("stale segment payload = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	containers, _ := s.GetContainersByController(ctx, ctrl.ID)
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	bins, _ := s.GetBinsByContainer(ctx, sql.NullInt64{Int64: containers[0].ID, Valid: true})
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 3 {
		t.Fatalf("stale save changed bins: %+v", bins)
	}
}

// TestHandleHardwareGridSave_MatchingSpaceTagSucceeds verifies the guard does not
// block a correctly-tagged save.
func TestHandleHardwareGridSave_MatchingSpaceTagSucceeds(t *testing.T) {
	h, s, dbConn := setupHardwareHandler(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":3,"width":1,"name":"a1"}]`
	if rr := postGridSaveWithSpace(t, h, ctrl.ID, gridData, configData, ledspace.Segment); rr.Code != http.StatusSeeOther {
		t.Fatalf("matching segment tag = %d, want 303: %s", rr.Code, rr.Body.String())
	}
}

func TestHardwareImportRejectsOversizedBodyBeforeParsing(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/hardware/import", nil)
	req.ContentLength = config.MaxUploadSizeImport + 1
	rec := httptest.NewRecorder()
	h.HandleHardwareImport(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestLegacyHardwareConfigurationHandlersRejectDurableLocateOwnership(t *testing.T) {
	for _, state := range []string{"active", "partial"} {
		for _, action := range []string{"controller create", "controller delete", "legacy grid save", "legacy config import"} {
			t.Run(state+"/"+action, func(t *testing.T) {
				h, store, conn := setupHardwareHandler(t)
				defer conn.Close()
				ctx := context.Background()
				controller, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Existing", IpAddress: "192.0.2.50"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(ctx, `INSERT INTO physical_locate_sessions
					(controller_id,controller_address,controller_port,session_id,drawer_id,bin_id,owner_id,state,ranges_json,commands_json,fingerprint,color,brightness,last_operation,last_operation_at,last_successful_operation,last_successful_at,last_error)
					VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, 'activation',CURRENT_TIMESTAMP,'activation',CURRENT_TIMESTAMP,'cleanup unresolved')`,
					controller.ID, "192.0.2.50", 80, "handler-"+state, 1, 1, 7, state,
					`[{"start":1,"count":5}]`, `[{"segment_id":0,"start":0,"stop":5}]`, "fingerprint", "#0000ff", 128); err != nil {
					t.Fatal(err)
				}
				if _, err := conn.ExecContext(ctx, `INSERT INTO physical_locate_session_targets
					(session_id,controller_id,controller_address,controller_port,ranges_json,commands_json,fingerprint,may_have_received,last_error)
					VALUES (?,?,?,?,?,?,?,1,'cleanup unresolved')`, "handler-"+state, controller.ID, "192.0.2.50", 80,
					`[{"start":1,"count":5}]`, `[{"segment_id":0,"start":0,"stop":5}]`, "fingerprint"); err != nil {
					t.Fatal(err)
				}

				var response *httptest.ResponseRecorder
				switch action {
				case "controller create":
					router := chi.NewRouter()
					router.Post("/hardware", h.HandleHardwareCreate)
					form := url.Values{"name": {"New"}, "ip_address": {"192.0.2.51"}, "port": {"80"}}
					req := httptest.NewRequest(http.MethodPost, "/hardware", strings.NewReader(form.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					response = httptest.NewRecorder()
					router.ServeHTTP(response, req)
				case "controller delete":
					router := chi.NewRouter()
					router.Post("/hardware/{id}/delete", h.HandleHardwareDelete)
					req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/hardware/%d/delete", controller.ID), nil)
					response = httptest.NewRecorder()
					router.ServeHTTP(response, req)
				case "legacy grid save":
					response = postGridSave(t, h, controller.ID,
						`[{"container_index":0,"x":0,"y":0,"led_index":2,"width":1,"name":"new bin"}]`,
						`[{"id":null,"name":"replacement","segment_id":0,"led_start":0,"led_count":20,"config":{"type":"grid","rows":4,"cols":5}}]`)
				case "legacy config import":
					router := chi.NewRouter()
					router.Post("/hardware/import", h.HandleHardwareImport)
					var body bytes.Buffer
					writer := multipart.NewWriter(&body)
					part, err := writer.CreateFormFile("config_file", "hardware.json")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := io.WriteString(part, `{"version":"1.0","controller":{"name":"Imported","ip_address":"192.0.2.52","port":80},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":20,"config":{"type":"linear","total":20}}],"bins":[{"container_index":0,"led_index":2,"width":1,"name":"imported bin"}]}`); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					req := httptest.NewRequest(http.MethodPost, "/hardware/import", &body)
					req.Header.Set("Content-Type", writer.FormDataContentType())
					response = httptest.NewRecorder()
					router.ServeHTTP(response, req)
				}
				if response.Code != http.StatusConflict {
					t.Fatalf("%s during %s Locate = %d, want 409: %s", action, state, response.Code, response.Body.String())
				}

				var controllers, containers, bins int
				if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM controllers").Scan(&controllers); err != nil {
					t.Fatal(err)
				}
				if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM containers").Scan(&containers); err != nil {
					t.Fatal(err)
				}
				if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM bins").Scan(&bins); err != nil {
					t.Fatal(err)
				}
				if controllers != 1 || containers != 0 || bins != 0 {
					t.Fatalf("rejected %s mutated DB: controllers=%d containers=%d bins=%d", action, controllers, containers, bins)
				}
			})
		}
	}
}
