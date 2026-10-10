package components

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// TestGridPainterBindsCoordinateSpace verifies that the editable painter submits
// the coordinate space its indices were authored in, so a save cannot be
// interpreted in a different space.
func TestGridPainterBindsCoordinateSpace(t *testing.T) {
	for _, space := range []string{"segment", "drawer"} {
		var buf bytes.Buffer
		if err := GridPainter(db.Controller{ID: 1, Name: "C"}, nil, nil, space, true).Render(context.Background(), &buf); err != nil {
			t.Fatalf("render %s: %v", space, err)
		}
		out := buf.String()
		if !strings.Contains(out, `name="bin_index_space"`) {
			t.Errorf("missing bin_index_space field for %s", space)
		}
		if !strings.Contains(out, `value="`+space+`"`) {
			t.Errorf("missing coordinate-space value for %s", space)
		}
	}
}
