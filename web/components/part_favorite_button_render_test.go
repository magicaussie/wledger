package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPartFavoriteButtonRenders(t *testing.T) {
	var buf bytes.Buffer
	if err := PartFavoriteButton(1, true, true).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "/parts/1/favorite") {
		t.Errorf("expected hx-post to the favorite endpoint, got: %s", out)
	}
	if !strings.Contains(out, "outerHTML") {
		t.Errorf("expected an outerHTML swap")
	}
}

func TestPartFavoriteButtonReadOnly(t *testing.T) {
	// A non-writer must not get a toggle button.
	var buf bytes.Buffer
	if err := PartFavoriteButton(1, true, false).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	if strings.Contains(buf.String(), "hx-post") {
		t.Errorf("read-only user should not get a toggle button")
	}
}
