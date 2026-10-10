package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

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
