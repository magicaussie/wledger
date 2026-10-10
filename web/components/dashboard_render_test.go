package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/i18n"
)

func init() {
	// Best-effort load of the real locale bundle so empty-state text renders in
	// English during render tests. If it fails, i18n.T falls back to the key.
	_ = i18n.InitWithDir("../../locales")
}

const longBinName = "A very long bin name that would overflow a tiny dashboard tile"

// TestDashboardGridBoundsLongBinNames verifies the legacy dashboard tile keeps
// the complete name available (title + aria-label) while bounding the visible
// text so a long name cannot overflow the fixed-size tile.
func TestDashboardGridBoundsLongBinNames(t *testing.T) {
	ctrl := DashboardController{
		ID:   1,
		Name: "Controller With A Very Long Name That Could Overflow The Header",
		Containers: []DashboardContainer{{
			ID:               2,
			Name:             "Container With A Very Long Name",
			SegmentID:        0,
			ControllerName:   "Controller With A Very Long Name That Could Overflow The Header",
			ControllerOnline: true,
			Bins: []DashboardBin{{
				ID: 3, Name: longBinName, GridX: 0, GridY: 0, Statuses: []string{"ok"},
			}},
		}},
	}

	var buf bytes.Buffer
	if err := DashboardGrid(ctrl).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `title="`+longBinName+`"`) {
		t.Errorf("expected the full bin name in a title attribute")
	}
	if !strings.Contains(out, `aria-label="`+longBinName+`"`) {
		t.Errorf("expected the full bin name in an aria-label")
	}
	if !strings.Contains(out, "truncate") {
		t.Errorf("expected a truncate class for bounded bin text")
	}
	if !strings.Contains(out, "overflow-hidden") {
		t.Errorf("expected overflow-hidden on the bin tile")
	}
}

// TestDashboardGridEmptyController verifies a controller with no containers
// renders the existing empty state rather than a broken grid.
func TestDashboardGridEmptyController(t *testing.T) {
	ctrl := DashboardController{ID: 1, Name: "Empty Controller"}

	var buf bytes.Buffer
	if err := DashboardGrid(ctrl).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if !strings.Contains(buf.String(), "No containers configured.") {
		t.Errorf("expected the empty-state message for a controller with no containers")
	}
}

// TestDashboardContainerCardBoundsLongNames verifies the wall card and its modal
// bound long container/bin names while keeping the full text in title attributes,
// and that the invalid border-500/10 utility class is gone.
func TestDashboardContainerCardBoundsLongNames(t *testing.T) {
	container := DashboardContainer{
		ID:               5,
		Name:             "A very long container name that would overflow the card header",
		SegmentID:        1,
		ControllerName:   "A very long controller name that would overflow the card",
		ControllerOnline: true,
		Bins: []DashboardBin{{
			ID: 6, Name: longBinName, GridX: 0, GridY: 0, Statuses: []string{"low"},
		}},
	}

	var buf bytes.Buffer
	if err := DashboardContainerCard(container).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `title="`+container.Name+`"`) {
		t.Errorf("expected the container name in a title attribute")
	}
	if !strings.Contains(out, `title="`+longBinName+`"`) {
		t.Errorf("expected the bin name in a title attribute in the modal")
	}
	if !strings.Contains(out, "truncate") {
		t.Errorf("expected truncate classes for bounded text")
	}
	if !strings.Contains(out, "modal") {
		t.Errorf("expected the grid preview modal markup")
	}
	if strings.Contains(out, "border-500/10") {
		t.Errorf("invalid border-500/10 utility class should have been removed")
	}
}

// TestDashboardContainerCardNoDuplicateModalIDs verifies two cards for the same
// container do not emit duplicate DOM ids: the modal is addressed through a
// scoped Alpine x-ref instead of a global container_modal_<id>.
func TestDashboardContainerCardNoDuplicateModalIDs(t *testing.T) {
	container := DashboardContainer{ID: 7, Name: "Drawer", ControllerName: "Ctrl", ControllerOnline: true}

	var buf bytes.Buffer
	for i := 0; i < 2; i++ {
		if err := DashboardContainerCard(container).Render(context.Background(), &buf); err != nil {
			t.Fatalf("render failed: %v", err)
		}
	}
	out := buf.String()

	if strings.Contains(out, "container_modal_") {
		t.Errorf("modal must not use a global container_modal_ id")
	}
	if got := strings.Count(out, `x-ref="modal"`); got != 2 {
		t.Errorf("expected 2 scoped x-ref=modal attributes, got %d", got)
	}
}

