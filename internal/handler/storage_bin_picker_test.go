package handler

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/web/components"
)

// openStorageBinPickerFixture returns a Handler over a fresh, file-backed
// SQLite database migrated to the newest schema, so placement assertions run
// against real SQLite and cannot leak rows between tests.
func openStorageBinPickerFixture(t *testing.T) (*Handler, *sql.DB) {
	t.Helper()

	dbConn, err := db.Open("file:" + filepath.Join(t.TempDir(), "storage_bin_picker.db"))
	if err != nil {
		t.Fatalf("open storage bin picker fixture database: %v", err)
	}
	t.Cleanup(func() { dbConn.Close() })

	// A PRAGMA is per connection, so one connection keeps it in force for the
	// dangling-attachment fixture.
	dbConn.SetMaxOpenConns(1)

	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("migrate storage bin picker fixture: %v", err)
	}
	if err := i18n.InitWithDir("../../locales"); err != nil {
		t.Fatalf("initialize fixture locales: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &Handler{
		Logger:   logger,
		Queries:  db.NewStore(dbConn),
		Database: dbConn,
		UIError:  uierror.New(logger),
	}, dbConn
}

// getStorageBinPicker serves the registered path through a router so the test
// proves both the route and the handler the fragment is fetched from.
func getStorageBinPicker(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/storage/bin_picker", h.HandleStorageBinPicker)

	req := httptest.NewRequest(http.MethodGet, "/storage/bin_picker", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

// placedBinFixture is one bin's optional legacy electrical association.
type placedBinFixture struct {
	name        string
	containerID sql.NullInt64
}

func createPickerBin(t *testing.T, h *Handler, bin placedBinFixture) int64 {
	t.Helper()
	id, err := h.Queries.CreateBin(context.Background(), db.CreateBinParams{
		Name:        bin.name,
		ContainerID: bin.containerID,
	})
	if err != nil {
		t.Fatalf("create bin %q: %v", bin.name, err)
	}
	return id
}

func attachPickerBin(t *testing.T, h *Handler, binID, drawerID, displayOrder int64) {
	t.Helper()
	err := h.Queries.CreateBinDrawerAttachment(context.Background(), db.CreateBinDrawerAttachmentParams{
		BinID:        binID,
		DrawerID:     drawerID,
		X:            0,
		Y:            0,
		Width:        1,
		Height:       1,
		DisplayOrder: displayOrder,
	})
	if err != nil {
		t.Fatalf("attach bin %d to drawer %d: %v", binID, drawerID, err)
	}
}

func createPickerCabinet(t *testing.T, h *Handler, name string, displayOrder int64) int64 {
	t.Helper()
	id, err := h.Queries.CreateCabinet(context.Background(), db.CreateCabinetParams{
		Name:             name,
		CoordinateSystem: "grid",
		DisplayOrder:     displayOrder,
	})
	if err != nil {
		t.Fatalf("create cabinet %q: %v", name, err)
	}
	return id
}

func createPickerDrawer(t *testing.T, h *Handler, cabinetID int64, label string, displayOrder int64) int64 {
	t.Helper()
	id, err := h.Queries.CreateDrawer(context.Background(), db.CreateDrawerParams{
		CabinetID:    cabinetID,
		Label:        label,
		DisplayOrder: displayOrder,
		Width:        10,
		Height:       5,
	})
	if err != nil {
		t.Fatalf("create drawer %q: %v", label, err)
	}
	return id
}

// indexOfBin fails the test when the picker does not offer that bin at all.
func indexOfBin(t *testing.T, body string, binID int64) int {
	t.Helper()
	marker := fmt.Sprintf(`data-bin-id="%d"`, binID)
	index := strings.Index(body, marker)
	if index < 0 {
		t.Fatalf("bin %d is not selectable in the picker fragment:\n%s", binID, body)
	}
	return index
}

func countOccurrences(body, marker string) int {
	return strings.Count(body, marker)
}

// dispatchesEvent matches an Alpine event name whether templ leaves the quotes
// literal in the attribute or HTML-escapes them.
func dispatchesEvent(body, event string) bool {
	return strings.Contains(body, "'"+event+"'") || strings.Contains(body, "&#39;"+event+"&#39;")
}

// TestStorageBinPicker_PlacesAttachedBinsAndGroupsRemainder is the core real
// SQLite contract: a NULL-container bin is placed by its attachment exactly
// like a legacy-container bin, unattached bins land in the labelled unplaced
// group in stable id order, every bin appears exactly once, and nothing is
// written.
func TestStorageBinPicker_PlacesAttachedBinsAndGroupsRemainder(t *testing.T) {
	h, dbConn := openStorageBinPickerFixture(t)
	ctx := context.Background()

	controller, err := h.Queries.CreateController(ctx, db.CreateControllerParams{Name: "Controller", IpAddress: "192.168.1.10"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	container, err := h.Queries.CreateContainer(ctx, db.CreateContainerParams{Name: "String", ControllerID: controller.ID})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}

	cabinet := createPickerCabinet(t, h, "Cabinet Alpha", 0)
	drawerOne := createPickerDrawer(t, h, cabinet, "Drawer One", 0)
	drawerTwo := createPickerDrawer(t, h, cabinet, "Drawer Two", 1)

	nullContainer := sql.NullInt64{}
	legacyContainer := sql.NullInt64{Int64: container, Valid: true}

	placedNull := createPickerBin(t, h, placedBinFixture{name: "Placed Null Container", containerID: nullContainer})
	placedLegacy := createPickerBin(t, h, placedBinFixture{name: "Placed Legacy Container", containerID: legacyContainer})
	placedSecondDrawer := createPickerBin(t, h, placedBinFixture{name: "Placed Second Drawer", containerID: legacyContainer})
	unplacedNull := createPickerBin(t, h, placedBinFixture{name: "Unplaced Null Container", containerID: nullContainer})
	unplacedLegacy := createPickerBin(t, h, placedBinFixture{name: "Unplaced Legacy Container", containerID: legacyContainer})

	attachPickerBin(t, h, placedNull, drawerOne, 0)
	attachPickerBin(t, h, placedLegacy, drawerOne, 1)
	attachPickerBin(t, h, placedSecondDrawer, drawerTwo, 0)

	rr := getStorageBinPicker(t, h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. Body: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	// Placement is read from the attachment hierarchy, never the container.
	for _, want := range []string{"Cabinet Alpha", "Drawer One", "Drawer Two"} {
		if !strings.Contains(body, want) {
			t.Errorf("picker fragment is missing physical group %q", want)
		}
	}

	unplacedIndex := strings.Index(body, `data-bin-group="unplaced"`)
	if unplacedIndex < 0 {
		t.Fatalf("picker fragment is missing the unplaced group:\n%s", body)
	}

	nullIndex := indexOfBin(t, body, placedNull)
	legacyIndex := indexOfBin(t, body, placedLegacy)
	secondDrawerIndex := indexOfBin(t, body, placedSecondDrawer)
	unplacedNullIndex := indexOfBin(t, body, unplacedNull)
	unplacedLegacyIndex := indexOfBin(t, body, unplacedLegacy)

	if nullIndex > unplacedIndex || legacyIndex > unplacedIndex {
		t.Errorf("attached bins must be grouped before the unplaced group (null=%d legacy=%d unplaced=%d)", nullIndex, legacyIndex, unplacedIndex)
	}
	if nullIndex > legacyIndex {
		t.Errorf("Drawer One bins are not in deterministic physical order (null=%d legacy=%d)", nullIndex, legacyIndex)
	}
	if legacyIndex > secondDrawerIndex {
		t.Errorf("drawers are not in display order (drawer one=%d drawer two=%d)", legacyIndex, secondDrawerIndex)
	}
	if unplacedNullIndex < unplacedIndex || unplacedLegacyIndex < unplacedIndex {
		t.Errorf("unattached bins must render inside the unplaced group (null=%d legacy=%d unplaced=%d)", unplacedNullIndex, unplacedLegacyIndex, unplacedIndex)
	}
	if unplacedNullIndex > unplacedLegacyIndex {
		t.Errorf("unplaced bins are not in stable id order (null=%d legacy=%d)", unplacedNullIndex, unplacedLegacyIndex)
	}

	// Every bin is a keyboard-reachable button carrying its stable id.
	if got := countOccurrences(body, `data-bin-id="`); got != 5 {
		t.Errorf("rendered %d selectable bins, want 5 (each bin exactly once)", got)
	}
	if got := countOccurrences(body, `<button type="button"`); got != 5 {
		t.Errorf("rendered %d bin buttons, want 5 native buttons for keyboard access", got)
	}
	if got := countOccurrences(body, "aria-pressed"); got != 5 {
		t.Errorf("rendered %d aria-pressed bindings, want 5", got)
	}

	if !strings.Contains(body, `name="bin_id"`) {
		t.Error("picker fragment is missing the hidden bin_id field the form submits")
	}
	if !strings.Contains(body, "bin-selected-form") || !dispatchesEvent(body, "bin-selected") {
		t.Error("picker fragment is missing the bin-selected / bin-selected-form event contract")
	}

	assertTableCount(t, dbConn, "bins", 5)
	assertTableCount(t, dbConn, "bin_drawer_attachments", 3)
	assertTableCount(t, dbConn, "part_assignments", 0)
	assertTableCount(t, dbConn, "drawers", 2)
}

// TestStorageBinPicker_NullContainerAttachedBinIsPlaced proves a NULL legacy
// container is irrelevant to placement, and that the unplaced group stays
// absent when nothing is unplaced.
func TestStorageBinPicker_NullContainerAttachedBinIsPlaced(t *testing.T) {
	h, _ := openStorageBinPickerFixture(t)

	cabinet := createPickerCabinet(t, h, "Cabinet Beta", 0)
	drawer := createPickerDrawer(t, h, cabinet, "Drawer Beta", 0)
	bin := createPickerBin(t, h, placedBinFixture{name: "Only Null Container Bin", containerID: sql.NullInt64{}})
	attachPickerBin(t, h, bin, drawer, 0)

	rr := getStorageBinPicker(t, h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	indexOfBin(t, body, bin)
	if !strings.Contains(body, "Cabinet Beta") || !strings.Contains(body, "Drawer Beta") {
		t.Errorf("NULL-container attached bin is not grouped under its physical cabinet/drawer:\n%s", body)
	}
	if strings.Contains(body, `data-bin-group="unplaced"`) {
		t.Error("the unplaced group must not appear when every bin has an attachment row")
	}
	if strings.Contains(body, `data-bin-empty="true"`) {
		t.Error("the empty state must not appear when a bin is selectable")
	}
}

// TestStorageBinPicker_UnplacedLegacyRowsRemainSelectable covers a database
// with no physical layout at all: legacy rows stay selectable in the labelled
// group, in stable id order, with no inferred location.
func TestStorageBinPicker_UnplacedLegacyRowsRemainSelectable(t *testing.T) {
	h, _ := openStorageBinPickerFixture(t)

	nullBin := createPickerBin(t, h, placedBinFixture{name: "Legacy Null Bin", containerID: sql.NullInt64{}})
	secondBin := createPickerBin(t, h, placedBinFixture{name: "Legacy Second Bin", containerID: sql.NullInt64{}})

	rr := getStorageBinPicker(t, h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	groupIndex := strings.Index(body, `data-bin-group="unplaced"`)
	if groupIndex < 0 {
		t.Fatalf("unplaced legacy bins need the labelled group:\n%s", body)
	}
	nullIndex := indexOfBin(t, body, nullBin)
	secondIndex := indexOfBin(t, body, secondBin)
	if nullIndex < groupIndex || secondIndex < groupIndex {
		t.Errorf("legacy bins must render inside the unplaced group (null=%d second=%d group=%d)", nullIndex, secondIndex, groupIndex)
	}
	if nullIndex > secondIndex {
		t.Errorf("unplaced bins are not in stable id order (null=%d second=%d)", nullIndex, secondIndex)
	}
	if strings.Contains(body, `data-bin-empty="true"`) {
		t.Error("the empty state must not appear while bins are selectable")
	}
	if got := countOccurrences(body, `data-bin-id="`); got != 2 {
		t.Errorf("rendered %d selectable bins, want 2", got)
	}
}

func TestStorageBinPicker_ArchivedPlacementRemainsSelectableButNotUnplaced(t *testing.T) {
	h, _ := openStorageBinPickerFixture(t)

	archivedCabinet := createPickerCabinet(t, h, "Archived Cabinet", 0)
	archivedCabinetDrawer := createPickerDrawer(t, h, archivedCabinet, "Cabinet Drawer", 0)
	activeCabinet := createPickerCabinet(t, h, "Active Cabinet", 1)
	archivedDrawer := createPickerDrawer(t, h, activeCabinet, "Archived Drawer", 0)
	cabinetBin := createPickerBin(t, h, placedBinFixture{name: "Cabinet Bin", containerID: sql.NullInt64{}})
	drawerBin := createPickerBin(t, h, placedBinFixture{name: "Drawer Bin", containerID: sql.NullInt64{}})
	unplacedBin := createPickerBin(t, h, placedBinFixture{name: "Unplaced Bin", containerID: sql.NullInt64{}})
	attachPickerBin(t, h, cabinetBin, archivedCabinetDrawer, 0)
	attachPickerBin(t, h, drawerBin, archivedDrawer, 0)

	if _, err := h.Queries.ArchiveCabinet(context.Background(), archivedCabinet); err != nil {
		t.Fatalf("archive cabinet: %v", err)
	}
	if _, err := h.Queries.ArchiveDrawer(context.Background(), archivedDrawer); err != nil {
		t.Fatalf("archive drawer: %v", err)
	}

	rr := getStorageBinPicker(t, h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	inactiveIndex := strings.Index(body, `data-bin-group="inactive-physical"`)
	if inactiveIndex < 0 {
		t.Fatalf("picker is missing inactive placement group:\n%s", body)
	}
	for _, id := range []int64{cabinetBin, drawerBin} {
		if index := indexOfBin(t, body, id); index < inactiveIndex {
			t.Errorf("archived bin %d must render in inactive group (index=%d group=%d)", id, index, inactiveIndex)
		}
	}
	unplacedGroupIndex := strings.Index(body, `data-bin-group="unplaced"`)
	if unplacedGroupIndex < 0 {
		t.Fatalf("picker is missing truly unplaced group:\n%s", body)
	}
	if index := indexOfBin(t, body, unplacedBin); index < unplacedGroupIndex {
		t.Errorf("truly unplaced bin must render in its own group (index=%d)", index)
	}
	if got := countOccurrences(body, `data-bin-id="`); got != 3 {
		t.Errorf("rendered %d selectable bins, want 3", got)
	}
	if unplacedGroupIndex < inactiveIndex {
		t.Error("unplaced bins must not be conflated with archived physical attachments")
	}
}

// TestStorageBinPicker_EmptyDatabaseRendersEmptyState keeps an empty install
// from rendering a misleading group.
func TestStorageBinPicker_EmptyDatabaseRendersEmptyState(t *testing.T) {
	h, _ := openStorageBinPickerFixture(t)

	rr := getStorageBinPicker(t, h)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-bin-empty="true"`) {
		t.Errorf("empty picker should render the empty state, got:\n%s", body)
	}
	if strings.Contains(body, `data-bin-group="unplaced"`) {
		t.Error("an empty database must not render the unplaced group")
	}
}

// TestStorageBinPicker_DanglingAttachmentIsReported requires a broken
// attachment (a row naming a bin that no longer exists) to surface as a server
// error rather than a picker that silently drops the bin.
func TestStorageBinPicker_DanglingAttachmentIsReported(t *testing.T) {
	h, dbConn := openStorageBinPickerFixture(t)

	cabinet := createPickerCabinet(t, h, "Cabinet Gamma", 0)
	drawer := createPickerDrawer(t, h, cabinet, "Drawer Gamma", 0)
	bin := createPickerBin(t, h, placedBinFixture{name: "Dangling Bin", containerID: sql.NullInt64{}})
	attachPickerBin(t, h, bin, drawer, 0)

	if _, err := dbConn.ExecContext(context.Background(), "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := dbConn.ExecContext(context.Background(), "DELETE FROM bins WHERE id = ?", bin); err != nil {
		t.Fatalf("delete attached bin: %v", err)
	}

	rr := getStorageBinPicker(t, h)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a dangling attachment. Body: %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `data-bin-group="unplaced"`) {
		t.Error("a failed read must not fall back to a partial picker")
	}
}

// pickerGlyphSlotClass is the fixed-width class list every bin button gives its
// selection-check slot. It is static in the rendered markup: only the slot's
// opacity is bound, so the slot occupies the same width in both the selected and
// the unselected state and cannot resize the button or shift a same-row sibling.
const pickerGlyphSlotClass = `class="inline-block w-4 shrink-0 text-center font-bold text-base-content opacity-0"`

// buttonForBin returns the markup of one bin's rendered <button> element.
func buttonForBin(t *testing.T, body string, binID int64) string {
	t.Helper()
	markerIndex := indexOfBin(t, body, binID)
	open := strings.LastIndex(body[:markerIndex], "<button")
	if open < 0 {
		t.Fatalf("bin %d markup is not inside a <button>:\n%s", binID, body)
	}
	close := strings.Index(body[markerIndex:], "</button>")
	if close < 0 {
		t.Fatalf("bin %d button is not closed:\n%s", binID, body)
	}
	return body[open : markerIndex+close]
}

// TestStorageBinPickerComponent_SelectionContract renders a hand-built view so
// the markup contract is proven without a database: the hidden bin_id field,
// the selection events, native buttons with stable ids and aria-pressed state,
// and group labels that separate physical placement from the unplaced
// remainder.
func TestStorageBinPickerComponent_SelectionContract(t *testing.T) {
	view := components.StorageBinPickerView{
		Cabinets: []components.StorageBinPickerCabinet{{
			ID:   7,
			Name: "Cabinet Alpha",
			Drawers: []components.StorageBinPickerDrawer{{
				ID:    11,
				Label: "Drawer One",
				Bins:  []components.StorageBinPickerBin{{ID: 3, Name: "Alpha Bin"}},
			}},
		}},
		Inactive: []components.StorageBinPickerBin{{ID: 8, Name: "Archived Bin"}},
		Unplaced: []components.StorageBinPickerBin{{ID: 9, Name: "Unplaced Bin"}},
	}

	var buf bytes.Buffer
	if err := components.StorageBinPicker(view).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render storage bin picker: %v", err)
	}
	body := buf.String()

	for _, want := range []string{
		`name="bin_id"`,
		`bin-selected-form`,
		`@click=`,
		`data-bin-id="3"`,
		`data-bin-id="8"`,
		`data-bin-id="9"`,
		`aria-pressed`,
		`focus-visible:outline-2`,
		`focus-visible:outline-primary`,
		`min-h-[44px]`,
		`max-w-full`,
		`min-w-0 truncate`,
		`transition-none`,
		`aria-[pressed=false]:hover:bg-base-300/60`,
		`data-bin-group="cabinet"`,
		`data-bin-group="drawer"`,
		`data-bin-group="inactive-physical"`,
		`data-bin-group="unplaced"`,
		pickerGlyphSlotClass,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("picker component is missing %q:\n%s", want, body)
		}
	}

	// Regression (M6 contrast/geometry): the check glyph keeps its full-width
	// slot in both DOM states and is hidden with opacity, never x-show /
	// display:none, so selecting a bin cannot resize it or move its neighbours.
	if strings.Contains(body, "x-show") || strings.Contains(body, "x-cloak") {
		t.Errorf("check glyph must reserve width with opacity, not x-show/x-cloak:\n%s", body)
	}
	if strings.Contains(body, "display:none") {
		t.Errorf("check glyph must not be removed from layout:\n%s", body)
	}
	if got := strings.Count(body, "✓"); got != 3 {
		t.Errorf("the check glyph must exist in every button even when unselected (want 3, got %d)", got)
	}
	if got := countOccurrences(body, pickerGlyphSlotClass); got != 3 {
		t.Errorf("every bin button must carry the identical fixed-width glyph slot (want 3, got %d)", got)
	}
	if !strings.Contains(body, "opacity-100") || !strings.Contains(body, "opacity-0") {
		t.Error("the glyph slot must bind both opacity states so selection only toggles opacity")
	}

	// Regression (M6 contrast): muted small text uses a token-based text color,
	// not a sub-50% opacity that fails 4.5:1 on the silk theme.
	for _, banned := range []string{"opacity-40", "opacity-50", "opacity-60", "opacity-70"} {
		if strings.Contains(body, banned) {
			t.Errorf("muted text must not use %s; use text-base-content/80 for >=4.5:1 on silk", banned)
		}
	}
	if got := strings.Count(body, "text-base-content/80"); got < 4 {
		t.Errorf("expected the muted drawer, bin-id, count and no-bins text to use text-base-content/80 (found %d)", got)
	}
	if !dispatchesEvent(body, "bin-selected") {
		t.Error("bin buttons must dispatch the bin-selected event the stock forms listen for")
	}
	if got := countOccurrences(body, `<button type="button"`); got != 3 {
		t.Errorf("rendered %d bin buttons, want 3", got)
	}
	if placedIndex, inactiveIndex, unplacedIndex := indexOfBin(t, body, 3), indexOfBin(t, body, 8), indexOfBin(t, body, 9); !(placedIndex < inactiveIndex && inactiveIndex < unplacedIndex) {
		t.Errorf("placed, inactive and unplaced bins must render in distinct groups (placed=%d inactive=%d unplaced=%d)", placedIndex, inactiveIndex, unplacedIndex)
	}
}

// TestStorageBinPickerComponent_NeighbourBinsKeepEqualWidth proves the rendered
// contract behind the M6 geometry finding: two bins side by side in one drawer
// reserve byte-identical fixed-width check slots, so toggling the selection of
// one cannot change its width or shift its same-row sibling.
func TestStorageBinPickerComponent_NeighbourBinsKeepEqualWidth(t *testing.T) {
	view := components.StorageBinPickerView{
		Cabinets: []components.StorageBinPickerCabinet{{
			ID:   1,
			Name: "Cabinet",
			Drawers: []components.StorageBinPickerDrawer{{
				ID:    2,
				Label: "Drawer",
				Bins: []components.StorageBinPickerBin{
					{ID: 10, Name: "First"},
					{ID: 11, Name: "Second"},
				},
			}},
		}},
	}

	var buf bytes.Buffer
	if err := components.StorageBinPicker(view).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render storage bin picker: %v", err)
	}
	body := buf.String()

	first := buttonForBin(t, body, 10)
	second := buttonForBin(t, body, 11)

	// The width-bearing slot markup is the same constant in every button, so a
	// selected neighbour is exactly as wide as an unselected one.
	for binID, button := range map[int64]string{10: first, 11: second} {
		if got := countOccurrences(button, pickerGlyphSlotClass); got != 1 {
			t.Errorf("bin %d must render exactly one fixed-width glyph slot (got %d):\n%s", binID, got, button)
		}
		if got := strings.Count(button, "✓"); got != 1 {
			t.Errorf("bin %d must keep its check glyph in the DOM while unselected (got %d)", binID, got)
		}
		if strings.Contains(button, "x-show") || strings.Contains(button, "display:none") {
			t.Errorf("bin %d must hide the unselected glyph with opacity, not layout:\n%s", binID, button)
		}
	}

	// Both neighbours reserve the identical width-bearing class list, which is
	// what guarantees equal reserved width regardless of selection state.
	extractSlotClass := func(button string) string {
		start := strings.Index(button, `class="inline-block`)
		if start < 0 {
			t.Fatalf("button has no fixed-width glyph slot:\n%s", button)
		}
		valueStart := start + len(`class="`)
		valueEnd := strings.Index(button[valueStart:], `"`)
		if valueEnd < 0 {
			t.Fatalf("glyph slot class attribute is not closed:\n%s", button)
		}
		return button[start : valueStart+valueEnd+1]
	}
	if firstClass, secondClass := extractSlotClass(first), extractSlotClass(second); firstClass != secondClass {
		t.Errorf("neighbour bins must reserve identical slot width:\nfirst:  %q\nsecond: %q", firstClass, secondClass)
	} else if firstClass != pickerGlyphSlotClass {
		t.Errorf("glyph slot class = %q, want the fixed-width %q", firstClass, pickerGlyphSlotClass)
	}
	if !strings.Contains(pickerGlyphSlotClass, "w-4") || !strings.Contains(pickerGlyphSlotClass, "shrink-0") {
		t.Errorf("the glyph slot must declare a fixed width and resist compression: %q", pickerGlyphSlotClass)
	}
}

// TestStorageBinPickerComponent_BinCountLabelColonMatchesLocale pins the
// locale-specific separator: Chinese uses the full-width colon (no trailing
// space) and every other locale keeps the existing half-width "colon space".
func TestStorageBinPickerComponent_BinCountLabelColonMatchesLocale(t *testing.T) {
	if err := i18n.InitWithDir("../../locales"); err != nil {
		t.Fatalf("initialize locales: %v", err)
	}

	view := components.StorageBinPickerView{
		Cabinets: []components.StorageBinPickerCabinet{{
			ID:   1,
			Name: "柜体",
			Drawers: []components.StorageBinPickerDrawer{{
				ID:    2,
				Label: "抽屉",
				Bins:  []components.StorageBinPickerBin{{ID: 3, Name: "料箱"}},
			}},
		}},
	}

	render := func(ctx context.Context, lang string) string {
		t.Helper()
		var buf bytes.Buffer
		if err := components.StorageBinPicker(view).Render(i18n.WithLocalizer(ctx, lang), &buf); err != nil {
			t.Fatalf("render storage bin picker for %s: %v", lang, err)
		}
		return buf.String()
	}

	zh := render(context.Background(), "zh")
	if !strings.Contains(zh, "料箱：1") {
		t.Errorf("Chinese bin-count label must use the full-width colon:\n%s", zh)
	}
	if strings.Contains(zh, "料箱: 1") {
		t.Errorf("Chinese bin-count label must not use the half-width colon:\n%s", zh)
	}

	zhCN := render(context.Background(), "zh-CN")
	if !strings.Contains(zhCN, "料箱：1") {
		t.Errorf("regional Chinese bin-count label must use the full-width colon:\n%s", zhCN)
	}

	en := render(context.Background(), "en")
	if !strings.Contains(en, "Bins: 1") {
		t.Errorf("non-Chinese locales must keep the existing half-width colon:\n%s", en)
	}
}

func assertTableCount(t *testing.T, dbConn *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := dbConn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Errorf("%s = %d rows, want %d (the picker must not write)", table, got, want)
	}
}
