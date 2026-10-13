package router

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestPhysicalBinCreateMoveAndDetachRequireEditorRoleAndCSRF(t *testing.T) {
	env := newAuthTestEnv(t)
	defer env.conn.Close()
	env.createUser(t, "viewer-physical@test.com", "viewer")
	env.createUser(t, "editor-physical@test.com", "editor")
	env.createUser(t, "admin-physical@test.com", "admin")

	ctx := context.Background()
	cabinetID, err := env.store.CreateCabinet(ctx, db.CreateCabinetParams{Name: "Physical cabinet", CoordinateSystem: "grid"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.UpdateCabinetLayoutSettings(ctx, db.UpdateCabinetLayoutSettingsParams{
		ID: cabinetID, Name: "Physical cabinet", CoordinateSystem: "grid", LayoutWidth: 40, LayoutHeight: 20, LayoutDecorationsJson: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	drawerOne, err := env.store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer One", Width: 12, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	drawerTwo, err := env.store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: cabinetID, Label: "Drawer Two", X: 20, Width: 12, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	binID, err := env.store.CreateBin(ctx, db.CreateBinParams{Name: "Stocked bin", Width: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{
		BinID: binID, DrawerID: drawerOne, X: 1, Y: 1, Width: 2, Height: 2,
	}); err != nil {
		t.Fatal(err)
	}
	partID, err := env.store.CreatePart(ctx, db.CreatePartParams{Name: "Stocked physical part"})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}, Quantity: 8,
	}); err != nil {
		t.Fatal(err)
	}

	createPath := fmt.Sprintf("/storage/cabinets/%d/drawers/%d/bins", cabinetID, drawerOne)
	movePath := fmt.Sprintf("/storage/cabinets/%d/bins/%d/move", cabinetID, binID)
	detachPath := fmt.Sprintf("/storage/cabinets/%d/bins/%d/detach", cabinetID, binID)
	editPath := fmt.Sprintf("/storage/cabinets/%d/edit", cabinetID)

	viewerCookies := env.login(t, "viewer-physical@test.com")
	viewerToken := env.csrfToken(t, viewerCookies)
	viewerGet := httptest.NewRecorder()
	viewerGetRequest := httptest.NewRequest(http.MethodGet, editPath, nil)
	for _, cookie := range viewerCookies {
		viewerGetRequest.AddCookie(cookie)
	}
	env.router.ServeHTTP(viewerGet, viewerGetRequest)
	if viewerGet.Code != http.StatusForbidden {
		t.Errorf("viewer GET layout editor = %d, want 403", viewerGet.Code)
	}
	for _, path := range []string{editPath, createPath, movePath, detachPath} {
		if response := env.post(path, viewerCookies); response.Code != http.StatusForbidden {
			t.Errorf("viewer POST %s = %d, want 403", path, response.Code)
		}
		if response := postPhysicalForm(env, path, url.Values{"csrf_token": {viewerToken}}, viewerCookies, viewerToken); response.Code != http.StatusForbidden {
			t.Errorf("viewer CSRF POST %s = %d, want 403", path, response.Code)
		}
	}

	editorCookies := env.login(t, "editor-physical@test.com")
	editorToken := env.csrfToken(t, editorCookies)
	editorGet := httptest.NewRecorder()
	editorGetRequest := httptest.NewRequest(http.MethodGet, editPath, nil)
	for _, cookie := range editorCookies {
		editorGetRequest.AddCookie(cookie)
	}
	env.router.ServeHTTP(editorGet, editorGetRequest)
	if editorGet.Code != http.StatusOK {
		t.Fatalf("editor GET layout editor = %d, want 200", editorGet.Code)
	}
	if response := env.post(createPath, editorCookies); response.Code != http.StatusForbidden {
		t.Fatalf("editor create without CSRF = %d, want 403", response.Code)
	}
	if response := postPhysicalForm(env, createPath, url.Values{"name": {"New bin"}}, editorCookies, "invalid-token"); response.Code != http.StatusForbidden {
		t.Fatalf("editor create with invalid CSRF = %d, want 403", response.Code)
	}
	if response := postPhysicalForm(env, createPath, url.Values{
		"csrf_token": {editorToken}, "revision": {"0"}, "name": {"Created in drawer"},
		"x": {"5"}, "y": {"1"}, "width": {"2"}, "height": {"2"},
	}, editorCookies, editorToken); response.Code != http.StatusSeeOther {
		t.Fatalf("editor create with valid CSRF = %d: %s", response.Code, response.Body.String())
	}
	created, err := env.store.GetBin(ctx, 2)
	if err != nil || created.Name != "Created in drawer" || created.ContainerID.Valid || created.LedIndex.Valid || created.Width.Valid || created.GridX.Valid || created.GridY.Valid {
		t.Fatalf("created physical bin should retain null electrical associations: %+v err=%v", created, err)
	}
	if rows, err := env.store.GetBinsByContainer(ctx, sql.NullInt64{}); err != nil || len(rows) != 0 {
		t.Fatalf("physical bins must not be created through an electrical container: %+v err=%v", rows, err)
	}

	if response := env.post(movePath, editorCookies); response.Code != http.StatusForbidden {
		t.Fatalf("editor move without CSRF = %d, want 403", response.Code)
	}
	if response := postPhysicalForm(env, movePath, url.Values{
		"csrf_token": {editorToken}, "revision": {"1"}, "target_drawer_id": {strconv.FormatInt(drawerTwo, 10)}, "x": {"1"}, "y": {"1"},
	}, editorCookies, "invalid-token"); response.Code != http.StatusForbidden {
		t.Fatalf("editor move with invalid CSRF = %d, want 403", response.Code)
	}
	if response := postPhysicalForm(env, movePath, url.Values{
		"csrf_token": {editorToken}, "revision": {"1"}, "target_drawer_id": {strconv.FormatInt(drawerTwo, 10)}, "x": {"1"}, "y": {"1"},
	}, editorCookies, editorToken); response.Code != http.StatusSeeOther {
		t.Fatalf("editor move with valid CSRF = %d: %s", response.Code, response.Body.String())
	}
	moved, err := env.store.GetBinDrawerAttachment(ctx, binID)
	if err != nil || moved.BinID != binID || moved.DrawerID != drawerTwo {
		t.Fatalf("move did not retain stable bin identity: %+v err=%v", moved, err)
	}
	assignments, err := env.store.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 1 || assignments[0].BinID.Int64 != binID || assignments[0].Quantity != 8 {
		t.Fatalf("move changed stock assignment: %+v err=%v", assignments, err)
	}

	if response := env.post(detachPath, editorCookies); response.Code != http.StatusForbidden {
		t.Fatalf("editor detach without CSRF = %d, want 403", response.Code)
	}
	if response := postPhysicalForm(env, detachPath, url.Values{"csrf_token": {editorToken}, "revision": {"2"}}, editorCookies, editorToken); response.Code != http.StatusSeeOther {
		t.Fatalf("editor detach with valid CSRF = %d: %s", response.Code, response.Body.String())
	}
	if _, err := env.store.GetBinDrawerAttachment(ctx, binID); err != sql.ErrNoRows {
		t.Fatalf("detach should remove only the placement: %v", err)
	}
	bin, err := env.store.GetBin(ctx, binID)
	if err != nil || bin.ID != binID || bin.Name != "Stocked bin" {
		t.Fatalf("detach deleted or replaced inventory bin: %+v err=%v", bin, err)
	}
	assignments, err = env.store.GetPartAssignments(ctx, partID)
	if err != nil || len(assignments) != 1 || assignments[0].Quantity != 8 {
		t.Fatalf("detach changed assigned stock: %+v err=%v", assignments, err)
	}

	adminCookies := env.login(t, "admin-physical@test.com")
	adminToken := env.csrfToken(t, adminCookies)
	adminCreatePath := fmt.Sprintf("/storage/cabinets/%d/drawers/%d/bins", cabinetID, drawerTwo)
	if response := postPhysicalForm(env, adminCreatePath, url.Values{
		"csrf_token": {adminToken}, "revision": {"3"}, "name": {"Admin bin"},
		"x": {"1"}, "y": {"1"}, "width": {"2"}, "height": {"2"},
	}, adminCookies, adminToken); response.Code != http.StatusSeeOther {
		t.Fatalf("admin physical bin create = %d: %s", response.Code, response.Body.String())
	}
	if env.wled.total() != 0 {
		t.Fatalf("physical bin operations issued %d WLED calls", env.wled.total())
	}
}

func postPhysicalForm(env *authTestEnv, path string, form url.Values, cookies []*http.Cookie, csrfToken string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", csrfToken)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	env.router.ServeHTTP(response, request)
	return response
}
