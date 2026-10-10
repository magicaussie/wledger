package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/web/components"
)

// TestDashboardWallRendersTwoContainersOneEmpty renders the full dashboard page
// with a synthetic Wall (two containers, one empty) and asserts the wall card,
// modal and accessibility contract that Task 030/030B established.
//
// Note: like the other render tests in this package, no i18n bundle is loaded,
// so i18n.T/TD render the message ids verbatim (e.g. "NoBinsMapped",
// "OpenContainer"). The English strings are covered by the components tests.
func TestDashboardWallRendersTwoContainersOneEmpty(t *testing.T) {
	user := auth.User{ID: 1, Email: "demo@example.test", Role: "admin"}
	stats := db.GetDashboardStatsRow{TotalControllers: 1, OnlineControllers: 0, TotalStockValue: 0.0}

	wall := components.DashboardWall{
		ID:          1,
		Name:        "Demo Wall",
		Description: "Synthetic demo wall",
		Containers: []components.DashboardContainer{
			{
				ID:               10,
				Name:             "Demo Drawer A — A Very Long Container Name For Overflow Testing",
				SegmentID:        0,
				ControllerName:   "Demo Controller",
				ControllerOnline: false,
				Bins: []components.DashboardBin{
					{ID: 100, Name: "R1C1", GridX: 0, GridY: 0, Statuses: []string{"ok"}},
					{ID: 101, Name: "A very long bin name that should truncate", GridX: 1, GridY: 0, Statuses: []string{"low"}},
				},
			},
			{
				ID:               11,
				Name:             "Demo Drawer B (empty)",
				SegmentID:        1,
				ControllerName:   "Demo Controller",
				ControllerOnline: false,
			},
		},
	}

	var buf bytes.Buffer
	if err := Dashboard(user, stats, []components.DashboardWall{wall}, nil).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "Demo Wall") {
		t.Errorf("expected the wall name to render")
	}
	if got := strings.Count(out, `type="button"`); got < 2 {
		t.Errorf("expected at least 2 card trigger buttons, got %d", got)
	}
	if !strings.Contains(out, "NoBinsMapped") {
		t.Errorf("expected the empty-container state to render")
	}
	if !strings.Contains(out, `aria-label="OpenContainer"`) {
		t.Errorf("expected the localized OpenContainer aria-label")
	}
	if strings.Contains(out, "container_modal_") {
		t.Errorf("container modals must not use a global container_modal_ id")
	}
	if got := strings.Count(out, `x-ref="modal"`); got != 2 {
		t.Errorf("expected 2 scoped x-ref=modal dialogs, got %d", got)
	}

	// The card trigger must contain only phrasing content (no block containers).
	start := strings.Index(out, "<button")
	end := strings.Index(out, "</button>")
	if start == -1 || end == -1 || end < start {
		t.Fatalf("could not locate the card trigger button")
	}
	btn := out[start : end+len("</button>")]
	if strings.Contains(btn, "<div") {
		t.Errorf("card trigger button must not contain a <div>")
	}

	// The modal-box must not disable DaisyUI's vertical scrolling.
	if strings.Contains(out, "modal-box max-w-4xl p-0 overflow-hidden") {
		t.Errorf("modal-box must not be overflow-hidden")
	}
}
