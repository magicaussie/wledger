package handler

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

type convFixture struct {
	store  db.Store
	dbConn *sql.DB
	router http.Handler
}

func adminCtx() context.Context {
	return auth.WithUser(context.Background(), auth.User{ID: 1, Email: "admin@test", Role: "admin"})
}

func viewerCtx() context.Context {
	return auth.WithUser(context.Background(), auth.User{ID: 2, Email: "viewer@test", Role: "viewer"})
}

func setupConversionHandler(t *testing.T) *convFixture {
	t.Helper()
	dir := t.TempDir()
	dbConn, err := db.Open("file:" + dir + "/conversion.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sm := scs.New()
	h := &Handler{
		Logger:   logger,
		Queries:  store,
		Database: dbConn,
		Session:  sm,
		UIError:  uierror.New(logger),
		Hardware: hardware.NewService(store, wled.NewClient(), logger),
		WLED:     wled.NewService(store, wled.NewClient(), logger),
	}
	r := chi.NewRouter()
	r.Use(sm.LoadAndSave)
	r.Get("/hardware/conversion", h.HandleConversionPreview)
	r.Post("/hardware/conversion", h.HandleConversionConfirm)
	r.Post("/hardware/{id}/grid", h.HandleHardwareGridSave)
	return &convFixture{store: store, dbConn: dbConn, router: r}
}

func (f *convFixture) get(ctx context.Context) (*httptest.ResponseRecorder, []*http.Cookie) {
	req := httptest.NewRequest(http.MethodGet, "/hardware/conversion", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr, rr.Result().Cookies()
}

func (f *convFixture) post(ctx context.Context, form url.Values, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/hardware/conversion", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

func (f *convFixture) postGrid(ctx context.Context, controllerID int64, gridData, configData string) *httptest.ResponseRecorder {
	form := url.Values{}
	form.Add("grid_data", gridData)
	form.Add("config_data", configData)
	req := httptest.NewRequest(http.MethodPost, "/hardware/"+strconv.FormatInt(controllerID, 10)+"/grid", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	f.router.ServeHTTP(rr, req)
	return rr
}

var (
	reCSRF        = regexp.MustCompile(`data-csrf="([^"]+)"`)
	reFingerprint = regexp.MustCompile(`data-fingerprint="([^"]+)"`)
)

func extractField(t *testing.T, body string, re *regexp.Regexp, name string) string {
	t.Helper()
	m := re.FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("could not extract %s from preview body", name)
	}
	return m[1]
}

// seedSegmentScenario creates a controller with one allocated drawer and one
// mapped bin in segment-relative coordinates.
func seedSegmentScenario(t *testing.T, f *convFixture) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	ctrl, err := f.store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	cont, err := f.store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "A", ControllerID: ctrl.ID, SegmentID: 0, LedStart: 0, LedCount: 10,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	binID, err := f.store.CreateBin(ctx, db.CreateBinParams{
		Name: "a1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 3, Valid: true},
		Width: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin: %v", err)
	}
	return cont, binID
}

func TestConversionPreview_AdminOnly(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()

	if rr, _ := f.get(viewerCtx()); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer preview = %d, want 403", rr.Code)
	}
	if rr, _ := f.get(adminCtx()); rr.Code != http.StatusOK {
		t.Fatalf("admin preview = %d, want 200", rr.Code)
	}
}

func TestConversionConfirm_AdminOnly(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()

	form := url.Values{"confirm": {"confirm"}, "csrf_token": {"x"}}
	if rr := f.post(viewerCtx(), form, nil); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer confirm = %d, want 403", rr.Code)
	}
}

func TestConversionConfirm_CSRFRejected(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()

	rr, cookies := f.get(adminCtx())
	body := rr.Body.String()
	tok := extractField(t, body, reCSRF, "csrf_token")
	fp := extractField(t, body, reFingerprint, "fingerprint")

	// No token.
	if rr := f.post(adminCtx(), url.Values{"confirm": {"confirm"}, "fingerprint": {fp}}, cookies); rr.Code != http.StatusForbidden {
		t.Fatalf("missing csrf = %d, want 403", rr.Code)
	}
	// Wrong token.
	form := url.Values{"confirm": {"confirm"}, "fingerprint": {fp}, "csrf_token": {"not-the-token"}}
	if rr := f.post(adminCtx(), form, cookies); rr.Code != http.StatusForbidden {
		t.Fatalf("wrong csrf = %d, want 403", rr.Code)
	}
	// Token without the explicit confirmation field.
	form = url.Values{"fingerprint": {fp}, "csrf_token": {tok}}
	if rr := f.post(adminCtx(), form, cookies); rr.Code != http.StatusBadRequest {
		t.Fatalf("missing confirmation = %d, want 400", rr.Code)
	}
}

func TestConversionConfirm_Success(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()
	cont, binID := seedSegmentScenario(t, f)

	rr, cookies := f.get(adminCtx())
	body := rr.Body.String()
	tok := extractField(t, body, reCSRF, "csrf_token")
	fp := extractField(t, body, reFingerprint, "fingerprint")

	form := url.Values{"confirm": {"confirm"}, "fingerprint": {fp}, "csrf_token": {tok}}
	post := f.post(adminCtx(), form, cookies)
	if post.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d, want 303: %s", post.Code, post.Body.String())
	}
	if loc := post.Header().Get("Location"); !strings.Contains(loc, "result=converted") {
		t.Fatalf("redirect = %q, want result=converted", loc)
	}

	space, _ := ledspace.Current(ctx, f.store)
	if space != ledspace.Drawer {
		t.Fatalf("space = %q, want drawer", space)
	}
	bins, _ := f.store.GetBinsByContainer(ctx, cont)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 3 {
		t.Fatalf("bin after conversion = %+v, want index 3 (drawer-relative)", bins)
	}
	_ = binID
}

