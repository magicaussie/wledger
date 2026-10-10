package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
)

func TestConversionPreviewRenders(t *testing.T) {
	view := ConversionView{
		Space:        "segment",
		TotalDrawers: 2,
		Convertible:  1,
		Blocked:      1,
		AffectedBins: 2,
		Fingerprint:  "fp123",
		CSRF:         "tok456",
		Result:       "stale",
		Findings: []ConversionFindingView{
			{Name: "Drawer A", SegmentID: 0, LedStart: 0, LedCount: 10, Convertible: true,
				Bins: []ConversionBinView{{BinID: 7, From: 3, To: 3}}},
			{Name: "Drawer B", SegmentID: 0, LedStart: 10, LedCount: 10, Convertible: false, Reason: "overlapping LED allocation in segment"},
		},
	}

	var buf bytes.Buffer
	if err := ConversionPreview(auth.User{}, view).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Drawer A", "Drawer B", "overlapping LED allocation", "fp123", "tok456"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in rendered output", want)
		}
	}
}

// TestConversionPreviewHidesConfirmationWhenBlocked verifies the confirmation form
// is not offered while any drawer is blocked.
func TestConversionPreviewHidesConfirmationWhenBlocked(t *testing.T) {
	view := ConversionView{Space: "segment", Blocked: 1, Fingerprint: "f", CSRF: "c"}
	var buf bytes.Buffer
	if err := ConversionPreview(auth.User{}, view).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if strings.Contains(buf.String(), `name="fingerprint"`) {
		t.Error("confirmation form should be hidden when drawers are blocked")
	}
}

// TestConversionPreviewShowsConfirmationWhenClean verifies the confirmation form is
// offered for a clean segment-relative preflight.
func TestConversionPreviewShowsConfirmationWhenClean(t *testing.T) {
	view := ConversionView{Space: "segment", Convertible: 1, Fingerprint: "f", CSRF: "c"}
	var buf bytes.Buffer
	if err := ConversionPreview(auth.User{}, view).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `name="fingerprint"`) || !strings.Contains(out, `name="csrf_token"`) {
		t.Error("expected confirmation form with csrf_token and fingerprint hidden fields")
	}
}

// TestConversionPreviewUnresolvedExplainsWhy verifies the unresolved state renders a
// specific explanation and no confirmation form.
func TestConversionPreviewUnresolvedExplainsWhy(t *testing.T) {
	view := ConversionView{Space: "unresolved", Fingerprint: "f", CSRF: "c"}
	var buf bytes.Buffer
	if err := ConversionPreview(auth.User{}, view).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `name="fingerprint"`) {
		t.Error("confirmation must not be offered for an unresolved space")
	}
	// With no i18n bundle loaded the message id is rendered verbatim.
	if !strings.Contains(out, "ConversionUnresolvedNotice") {
		t.Error("expected an unresolved-space explanation")
	}
}

// TestConversionPreviewAlreadyDrawer verifies the already-converted state states that
// nothing needs to be done and offers no confirmation.
func TestConversionPreviewAlreadyDrawer(t *testing.T) {
	view := ConversionView{Space: "drawer", Fingerprint: "f", CSRF: "c"}
	var buf bytes.Buffer
	if err := ConversionPreview(auth.User{}, view).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `name="fingerprint"`) {
		t.Error("confirmation must not be offered for an already drawer-relative space")
	}
	if !strings.Contains(out, "NoConversionNeeded") {
		t.Error("expected a nothing-to-convert message")
	}
}