// TestDashboardContainerCardSemanticTrigger verifies the card trigger is a
// semantic button and the dialog is a sibling (not nested inside the button),
// so clicks inside the modal cannot re-trigger the card.
func TestDashboardContainerCardSemanticTrigger(t *testing.T) {
	container := DashboardContainer{ID: 8, Name: "Drawer", ControllerName: "Ctrl", ControllerOnline: true}

	var buf bytes.Buffer
	if err := DashboardContainerCard(container).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, `type="button"`) {
		t.Errorf("expected a semantic button trigger")
	}
	if !strings.Contains(out, `aria-haspopup="dialog"`) {
		t.Errorf("expected aria-haspopup=dialog on the trigger")
	}
	btnClose := strings.Index(out, "</button>")
	dlgOpen := strings.Index(out, "<dialog")
	if btnClose == -1 || dlgOpen == -1 {
		t.Fatalf("expected both a button and a dialog in the output")
	}
	if dlgOpen < btnClose {
		t.Errorf("dialog must not be nested inside the trigger button")
	}
	if !strings.Contains(out, "@click.stop") {
		t.Errorf("expected the dialog to stop click propagation")
	}
}

// TestDashboardContainerCardModalScrolls verifies the modal-box no longer
// disables vertical scrolling (DaisyUI default) and the grid keeps horizontal
// scrolling, while the close and backdrop forms remain.
func TestDashboardContainerCardModalScrolls(t *testing.T) {
	container := DashboardContainer{
		ID: 9, Name: "Drawer", ControllerName: "Ctrl", ControllerOnline: true,
		Bins: []DashboardBin{{ID: 10, Name: "B1", GridX: 0, GridY: 0, Statuses: []string{"ok"}}},
	}

	var buf bytes.Buffer
	if err := DashboardContainerCard(container).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	if strings.Contains(out, "modal-box max-w-4xl p-0 overflow-hidden") {
		t.Errorf("modal-box must not be overflow-hidden (it defeats vertical scrolling)")
	}
	if !strings.Contains(out, "modal-box max-w-4xl p-0 border border-base-300") {
		t.Errorf("expected the modal-box without overflow-hidden")
	}
	if !strings.Contains(out, "overflow-x-auto") {
		t.Errorf("expected the grid wrapper to keep horizontal scrolling")
	}
	if !strings.Contains(out, "modal-backdrop") {
		t.Errorf("expected the modal backdrop form")
	}
	if strings.Count(out, `method="dialog"`) < 2 {
		t.Errorf("expected both the close form and the backdrop form")
	}
}

// TestDashboardContainerCardEmptyBins verifies an empty container shows an
// explicit empty state instead of a blank grid.
func TestDashboardContainerCardEmptyBins(t *testing.T) {
	container := DashboardContainer{ID: 11, Name: "Empty Drawer", ControllerName: "Ctrl", ControllerOnline: true}

	var buf bytes.Buffer
	if err := DashboardContainerCard(container).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "No bins mapped to this container.") {
		t.Errorf("expected the empty-bin message in the wall modal")
	}
	if strings.Contains(out, `class="grid gap-1.5`) {
		t.Errorf("empty container should not render a grid")
	}
}

// TestDashboardGridEmptyContainer verifies the legacy grid shows the same empty
// state for a container with no mapped bins.
func TestDashboardGridEmptyContainer(t *testing.T) {
	ctrl := DashboardController{
		ID: 12, Name: "Ctrl",
		Containers: []DashboardContainer{{
			ID: 13, Name: "Empty Drawer", ControllerName: "Ctrl", ControllerOnline: true,
		}},
	}

	var buf bytes.Buffer
	if err := DashboardGrid(ctrl).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "No bins mapped to this container.") {
		t.Errorf("expected the empty-bin message in the legacy grid")
	}
}