func TestConversionConfirm_StaleRejected(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()
	cont, binID := seedSegmentScenario(t, f)

	rr, cookies := f.get(adminCtx())
	tok := extractField(t, rr.Body.String(), reCSRF, "csrf_token")
	fp := extractField(t, rr.Body.String(), reFingerprint, "fingerprint")

	// Change the relevant database state after the preview was taken.
	if err := f.store.UpdateBinLedIndex(ctx, db.UpdateBinLedIndexParams{
		LedIndex: sql.NullInt64{Int64: 4, Valid: true}, ID: binID,
	}); err != nil {
		t.Fatalf("mutate bin: %v", err)
	}

	form := url.Values{"confirm": {"confirm"}, "fingerprint": {fp}, "csrf_token": {tok}}
	post := f.post(adminCtx(), form, cookies)
	if post.Code != http.StatusSeeOther {
		t.Fatalf("stale confirm = %d, want 303", post.Code)
	}
	if loc := post.Header().Get("Location"); !strings.Contains(loc, "result=stale") {
		t.Fatalf("redirect = %q, want result=stale", loc)
	}

	space, _ := ledspace.Current(ctx, f.store)
	if space != ledspace.Segment {
		t.Fatalf("stale confirmation changed space to %q", space)
	}
	bins, _ := f.store.GetBinsByContainer(ctx, cont)
	if len(bins) != 1 || bins[0].LedIndex.Int64 != 4 {
		t.Fatalf("stale confirmation modified bins: %+v", bins)
	}
}

func TestConversionConfirm_RefusedWhenBlocked(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()

	ctrl, _ := f.store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont, _ := f.store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "A", ControllerID: ctrl.ID, SegmentID: 0, LedStart: 0, LedCount: 10,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	// Out-of-range bin blocks the conversion.
	if _, err := f.store.CreateBin(ctx, db.CreateBinParams{
		Name: "bad", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 15, Valid: true},
		Width: sql.NullInt64{Int64: 1, Valid: true},
	}); err != nil {
		t.Fatalf("create bin: %v", err)
	}

	rr, cookies := f.get(adminCtx())
	tok := extractField(t, rr.Body.String(), reCSRF, "csrf_token")
	fp := extractField(t, rr.Body.String(), reFingerprint, "fingerprint")

	form := url.Values{"confirm": {"confirm"}, "fingerprint": {fp}, "csrf_token": {tok}}
	post := f.post(adminCtx(), form, cookies)
	if loc := post.Header().Get("Location"); !strings.Contains(loc, "result=refused") {
		t.Fatalf("redirect = %q, want result=refused", loc)
	}
	if space, _ := ledspace.Current(ctx, f.store); space != ledspace.Segment {
		t.Fatalf("refused conversion changed space to %q", space)
	}
}

