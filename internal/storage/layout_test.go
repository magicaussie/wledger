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

func TestLayoutGetReportsMissingControllerReference(t *testing.T) {
	t.Helper()
	conn, err := db.Open("file:" + t.TempDir() + "/orphan-controller.db")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetMaxOpenConns(1)
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO cabinets(id,name,controller_id,coordinate_system,layout_width,layout_height) VALUES(1,'Cabinet',999,'grid',10,10)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayoutService(db.NewStore(conn)).Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Controller != nil || layout.ControllerError != "Cabinet references missing LED controller #999." {
		t.Fatalf("controller reference state: %#v", layout)
	}
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
	if _, err = store.UpdateCabinetLayoutSettings(ctx, db.UpdateCabinetLayoutSettingsParams{ID: cabinet.ID, Name: "Cabinet", CoordinateSystem: "grid", LayoutWidth: 20, LayoutHeight: 20, LayoutDecorationsJson: "[]"}); err != nil {
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

func TestEmptyLayoutUsesNonNilJSONCollections(t *testing.T) {
	store := newLayoutStore(t)
	ctx := context.Background()
	id, err := NewLayoutService(store).CreateCabinet(ctx, "Empty", 0, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := NewLayoutService(store).Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Drawers == nil || layout.UnattachedBins == nil {
		t.Fatalf("editor collections must serialize as []: %#v", layout)
	}
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
func TestLayoutSavePersistsDecorationsSeparately(t *testing.T) {
	s := seedLayout(t)
	in := layoutInput()
	in.Decorations = []DecorationInput{{Key: "paint-1", Kind: "outline", X: 12, Y: 12, Width: 4, Height: 4}}
	if err := s.Save(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	layout, err := s.Get(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Cabinet.LayoutDecorationsJson != `[{"key":"paint-1","kind":"outline","x":12,"y":12,"width":4,"height":4}]` {
		t.Fatalf("decorations not persisted separately: %s", layout.Cabinet.LayoutDecorationsJson)
	}
	if layout.Drawers[0].Drawer.ID != 1 || layout.Drawers[0].Bins[0].BinID != 1 {
		t.Fatalf("paint data changed inventory layout: %#v", layout.Drawers)
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
	outside := layoutInput()
	outside.Drawers[0].X = 15
	if err := s.Save(context.Background(), outside); !errors.Is(err, ErrLayoutInvalid) {
		t.Fatalf("out-of-bounds error=%v", err)
	}
	layout, _ = s.Get(context.Background(), 1)
	if layout.Cabinet.Name != "Cabinet" || layout.Cabinet.LayoutRevision != 0 {
		t.Fatalf("out-of-bounds save persisted: %#v", layout.Cabinet)
	}
	badPaint := layoutInput()
	badPaint.Decorations = []DecorationInput{{Key: "outside", Kind: "rectangle", X: 19, Y: 19, Width: 2, Height: 2}}
	if err := s.Save(context.Background(), badPaint); !errors.Is(err, ErrLayoutInvalid) {
		t.Fatalf("out-of-bounds decoration error=%v", err)
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

func TestDetachThenDeleteDrawerAndCabinetPreservesBin(t *testing.T) {
	s := seedLayout(t)
	ctx := context.Background()
	if err := s.DeleteDrawer(ctx, 1, 1, 0); !errors.Is(err, ErrUnsafeLayout) {
		t.Fatalf("delete occupied drawer = %v", err)
	}
	if err := s.DetachBinPlacement(ctx, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.GetBin(ctx, 1); err != nil {
		t.Fatalf("detach removed inventory bin: %v", err)
	}
	if err := s.DeleteDrawer(ctx, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCabinet(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.GetBin(ctx, 1); err != nil {
		t.Fatalf("cabinet lifecycle removed inventory bin: %v", err)
	}
}

func TestArchiveAndStaleLifecycleAreProtected(t *testing.T) {
	s := seedLayout(t)
	ctx := context.Background()
	if err := s.ArchiveDrawer(ctx, 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.ArchiveCabinet(ctx, 1, 0); !errors.Is(err, ErrLayoutStale) {
		t.Fatalf("stale archive = %v", err)
	}
	layout, err := s.Get(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Cabinet.LayoutRevision != 1 || layout.Drawers[0].Drawer.Archived != 1 {
		t.Fatalf("archive not persisted: %#v", layout)
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
