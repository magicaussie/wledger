package pages

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
)

// TestPartDetail_RendersUnsafeLinksAsInertText verifies defence in depth: even
// when an unsafe URL is already stored in the database (bypassing input
// validation), the product-detail page must not emit it as an executable href.
func TestPartDetail_RendersUnsafeLinksAsInertText(t *testing.T) {
	part := db.Part{ID: 1, Name: "Test Part"}
	links := []db.PartLink{
		{ID: 1, PartID: 1, Url: "javascript:alert(1)", Label: sql.NullString{String: "Evil JS", Valid: true}},
		{ID: 2, PartID: 1, Url: "data:text/html,<script>alert(1)</script>", Label: sql.NullString{String: "Evil Data", Valid: true}},
		{ID: 3, PartID: 1, Url: "//evil.example", Label: sql.NullString{String: "Evil Proto", Valid: true}},
		{ID: 4, PartID: 1, Url: "https://example.com/good", Label: sql.NullString{String: "Good Link", Valid: true}},
	}

	var buf bytes.Buffer
	if err := PartDetail(auth.User{}, part, nil, links, nil, nil).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()

	// Unsafe values must never appear in the output, executable or otherwise.
	for _, bad := range []string{
		"javascript:alert(1)",
		"data:text/html",
		"<script>alert(1)</script>",
		"//evil.example",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("unsafe value %q was emitted in output", bad)
		}
	}
	if strings.Contains(out, `href="javascript:`) || strings.Contains(out, `href="data:`) || strings.Contains(out, `href="//`) {
		t.Errorf("unsafe URL was emitted as an href")
	}

	// Labels are still shown as inert text.
	for _, label := range []string{"Evil JS", "Evil Data", "Evil Proto"} {
		if !strings.Contains(out, label) {
			t.Errorf("expected unsafe link label %q to be rendered as text", label)
		}
	}

	// Valid links still render as clickable hrefs.
	if !strings.Contains(out, `href="https://example.com/good"`) {
		t.Errorf("expected valid link to be rendered as an href")
	}
	if !strings.Contains(out, "Good Link") {
		t.Errorf("expected valid link label to be rendered")
	}
}