func TestConversionConfirm_AlreadyDrawer(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()
	if err := ledspace.Set(ctx, f.store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	rr, cookies := f.get(adminCtx())
	tok := extractField(t, rr.Body.String(), reCSRF, "csrf_token")
	fp := extractField(t, rr.Body.String(), reFingerprint, "fingerprint")

	form := url.Values{"confirm": {"confirm"}, "fingerprint": {fp}, "csrf_token": {tok}}
	post := f.post(adminCtx(), form, cookies)
	if loc := post.Header().Get("Location"); !strings.Contains(loc, "result=already_drawer") {
		t.Fatalf("redirect = %q, want result=already_drawer", loc)
	}
}

func TestConversionConfirm_Unresolved(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()
	if err := ledspace.Set(ctx, f.store, ledspace.Unresolved); err != nil {
		t.Fatalf("set unresolved: %v", err)
	}

	rr, cookies := f.get(adminCtx())
	tok := extractField(t, rr.Body.String(), reCSRF, "csrf_token")
	fp := extractField(t, rr.Body.String(), reFingerprint, "fingerprint")

	form := url.Values{"confirm": {"confirm"}, "fingerprint": {fp}, "csrf_token": {tok}}
	post := f.post(adminCtx(), form, cookies)
	if loc := post.Header().Get("Location"); !strings.Contains(loc, "result=unresolved") {
		t.Fatalf("redirect = %q, want result=unresolved", loc)
	}
}

// TestConversionPreview_GETNeverConverts verifies that merely loading the preview
// leaves the coordinate space untouched.
func TestConversionPreview_GETNeverConverts(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()
	seedSegmentScenario(t, f)

	if rr, _ := f.get(adminCtx()); rr.Code != http.StatusOK {
		t.Fatalf("preview = %d, want 200", rr.Code)
	}
	if space, _ := ledspace.Current(ctx, f.store); space != ledspace.Segment {
		t.Fatalf("a GET preview converted the database to %q", space)
	}
}

// TestOrdinaryGridSaveNeverConverts verifies that saving the grid does not trigger
// a coordinate conversion.
func TestOrdinaryGridSaveNeverConverts(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()

	ctrl, _ := f.store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"a1"}]`
	if rr := f.postGrid(adminCtx(), ctrl.ID, gridData, configData); rr.Code != http.StatusSeeOther {
		t.Fatalf("grid save = %d, want 303: %s", rr.Code, rr.Body.String())
	}
	if space, _ := ledspace.Current(ctx, f.store); space != ledspace.Segment {
		t.Fatalf("ordinary grid save converted the database to %q", space)
	}
}

// TestConversionConfirm_FingerprintRequired verifies that a confirmation without a
// preview fingerprint is rejected, so the stale-state guard cannot be bypassed.
func TestConversionConfirm_FingerprintRequired(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()
	seedSegmentScenario(t, f)

	rr, cookies := f.get(adminCtx())
	tok := extractField(t, rr.Body.String(), reCSRF, "csrf_token")

	form := url.Values{"confirm": {"confirm"}, "csrf_token": {tok}} // no fingerprint
	post := f.post(adminCtx(), form, cookies)
	if post.Code != http.StatusBadRequest {
		t.Fatalf("missing fingerprint = %d, want 400", post.Code)
	}
	if space, _ := ledspace.Current(ctx, f.store); space != ledspace.Segment {
		t.Fatalf("confirmation without a fingerprint converted the database to %q", space)
	}
}

// TestConversionConfirm_CrossSessionTokenRejected verifies that a CSRF token is
// only valid in the session that issued it.
func TestConversionConfirm_CrossSessionTokenRejected(t *testing.T) {
	f := setupConversionHandler(t)
	defer f.dbConn.Close()
	ctx := context.Background()
	seedSegmentScenario(t, f)

	rr, _ := f.get(adminCtx())
	tok := extractField(t, rr.Body.String(), reCSRF, "csrf_token")
	fp := extractField(t, rr.Body.String(), reFingerprint, "fingerprint")

	// Post the valid token without the issuing session's cookie (a different session).
	form := url.Values{"confirm": {"confirm"}, "fingerprint": {fp}, "csrf_token": {tok}}
	post := f.post(adminCtx(), form, nil)
	if post.Code != http.StatusForbidden {
		t.Fatalf("cross-session token = %d, want 403", post.Code)
	}
	if space, _ := ledspace.Current(ctx, f.store); space != ledspace.Segment {
		t.Fatalf("cross-session confirmation converted the database to %q", space)
	}
}
