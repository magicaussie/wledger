package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func newLayoutStore(t *testing.T) db.Store {
	t.Helper()
	conn, err := db.Open("file:" + t.TempDir() + "/layout.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err = db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	return db.NewStore(conn)
}

func seedLayout(t *testing.T) *LayoutService {
	t.Helper()
	store := newLayoutStore(t)
	ctx := context.Background()
	if _, err := store.CreateCabinet(ctx, db.CreateCabinetParams{Name: "Cabinet", CoordinateSystem: "grid"}); err != nil {
		t.Fatal(err)
	}
	// Configure the physical canvas using the M7 query, just as the service does.
	cabinet, err := store.GetCabinet(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateCabinetLayoutSettings(ctx, db.UpdateCabinetLayoutSettingsParams{ID: cabinet.ID, Name: "Cabinet", CoordinateSystem: "grid", LayoutWidth: 20, LayoutHeight: 20}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateDrawer(ctx, db.CreateDrawerParams{CabinetID: 1, Label: "One", Width: 10, Height: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateBin(ctx, db.CreateBinParams{Name: "Bin"}); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateBinDrawerAttachment(ctx, db.CreateBinDrawerAttachmentParams{BinID: 1, DrawerID: 1, Width: 2, Height: 2}); err != nil {
		t.Fatal(err)
	}
	return NewLayoutService(store)
}

func layoutInput() LayoutInput {
	return LayoutInput{ID: 1, Revision: 0, Name: "Renamed", CoordinateSystem: "grid", Width: 20, Height: 20, Drawers: []DrawerInput{{ID: 1, Label: "Renumbered", DisplayOrder: 3, X: 1, Y: 1, Width: 10, Height: 10, Bins: []BinInput{{ID: 1, DisplayOrder: 2, X: 3, Y: 3, Width: 2, Height: 2}}}}}
}

func TestLayoutSavePreservesStableIdentities(t *testing.T) {
	s := seedLayout(t)
	if err := s.Save(context.Background(), layoutInput()); err != nil {
		t.Fatal(err)
	}
	layout, err := s.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Cabinet.LayoutRevision != 1 || layout.Cabinet.Name != "Renamed" || layout.Drawers[0].Drawer.ID != 1 || layout.Drawers[0].Bins[0].BinID != 1 {
		t.Fatalf("layout not preserved: %#v", layout)
	}
}
func TestLayoutSaveRejectsStaleAndOverlapWithoutWrites(t *testing.T) {
	s := seedLayout(t)
	bad := layoutInput()
	bad.Drawers = append(bad.Drawers, DrawerInput{Label: "Overlap", X: 5, Y: 5, Width: 5, Height: 5})
	if err := s.Save(context.Background(), bad); !errors.Is(err, ErrLayoutInvalid) {
		t.Fatalf("overlap error=%v", err)
	}
	layout, _ := s.Get(context.Background(), 1)
	if layout.Cabinet.Name != "Cabinet" || layout.Cabinet.LayoutRevision != 0 {
		t.Fatalf("invalid save persisted: %#v", layout.Cabinet)
	}
	stale := layoutInput()
	stale.Revision = 7
	if err := s.Save(context.Background(), stale); !errors.Is(err, ErrLayoutStale) {
		t.Fatalf("stale error=%v", err)
	}
}
func TestLayoutSaveRejectsOmittedAttachedBin(t *testing.T) {
	s := seedLayout(t)
	in := layoutInput()
	in.Drawers[0].Bins = nil
	if err := s.Save(context.Background(), in); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("omitted bin error = %v", err)
	}
	layout, err := s.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.Drawers[0].Bins) != 1 || layout.Drawers[0].Bins[0].BinID != 1 {
		t.Fatalf("attached bin was changed: %#v", layout.Drawers[0].Bins)
	}
}

func TestLayoutSaveAssociatesExistingUnattachedBin(t *testing.T) {
	s := seedLayout(t)
	ctx := context.Background()
	if _, err := s.store.CreateBin(ctx, db.CreateBinParams{Name: "Loose"}); err != nil {
		t.Fatal(err)
	}
	in := layoutInput()
	in.Drawers[0].Bins = append(in.Drawers[0].Bins, BinInput{ID: 2, Name: "Loose", DisplayOrder: 1, X: 6, Y: 1, Width: 2, Height: 2})
	if err := s.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	layout, _ := s.Get(ctx, 1)
	if len(layout.Drawers[0].Bins) != 2 {
		t.Fatalf("got %d bins", len(layout.Drawers[0].Bins))
	}
}
