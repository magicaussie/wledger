package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

// TestDrawerDetailRendersLocateManually is a regression test for the manual-only
// drawer locate behaviour: rendering the drawer page must not emit any page-load
// trigger that would issue an automatic LED locate, while the explicit Locate
// control must remain present and wired to the locate endpoint.
func TestDrawerDetailRendersLocateManually(t *testing.T) {
	drawer := db.Container{ID: 5, Name: "Drawer 1", SegmentID: 0}
	cabinet := db.Controller{ID: 3, Name: "Cabinet"}
	bins := []DrawerBin{{ID: 69, Name: "Bin A"}}

	var buf bytes.Buffer
	if err := DrawerDetail(auth.User{}, drawer, cabinet, bins).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	// The explicit Locate control is present and wired to the locate endpoint.
	if !strings.Contains(out, `hx-post="/drawers/5/locate"`) {
		t.Errorf("explicit Locate control missing or not wired to /drawers/5/locate")
	}

	// The regression: the drawer view must not auto-run a locate on page load.
	// (Other hx-trigger values legitimately come from the shared layout/sidebar.)
	if strings.Contains(out, `hx-trigger="load"`) {
		t.Error("drawer page must not contain hx-trigger=\"load\" (no automatic locate)")
	}

	// There must be exactly one locate endpoint reference (the explicit button),
	// never a second hidden auto-locate element.
	if n := strings.Count(out, "/drawers/5/locate"); n != 1 {
		t.Errorf("expected exactly one locate reference, found %d", n)
	}
}
