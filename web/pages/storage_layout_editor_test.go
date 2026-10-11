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

func TestStorageLayoutEditorUsesJSONFieldNamesForLifecycleURLs(t *testing.T) {
	layout := storage.CabinetLayout{Cabinet: db.Cabinet{ID: 42, Name: "Test", LayoutRevision: 7, CoordinateSystem: "grid", LayoutWidth: 10, LayoutHeight: 10}, Drawers: []storage.DrawerLayout{{Drawer: db.Drawer{ID: 9, CabinetID: 42, Label: "D", Width: 1, Height: 1}, Bins: []db.BinDrawerAttachment{{BinID: 3, DrawerID: 9, Width: 1, Height: 1}}}}}
	var out bytes.Buffer
	if err := StorageLayoutEditor(layout, auth.User{Role: "admin"}).Render(context.Background(), &out); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	for _, want := range []string{`id:d.Cabinet.id`, `layout_revision`, `id:x.Drawer.id`, `id:b.bin_id`, `/storage/cabinets/42/edit`, `/delete`, `csrf_token`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, wrong := range []string{`d.Cabinet.ID`, `x.Drawer.ID`, `b.BinID`} {
		if strings.Contains(html, wrong) {
			t.Errorf("uses Go field casing %q", wrong)
		}
	}
}
