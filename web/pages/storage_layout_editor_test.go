package pages

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/storage"
)

func TestStorageLayoutEditorRendersCanvasAndStableLayoutData(t *testing.T) {
	layout := storage.CabinetLayout{
		Cabinet: db.Cabinet{ID: 42, Name: "Test", LayoutRevision: 7, CoordinateSystem: "grid", LayoutWidth: 10, LayoutHeight: 10, LayoutDecorationsJson: "[]"},
		Drawers: []storage.DrawerLayout{{Drawer: db.Drawer{ID: 9, CabinetID: 42, Label: "D", Width: 1, Height: 1}, Bins: []storage.LayoutBin{{BinID: 3, Name: "Bin 3", DrawerID: 9, Width: 1, Height: 1}}}},
	}
	var out bytes.Buffer
	if err := StorageLayoutEditor(layout, auth.User{Role: "admin"}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`id="layout-editor-data"`, `layout_revision`, `bin_id`, `/static/js/physical_layout_editor.js`, `Interactive physical layout canvas`, `Save layout`, `csrf_token`, `/storage/cabinets/42/edit`} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered layout editor missing %q", want)
		}
	}
}
