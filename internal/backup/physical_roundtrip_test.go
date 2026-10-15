package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// This file tests the forthcoming M3 physical-storage backup format: the default
// export writes exactly one current-format storage manifest and a restore
// accepts either a legacy 1.0 manifest or a current 2.3 manifest, never a mixed,
// duplicate, unknown or malformed envelope. It exercises the real public surface
// (default Export, Restore, the exported StorageManifest) and the M2 physical
// tables (cabinets, drawers, bin_drawer_attachments) plus M1 compatibility
// fingerprints.
//
// The physical graph is seeded and read with raw SQL rather than any generated
// symbol, so the file does not depend on the M3 restore query names and keeps
// working as those are added. Assertions compare state (fingerprints, raw rows,
// flags, geometry, timestamps) instead of prose, so a rejected archive is proven
// to leave the installation untouched without pinning any error message.

// Static contract pins for the M3 public surface. These fail to compile if the
// exported names or shapes differ from the contract under test: a current
// storage manifest embeds the legacy 1.0 Manifest, carries the physical graph as
// generated db rows and holds the complete system flag set.
var (
	_ = func(m StorageManifest) Manifest { return m.Legacy }
	_ = func(m StorageManifest) []db.SystemFlag { return m.SystemFlags }
	_ = func(m StorageManifest) ([]db.Cabinet, []db.Drawer, []db.BinDrawerAttachment) {
		return m.PhysicalStorage.Cabinets, m.PhysicalStorage.Drawers, m.PhysicalStorage.BinAttachments
	}
	_ = func(m StorageManifest) []db.DrawerLedAllocation { return m.PhysicalStorage.LEDAllocations }
	_ = func(m StorageManifest) []db.CabinetController { return m.PhysicalStorage.CabinetControllers }
)

// physCurrentVersion is the default storage-manifest format version a default
// export must carry.
const physCurrentVersion = "2.3"

// physCustomFlagKey / physCustomFlagValue are a flag outside the three the M1
// baseline tracks. A complete current backup must preserve it, so a restore
// cannot silently drop flags it does not recognise.
const (
	physCustomFlagKey   = "physical_roundtrip_custom_flag"
	physCustomFlagValue = "custom-value-7"
)

// physFixture is one isolated, file-backed installation: a migrated SQLite
// database (schema 011 included) plus its own uploads directory.
type physFixture struct {
	t          *testing.T
	conn       *sql.DB
	store      db.Store
	svc        Service
	uploadsDir string
	ctx        context.Context
}

func physNewFixture(t *testing.T, name string) *physFixture {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open("file:" + filepath.Join(dir, name+".sqlite"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate fixture database: %v", err)
	}
	uploadsDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		t.Fatalf("create uploads dir: %v", err)
	}
	store := db.NewStore(conn)
	return &physFixture{
		t:          t,
		conn:       conn,
		store:      store,
		svc:        NewService(conn, store, uploadsDir, slog.New(slog.NewTextHandler(io.Discard, nil))),
		uploadsDir: uploadsDir,
		ctx:        context.Background(),
	}
}

func (f *physFixture) initSettings() {
	f.t.Helper()
	if err := f.store.InitSettings(f.ctx); err != nil {
		f.t.Fatalf("init settings: %v", err)
	}
}

func (f *physFixture) export() []byte {
	f.t.Helper()
	var buf bytes.Buffer
	if err := f.svc.Export(f.ctx, &buf); err != nil {
		f.t.Fatalf("export: %v", err)
	}
	return buf.Bytes()
}

func (f *physFixture) restore(archive []byte) error {
	f.t.Helper()
	return f.svc.Restore(f.ctx, bytes.NewReader(archive), int64(len(archive)))
}

func physExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// physSeedLegacy seeds the legacy electrical/inventory graph a backup must carry
// unchanged: two controllers, two containers, mapped and NULL-index/width bins,
// two parts and multi-location/NULL/zero-quantity assignments, plus the
// coordinate/migration flags and one custom flag.
func physSeedLegacy(t *testing.T, conn *sql.DB) {
	t.Helper()
	exec := func(query string, args ...any) { physExec(t, conn, query, args...) }

	exec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (1, 'Legacy A', '192.168.1.10', 80, 300)`)
	exec(`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (2, 'Legacy B', '192.168.1.11', 80, 150)`)
	exec(`INSERT INTO controller_outputs (controller_id, display_order, name, pixel_count) VALUES (1, 0, 'Output 1', 300)`)
	exec(`INSERT INTO controller_outputs (controller_id, display_order, name, pixel_count) VALUES (2, 0, 'Output 1', 150)`)
	exec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (1, 'String 1', 1, 0, 0, 300)`)
	exec(`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (2, 'String 2', 2, 0, 0, 150)`)
	exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (1, 'Bin Mapped', 1, 0, 4, 0, 0)`)
	exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (2, 'Bin Mapped Second', 1, 10, 2, 1, 0)`)
	exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (3, 'Bin Null Index', 1, NULL, 1, 2, 0)`)
	exec(`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (4, 'Bin Null Width', 2, 50, NULL, 0, 0)`)
	exec(`INSERT INTO parts (id, name) VALUES (1, 'Part One')`)
	exec(`INSERT INTO parts (id, name) VALUES (2, 'Part Two')`)
	exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (1, 1, 1, 5)`)
	exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (2, 1, 2, 7)`)
	exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (3, 2, NULL, 3)`)
	exec(`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (4, 2, 4, 0)`)
	exec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, ledspace.FlagKey, ledspace.Segment)
	exec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, hardware.Migration005FlagKey, "true")
	exec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, hardware.DrawerAllocationBackfilledFlagKey, "true")
	exec(`INSERT INTO system_flags (key, value) VALUES (?, ?)`, physCustomFlagKey, physCustomFlagValue)
}

// physSeedPhysical seeds a small physical graph with variable geometry, mixed
// archive bits, non-trivial revisions and explicit timestamps: two cabinets (one
// attached to a controller, one free-standing), three drawers and three bin
// attachments, leaving one legacy bin unattached.
func physSeedPhysical(t *testing.T, conn *sql.DB) {
	t.Helper()
	exec := func(query string, args ...any) { physExec(t, conn, query, args...) }

	exec(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived, created_at, updated_at)
		VALUES (1, 'Cab Alpha', 1, 'grid', 3, 5, 0, 0, '2024-01-02 03:04:05', '2024-02-03 04:05:06')`)
	exec(`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived, created_at, updated_at)
		VALUES (2, 'Cab Beta', NULL, 'rows', 1, 0, 1, 1, '2024-03-04 05:06:07', '2024-03-04 05:06:07')`)
	exec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at)
		VALUES (1, 1, 'D1', 0, 0.0, 0.0, 12.5, 7.25, 0, '2024-01-05 06:07:08', '2024-01-05 06:07:08')`)
	exec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at)
		VALUES (2, 1, 'D2', 1, 12.5, 0.0, 6.0, 7.25, 1, '2024-01-06 07:08:09', '2024-01-06 07:08:09')`)
	exec(`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at)
		VALUES (3, 2, 'B1', 0, 1.5, 2.5, 3.0, 4.0, 0, '2024-01-07 08:09:10', '2024-01-07 08:09:10')`)
	exec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order, created_at, updated_at)
		VALUES (1, 1, 0.0, 0.0, 2.5, 3.5, 0, '2024-01-08 09:10:11', '2024-01-08 09:10:11')`)
	exec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order, created_at, updated_at)
		VALUES (2, 1, 2.5, 0.0, 2.0, 3.5, 1, '2024-01-09 10:11:12', '2024-01-09 10:11:12')`)
	exec(`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order, created_at, updated_at)
		VALUES (3, 3, 0.5, 0.5, 1.25, 2.0, 0, '2024-01-10 11:12:13', '2024-01-10 11:12:13')`)
	// The fixture migrates before seeding, so controller membership must be
	// inserted explicitly. Drawer allocations below use cabinet-wide coordinates.
	exec(`INSERT INTO cabinet_controllers (cabinet_id, controller_id, slot, created_at, updated_at)
		VALUES (1, 1, 0, '2024-01-02 03:04:05', '2024-02-03 04:05:06')`)
	exec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, start_pixel, pixel_count, display_order, created_at, updated_at)
		VALUES (1, 1, 1, 97, 8, 0, '2024-01-11 12:13:14', '2024-01-11 12:13:14')`)
	exec(`INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, start_pixel, pixel_count, display_order, created_at, updated_at)
		VALUES (2, 1, 1, 150, 4, 1, '2024-01-12 13:14:15', '2024-01-12 13:14:15')`)
}

func physWriteUpload(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create upload dir for %q: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write upload %q: %v", rel, err)
	}
}

// physSeedUploadSentinels writes the filesystem sentinels a rejected restore
// must leave untouched: a normal upload, a preserved recovery directory and an
// abandoned staging directory. The staging sentinel is decisive: any validation
// that runs after staging cleanup would remove it.
func physSeedUploadSentinels(t *testing.T, dir string) {
	t.Helper()
	physWriteUpload(t, dir, "sentinel.txt", "keep-me")
	physWriteUpload(t, dir, filepath.Join(".uploads_bak_sentinel", "precious.txt"), "keep-me-too")
	physWriteUpload(t, dir, filepath.Join(".restore_tmp_sentinel", "leftover.txt"), "keep-me-three")
}

func physUploadsSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot uploads %q: %v", dir, err)
	}
	return out
}

func physZipEntries(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %q: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return out
}

func physBuildZip(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatalf("create zip entry %q: %v", e[0], err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatalf("write zip entry %q: %v", e[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func physMarshalManifest(t *testing.T, manifest StorageManifest) string {
	t.Helper()
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal storage manifest: %v", err)
	}
	return string(raw)
}

func physCurrentArchive(t *testing.T, manifest StorageManifest) []byte {
	t.Helper()
	return physBuildZip(t, [][2]string{{currentStorageManifestName, physMarshalManifest(t, manifest)}})
}

func physMutatedArchive(t *testing.T, mutate func(*StorageManifest)) []byte {
	t.Helper()
	manifest := physBaseStorageManifest()
	mutate(&manifest)
	return physCurrentArchive(t, manifest)
}

func physDecodeStorageManifest(t *testing.T, archive []byte) StorageManifest {
	t.Helper()
	entries := physZipEntries(t, archive)
	raw, ok := entries[currentStorageManifestName]
	if !ok {
		t.Fatalf("archive is missing %s", currentStorageManifestName)
	}
	var manifest StorageManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode %s: %v", currentStorageManifestName, err)
	}
	return manifest
}

// physDuplicateFirstMember repeats the first top-level member of a compact JSON
// object, producing an ambiguous manifest with a repeated field.
func physDuplicateFirstMember(t *testing.T, raw string) string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("read object start: %v", err)
	}
	keyTok, err := dec.Token()
	if err != nil {
		t.Fatalf("read first member name: %v", err)
	}
	key, ok := keyTok.(string)
	if !ok {
		t.Fatalf("first member name is not a string: %v", keyTok)
	}
	var value json.RawMessage
	if err := dec.Decode(&value); err != nil {
		t.Fatalf("read first member value: %v", err)
	}
	return raw[:len(raw)-1] + `,"` + key + `":` + string(value) + `}`
}

func physTime() sql.NullTime {
	return sql.NullTime{Time: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true}
}

func physLegacyManifest() Manifest {
	now := physTime()
	return Manifest{
		Version:       "1.0",
		BinIndexSpace: ledspace.Segment,
		Settings:      minimalSettings(),
		Controllers: []db.Controller{
			{ID: 1, Name: "Legacy Ctrl", IpAddress: "10.0.0.9", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "Legacy String", ControllerID: 1, SegmentID: 0, LedStart: 0, LedCount: 8,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":8}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			{ID: 1, Name: "lb1", ContainerID: sql.NullInt64{Int64: 1, Valid: true}, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}
}

// physBaseStorageManifest is a valid current 2.3 manifest consistent with the
// seeded legacy and physical graph. Defect cases mutate one aspect of it.
func physBaseStorageManifest() StorageManifest {
	now := physTime()
	var m StorageManifest
	m.Version = physCurrentVersion
	m.ExportedAt = time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	m.Legacy = Manifest{
		Version:       "1.0",
		BinIndexSpace: ledspace.Segment,
		Settings:      minimalSettings(),
		Controllers: []db.Controller{
			{ID: 1, Name: "Ctrl", IpAddress: "10.0.0.1", LedCount: 300, CreatedAt: now},
		},
		ControllerOutputs: []db.ControllerOutput{
			{ID: 1, ControllerID: 1, DisplayOrder: 0, Name: "Output 1", PixelCount: 300, Direction: "forward", Pattern: "linear", Rows: 1, Columns: 1, CustomOrderJson: "[]", CreatedAt: now.Time, UpdatedAt: now.Time},
		},
		Containers: []db.Container{
			{ID: 1, Name: "S1", ControllerID: 1, SegmentID: 0, LedStart: 0, LedCount: 16,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":16}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			{ID: 1, Name: "b1", ContainerID: sql.NullInt64{Int64: 1, Valid: true}, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
			{ID: 2, Name: "b2", ContainerID: sql.NullInt64{Int64: 1, Valid: true}, LedIndex: sql.NullInt64{Int64: 2, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
		Parts: []db.Part{
			{ID: 1, Name: "P1", CreatedAt: now, UpdatedAt: now},
		},
		PartAssignments: []db.PartAssignment{
			{ID: 1, PartID: 1, BinID: sql.NullInt64{Int64: 1, Valid: true}, Quantity: 3},
		},
	}
	m.PhysicalStorage.Cabinets = []db.Cabinet{
		{ID: 1, Name: "Cab", ControllerID: sql.NullInt64{Int64: 1, Valid: true}, CoordinateSystem: "grid",
			LayoutRevision: 2, ElectricalRevision: 4, DisplayOrder: 0, Archived: 0, CreatedAt: now, UpdatedAt: now, LayoutWidth: 20, LayoutHeight: 10, LayoutDecorationsJson: `[{"key":"paint-1","kind":"outline","x":1,"y":1,"width":5,"height":4}]`},
	}
	m.PhysicalStorage.Drawers = []db.Drawer{
		{ID: 1, CabinetID: 1, Label: "D1", DisplayOrder: 0, X: 0, Y: 0, Width: 10, Height: 5, Archived: 0, CreatedAt: now, UpdatedAt: now},
	}
	m.PhysicalStorage.BinAttachments = []db.BinDrawerAttachment{
		{BinID: 1, DrawerID: 1, X: 0, Y: 0, Width: 2, Height: 3, DisplayOrder: 0, CreatedAt: now, UpdatedAt: now},
		{BinID: 2, DrawerID: 1, X: 2, Y: 0, Width: 2, Height: 3, DisplayOrder: 1, CreatedAt: now, UpdatedAt: now},
	}
	m.PhysicalStorage.CabinetControllers = []db.CabinetController{
		{CabinetID: 1, ControllerID: 1, Slot: 0, CreatedAt: now.Time, UpdatedAt: now.Time},
	}
	m.PhysicalStorage.LEDAllocations = []db.DrawerLedAllocation{
		{ID: 1, CabinetID: 1, DrawerID: 1, StartPixel: 97, PixelCount: 8, DisplayOrder: 0, CreatedAt: now.Time, UpdatedAt: now.Time},
		{ID: 2, CabinetID: 1, DrawerID: 1, StartPixel: 150, PixelCount: 4, DisplayOrder: 1, CreatedAt: now.Time, UpdatedAt: now.Time},
	}
	m.PhysicalStorage.CabinetLEDStrings = []db.CabinetLedString{}
	m.PhysicalStorage.CabinetLEDPathSections = []db.CabinetLedPathSection{}
	m.PhysicalStorage.AllocationCoordinateSpace = "cabinet"
	m.SystemFlags = []db.SystemFlag{
		{Key: ledspace.FlagKey, Value: ledspace.Segment, UpdatedAt: now},
		{Key: hardware.Migration005FlagKey, Value: "true", UpdatedAt: now},
	}
	return m
}

type physPhysicalRows struct {
	Cabinets               []db.Cabinet
	Drawers                []db.Drawer
	Attachments            []db.BinDrawerAttachment
	CabinetControllers     []db.CabinetController
	LEDAllocations         []db.DrawerLedAllocation
	CabinetLEDStrings      []db.CabinetLedString
	CabinetLEDPathSections []db.CabinetLedPathSection
}

func physReadPhysicalRows(t *testing.T, conn *sql.DB) physPhysicalRows {
	t.Helper()
	var rows physPhysicalRows

	cabinetRows, err := conn.Query(`SELECT id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived, created_at, updated_at, layout_width, layout_height, layout_decorations_json FROM cabinets ORDER BY id`)
	if err != nil {
		t.Fatalf("query cabinets: %v", err)
	}
	for cabinetRows.Next() {
		var c db.Cabinet
		if err := cabinetRows.Scan(&c.ID, &c.Name, &c.ControllerID, &c.CoordinateSystem, &c.LayoutRevision, &c.ElectricalRevision, &c.DisplayOrder, &c.Archived, &c.CreatedAt, &c.UpdatedAt, &c.LayoutWidth, &c.LayoutHeight, &c.LayoutDecorationsJson); err != nil {
			cabinetRows.Close()
			t.Fatalf("scan cabinet: %v", err)
		}
		rows.Cabinets = append(rows.Cabinets, c)
	}
	cabinetRows.Close()
	if err := cabinetRows.Err(); err != nil {
		t.Fatalf("iterate cabinets: %v", err)
	}

	drawerRows, err := conn.Query(`SELECT id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at FROM drawers ORDER BY id`)
	if err != nil {
		t.Fatalf("query drawers: %v", err)
	}
	for drawerRows.Next() {
		var d db.Drawer
		if err := drawerRows.Scan(&d.ID, &d.CabinetID, &d.Label, &d.DisplayOrder, &d.X, &d.Y, &d.Width, &d.Height, &d.Archived, &d.CreatedAt, &d.UpdatedAt); err != nil {
			drawerRows.Close()
			t.Fatalf("scan drawer: %v", err)
		}
		rows.Drawers = append(rows.Drawers, d)
	}
	drawerRows.Close()
	if err := drawerRows.Err(); err != nil {
		t.Fatalf("iterate drawers: %v", err)
	}

	attachmentRows, err := conn.Query(`SELECT bin_id, drawer_id, x, y, width, height, display_order, created_at, updated_at FROM bin_drawer_attachments ORDER BY bin_id`)
	if err != nil {
		t.Fatalf("query attachments: %v", err)
	}
	for attachmentRows.Next() {
		var a db.BinDrawerAttachment
		if err := attachmentRows.Scan(&a.BinID, &a.DrawerID, &a.X, &a.Y, &a.Width, &a.Height, &a.DisplayOrder, &a.CreatedAt, &a.UpdatedAt); err != nil {
			attachmentRows.Close()
			t.Fatalf("scan attachment: %v", err)
		}
		rows.Attachments = append(rows.Attachments, a)
	}
	attachmentRows.Close()
	if err := attachmentRows.Err(); err != nil {
		t.Fatalf("iterate attachments: %v", err)
	}

	membershipRows, err := conn.Query(`SELECT cabinet_id, controller_id, slot, strings_configured, created_at, updated_at FROM cabinet_controllers ORDER BY cabinet_id, slot`)
	if err != nil {
		t.Fatalf("query cabinet controllers: %v", err)
	}
	for membershipRows.Next() {
		var m db.CabinetController
		if err := membershipRows.Scan(&m.CabinetID, &m.ControllerID, &m.Slot, &m.StringsConfigured, &m.CreatedAt, &m.UpdatedAt); err != nil {
			membershipRows.Close()
			t.Fatalf("scan cabinet controller: %v", err)
		}
		rows.CabinetControllers = append(rows.CabinetControllers, m)
	}
	membershipRows.Close()
	if err := membershipRows.Err(); err != nil {
		t.Fatalf("iterate cabinet controllers: %v", err)
	}

	allocationRows, err := conn.Query(`SELECT id, cabinet_id, drawer_id, start_pixel, pixel_count, display_order, created_at, updated_at FROM drawer_led_allocations ORDER BY id`)
	if err != nil {
		t.Fatalf("query drawer LED allocations: %v", err)
	}
	for allocationRows.Next() {
		var a db.DrawerLedAllocation
		if err := allocationRows.Scan(&a.ID, &a.CabinetID, &a.DrawerID, &a.StartPixel, &a.PixelCount, &a.DisplayOrder, &a.CreatedAt, &a.UpdatedAt); err != nil {
			allocationRows.Close()
			t.Fatalf("scan drawer LED allocation: %v", err)
		}
		rows.LEDAllocations = append(rows.LEDAllocations, a)
	}
	allocationRows.Close()
	if err := allocationRows.Err(); err != nil {
		t.Fatalf("iterate drawer LED allocations: %v", err)
	}

	stringRows, err := conn.Query(`SELECT id,cabinet_id,controller_id,name,display_order,pixel_count,layout_complete,created_at,updated_at FROM cabinet_led_strings ORDER BY id`)
	if err != nil {
		t.Fatalf("query cabinet LED strings: %v", err)
	}
	for stringRows.Next() {
		var stringRecord db.CabinetLedString
		if err := stringRows.Scan(&stringRecord.ID, &stringRecord.CabinetID, &stringRecord.ControllerID, &stringRecord.Name, &stringRecord.DisplayOrder, &stringRecord.PixelCount, &stringRecord.LayoutComplete, &stringRecord.CreatedAt, &stringRecord.UpdatedAt); err != nil {
			stringRows.Close()
			t.Fatalf("scan cabinet LED string: %v", err)
		}
		rows.CabinetLEDStrings = append(rows.CabinetLEDStrings, stringRecord)
	}
	stringRows.Close()
	if err := stringRows.Err(); err != nil {
		t.Fatalf("iterate cabinet LED strings: %v", err)
	}
	sectionRows, err := conn.Query(`SELECT id,string_id,display_order,pixel_count,points_json,created_at,updated_at FROM cabinet_led_path_sections ORDER BY id`)
	if err != nil {
		t.Fatalf("query cabinet LED path sections: %v", err)
	}
	for sectionRows.Next() {
		var section db.CabinetLedPathSection
		if err := sectionRows.Scan(&section.ID, &section.StringID, &section.DisplayOrder, &section.PixelCount, &section.PointsJson, &section.CreatedAt, &section.UpdatedAt); err != nil {
			sectionRows.Close()
			t.Fatalf("scan cabinet LED path section: %v", err)
		}
		rows.CabinetLEDPathSections = append(rows.CabinetLEDPathSections, section)
	}
	sectionRows.Close()
	if err := sectionRows.Err(); err != nil {
		t.Fatalf("iterate cabinet LED path sections: %v", err)
	}

	return rows
}

// physReadFlags returns every system flag as key->value. Identity is the pair,
// so a restore is not required to preserve the flag's updated_at timestamp.
func physReadFlags(t *testing.T, conn *sql.DB) map[string]string {
	t.Helper()
	rows, err := conn.Query(`SELECT key, value FROM system_flags`)
	if err != nil {
		t.Fatalf("query flags: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			t.Fatalf("scan flag: %v", err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate flags: %v", err)
	}
	return out
}

func physQueryInt64Column(t *testing.T, conn *sql.DB, query string) []int64 {
	t.Helper()
	rows, err := conn.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan %q: %v", query, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %q: %v", query, err)
	}
	return out
}

func physQueryPairs(t *testing.T, conn *sql.DB, query string) map[int64]int64 {
	t.Helper()
	rows, err := conn.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var k, v int64
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan %q: %v", query, err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %q: %v", query, err)
	}
	return out
}

func physCapture(t *testing.T, store db.Store) hardware.CompatibilityBaseline {
	t.Helper()
	baseline, err := hardware.CaptureCompatibilityBaseline(context.Background(), store)
	if err != nil {
		t.Fatalf("capture compatibility baseline: %v", err)
	}
	return baseline
}

func physAssertFingerprintsEqual(t *testing.T, label string, want, got hardware.CompatibilityBaseline) {
	t.Helper()
	if want.InventoryFingerprint != got.InventoryFingerprint {
		t.Errorf("%s: inventory fingerprint changed: want %s got %s", label, want.InventoryFingerprint, got.InventoryFingerprint)
	}
	if want.MappingFingerprint != got.MappingFingerprint {
		t.Errorf("%s: mapping fingerprint changed: want %s got %s", label, want.MappingFingerprint, got.MappingFingerprint)
	}
	if want.TargetsFingerprint != got.TargetsFingerprint {
		t.Errorf("%s: targets fingerprint changed: want %s got %s", label, want.TargetsFingerprint, got.TargetsFingerprint)
	}
}

func physAssertLegacyIdentities(t *testing.T, conn *sql.DB) {
	t.Helper()
	if got := physQueryInt64Column(t, conn, `SELECT id FROM bins ORDER BY id`); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Errorf("bin ids = %v, want [1 2 3 4]", got)
	}
	if got := physQueryInt64Column(t, conn, `SELECT id FROM parts ORDER BY id`); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Errorf("part ids = %v, want [1 2]", got)
	}
	want := map[int64]int64{1: 5, 2: 7, 3: 3, 4: 0}
	if got := physQueryPairs(t, conn, `SELECT id, quantity FROM part_assignments ORDER BY id`); !reflect.DeepEqual(got, want) {
		t.Errorf("assignment quantities = %v, want %v", got, want)
	}
}

func physFlagSetContains(flags []db.SystemFlag, key, value string) bool {
	for _, f := range flags {
		if f.Key == key && f.Value == value {
			return true
		}
	}
	return false
}

func physAssertAuditEntry(t *testing.T, store db.Store, action, entityType string) {
	t.Helper()
	logs, err := store.GetAllAuditLogs(context.Background())
	if err != nil {
		t.Fatalf("audit logs: %v", err)
	}
	for _, l := range logs {
		if l.ActionType == action && l.EntityType == entityType {
			return
		}
	}
	t.Errorf("no audit entry with action=%q entity_type=%q among %d entries", action, entityType, len(logs))
}

// physSnapshot captures everything a rejected or rolled-back restore must not
// change: the M1 compatibility baseline (three fingerprints plus raw rows and
// flags), the full physical graph, every system flag and the audit count.
type physSnapshot struct {
	baseline   hardware.CompatibilityBaseline
	physical   physPhysicalRows
	flags      map[string]string
	auditCount int
}

func physTakeSnapshot(t *testing.T, f *physFixture) physSnapshot {
	t.Helper()
	logs, err := f.store.GetAllAuditLogs(f.ctx)
	if err != nil {
		t.Fatalf("audit logs: %v", err)
	}
	return physSnapshot{
		baseline:   physCapture(t, f.store),
		physical:   physReadPhysicalRows(t, f.conn),
		flags:      physReadFlags(t, f.conn),
		auditCount: len(logs),
	}
}

func physAssertUnchanged(t *testing.T, label string, f *physFixture, want physSnapshot) {
	t.Helper()
	got := physTakeSnapshot(t, f)
	physAssertFingerprintsEqual(t, label, want.baseline, got.baseline)
	if !reflect.DeepEqual(want.baseline.Raw, got.baseline.Raw) {
		t.Errorf("%s: legacy rows changed", label)
	}
	if want.baseline.Flags != got.baseline.Flags {
		t.Errorf("%s: compatibility flags changed: want %+v got %+v", label, want.baseline.Flags, got.baseline.Flags)
	}
	if !reflect.DeepEqual(want.physical, got.physical) {
		t.Errorf("%s: physical graph changed: want %+v got %+v", label, want.physical, got.physical)
	}
	if !reflect.DeepEqual(want.flags, got.flags) {
		t.Errorf("%s: system flags changed: want %v got %v", label, want.flags, got.flags)
	}
	if want.auditCount != got.auditCount {
		t.Errorf("%s: audit count changed: want %d got %d", label, want.auditCount, got.auditCount)
	}
}

// TestPhysicalBackup_RoundTripsLegacyAndPhysicalGraph exports a default backup
// of a populated installation and restores it into a separate empty database:
// the archive must carry only the current manifest, and the restore must
// reproduce the legacy graph, the physical graph, the uploads and every flag
// exactly, with all original identities and quantities intact.
func TestPhysicalBackup_RoundTripsLegacyAndPhysicalGraph(t *testing.T) {
	source := physNewFixture(t, "roundtrip_source")
	source.initSettings()
	physSeedLegacy(t, source.conn)
	physSeedPhysical(t, source.conn)
	physWriteUpload(t, source.uploadsDir, "logo.png", "logo-bytes")
	physWriteUpload(t, source.uploadsDir, filepath.Join("nested", "spec.pdf"), "spec-bytes")

	before := physCapture(t, source.store)

	archive := source.export()

	entries := physZipEntries(t, archive)
	if _, ok := entries[currentStorageManifestName]; !ok {
		t.Errorf("default export is missing %s", currentStorageManifestName)
	}
	if _, ok := entries[legacyManifestName]; ok {
		t.Errorf("default export must not carry the legacy %s manifest", legacyManifestName)
	}
	for _, name := range []string{"uploads/logo.png", "uploads/nested/spec.pdf"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("default export is missing uploads entry %q", name)
		}
	}

	manifest := physDecodeStorageManifest(t, archive)
	if manifest.Version != physCurrentVersion {
		t.Errorf("storage manifest version = %q, want %q", manifest.Version, physCurrentVersion)
	}
	if manifest.ExportedAt.IsZero() {
		t.Errorf("storage manifest exported_at is not populated")
	}
	if got := len(manifest.Legacy.Bins); got != 4 {
		t.Errorf("captured legacy bins = %d, want 4", got)
	}
	if got := len(manifest.Legacy.Parts); got != 2 {
		t.Errorf("captured legacy parts = %d, want 2", got)
	}
	if got := len(manifest.Legacy.PartAssignments); got != 4 {
		t.Errorf("captured legacy assignments = %d, want 4", got)
	}
	if len(manifest.PhysicalStorage.Cabinets) != 2 || len(manifest.PhysicalStorage.Drawers) != 3 || len(manifest.PhysicalStorage.BinAttachments) != 3 || len(manifest.PhysicalStorage.LEDAllocations) != 2 {
		t.Errorf("captured physical graph = %d cabinets/%d drawers/%d attachments/%d LED allocations, want 2/3/3/2",
			len(manifest.PhysicalStorage.Cabinets), len(manifest.PhysicalStorage.Drawers), len(manifest.PhysicalStorage.BinAttachments), len(manifest.PhysicalStorage.LEDAllocations))
	}
	if !physFlagSetContains(manifest.SystemFlags, physCustomFlagKey, physCustomFlagValue) {
		t.Errorf("captured flags are missing the custom flag %q=%q: %+v", physCustomFlagKey, physCustomFlagValue, manifest.SystemFlags)
	}

	target := physNewFixture(t, "roundtrip_target")
	physWriteUpload(t, target.uploadsDir, "stale.txt", "stale")

	if err := target.restore(archive); err != nil {
		t.Fatalf("restore: %v", err)
	}

	after := physCapture(t, target.store)
	physAssertFingerprintsEqual(t, "roundtrip", before, after)
	if !reflect.DeepEqual(before.Raw, after.Raw) {
		t.Errorf("legacy rows changed across the roundtrip")
	}
	if before.Flags != after.Flags {
		t.Errorf("compatibility flags changed across the roundtrip: before=%+v after=%+v", before.Flags, after.Flags)
	}

	if want, got := physReadPhysicalRows(t, source.conn), physReadPhysicalRows(t, target.conn); !reflect.DeepEqual(want, got) {
		t.Errorf("physical graph changed across the roundtrip:\nwant=%+v\ngot=%+v", want, got)
	}

	physAssertLegacyIdentities(t, target.conn)
	if want, got := physReadFlags(t, source.conn), physReadFlags(t, target.conn); !reflect.DeepEqual(want, got) {
		t.Errorf("system flags changed across the roundtrip: want=%v got=%v", want, got)
	}

	targetUploads := physUploadsSnapshot(t, target.uploadsDir)
	if targetUploads["logo.png"] != "logo-bytes" || targetUploads["nested/spec.pdf"] != "spec-bytes" {
		t.Errorf("restored uploads are wrong: %v", targetUploads)
	}
	if _, ok := targetUploads["stale.txt"]; ok {
		t.Errorf("stale upload survived the restore: %v", targetUploads)
	}

	// A successful export records an audit entry with machine-readable action and
	// entity types; no prose is asserted.
	physAssertAuditEntry(t, source.store, "BACKUP", "SYSTEM")
}

// TestPhysicalBackup_CurrentEmptyPhysicalGraphRoundTrips verifies that a current
// 2.3 archive whose physical graph is complete but empty is still authoritative:
// restoring it into a populated installation replaces the physical graph and
// leaves it empty.
func TestPhysicalBackup_CurrentEmptyPhysicalGraphRoundTrips(t *testing.T) {
	source := physNewFixture(t, "empty_physical_source")
	source.initSettings()
	physSeedLegacy(t, source.conn)

	before := physCapture(t, source.store)
	archive := source.export()

	manifest := physDecodeStorageManifest(t, archive)
	if n := len(manifest.PhysicalStorage.Cabinets) + len(manifest.PhysicalStorage.Drawers) + len(manifest.PhysicalStorage.BinAttachments) + len(manifest.PhysicalStorage.LEDAllocations); n != 0 {
		t.Errorf("captured physical graph has %d rows, want 0", n)
	}

	target := physNewFixture(t, "empty_physical_target")
	target.initSettings()
	physSeedLegacy(t, target.conn)
	physSeedPhysical(t, target.conn)

	if err := target.restore(archive); err != nil {
		t.Fatalf("restore current empty-physical archive: %v", err)
	}

	rows := physReadPhysicalRows(t, target.conn)
	if len(rows.Cabinets) != 0 || len(rows.Drawers) != 0 || len(rows.Attachments) != 0 {
		t.Errorf("current restore left physical rows: %+v", rows)
	}
	physAssertFingerprintsEqual(t, "empty-physical roundtrip", before, physCapture(t, target.store))
}

func TestPhysicalBackup_OlderCurrentArchiveCannotDiscardDrawerLEDAllocations(t *testing.T) {
	target := physNewFixture(t, "older_current_without_led_allocations")
	target.initSettings()
	physSeedLegacy(t, target.conn)
	physSeedPhysical(t, target.conn)
	before := physTakeSnapshot(t, target)

	manifestBytes := physMarshalManifest(t, physBaseStorageManifest())
	var members map[string]json.RawMessage
	if err := json.Unmarshal([]byte(manifestBytes), &members); err != nil {
		t.Fatal(err)
	}
	var physical map[string]json.RawMessage
	if err := json.Unmarshal(members[storageManifestPhysicalField], &physical); err != nil {
		t.Fatal(err)
	}
	delete(physical, "drawer_led_allocations")
	members[storageManifestPhysicalField], _ = json.Marshal(physical)
	manifestBytesRaw, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	archive := physBuildZip(t, [][2]string{{currentStorageManifestName, string(manifestBytesRaw)}})

	err = target.restore(archive)
	var conflict *DrawerLEDAllocationConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("restore without allocation data = %v; want allocation-preservation conflict", err)
	}
	if after := physTakeSnapshot(t, target); !reflect.DeepEqual(before, after) {
		t.Fatalf("refused older restore changed existing data:\nbefore=%+v\nafter=%+v", before, after)
	}
}

// TestPhysicalBackup_LegacyRestoreIntoEmptyPhysicalGraph verifies that a legacy
// 1.0 archive still restores into an installation with no physical graph,
// leaving the physical tables empty.
func TestPhysicalBackup_LegacyRestoreIntoEmptyPhysicalGraph(t *testing.T) {
	target := physNewFixture(t, "legacy_into_empty")
	target.initSettings()

	if err := target.restore(buildRestoreZip(t, physLegacyManifest())); err != nil {
		t.Fatalf("legacy restore into an empty physical graph: %v", err)
	}

	if got := physQueryInt64Column(t, target.conn, `SELECT id FROM bins ORDER BY id`); !reflect.DeepEqual(got, []int64{1}) {
		t.Errorf("restored legacy bins = %v, want [1]", got)
	}
	rows := physReadPhysicalRows(t, target.conn)
	if len(rows.Cabinets) != 0 || len(rows.Drawers) != 0 || len(rows.Attachments) != 0 {
		t.Errorf("legacy restore created physical rows: %+v", rows)
	}
}

// TestPhysicalBackup_LegacyRestoreIntoPopulatedPhysicalGraphRefused verifies that
// a legacy 1.0 archive is refused before any mutation when the target already has
// a physical graph, so a legacy restore cannot silently discard or hide it.
func TestPhysicalBackup_LegacyRestoreIntoPopulatedPhysicalGraphRefused(t *testing.T) {
	target := physNewFixture(t, "legacy_into_populated")
	target.initSettings()
	physSeedLegacy(t, target.conn)
	physSeedPhysical(t, target.conn)
	physSeedUploadSentinels(t, target.uploadsDir)

	snap := physTakeSnapshot(t, target)
	uploads := physUploadsSnapshot(t, target.uploadsDir)

	if err := target.restore(buildRestoreZip(t, physLegacyManifest())); err == nil {
		t.Fatalf("restoring a legacy archive into a populated physical graph must be refused")
	}

	physAssertUnchanged(t, "legacy-into-populated", target, snap)
	if got := physUploadsSnapshot(t, target.uploadsDir); !reflect.DeepEqual(got, uploads) {
		t.Errorf("uploads changed by a refused legacy restore: want %v got %v", uploads, got)
	}
}

// TestPhysicalBackup_RejectsInvalidCurrentArchivesBeforeMutation verifies that
// every unsupported, ambiguous, malformed or physically inconsistent current
// manifest is rejected before any database, uploads or staging state changes.
func TestPhysicalBackup_RejectsInvalidCurrentArchivesBeforeMutation(t *testing.T) {
	const rejected = "rejected-current-archive"
	cases := []struct {
		name string
		// formatErr requires the rejection to be a *BackupFormatError (an
		// envelope-level defect); the physical defects are asserted only by
		// non-nil error plus unchanged state.
		formatErr bool
		archive   func(t *testing.T) []byte
	}{
		{
			name:      "unsupported version",
			formatErr: true,
			archive: func(t *testing.T) []byte {
				return physMutatedArchive(t, func(m *StorageManifest) { m.Version = "9.9" })
			},
		},
		{
			name:      "duplicate current manifest entry",
			formatErr: true,
			archive: func(t *testing.T) []byte {
				raw := physMarshalManifest(t, physBaseStorageManifest())
				return physBuildZip(t, [][2]string{
					{currentStorageManifestName, raw},
					{currentStorageManifestName, raw},
				})
			},
		},
		{
			name:      "mixed legacy and current manifests",
			formatErr: true,
			archive: func(t *testing.T) []byte {
				legacyRaw, err := json.Marshal(physLegacyManifest())
				if err != nil {
					t.Fatalf("marshal legacy manifest: %v", err)
				}
				return physBuildZip(t, [][2]string{
					{legacyManifestName, string(legacyRaw)},
					{currentStorageManifestName, physMarshalManifest(t, physBaseStorageManifest())},
				})
			},
		},
		{
			name:      "unknown manifest field",
			formatErr: true,
			archive: func(t *testing.T) []byte {
				raw := physMarshalManifest(t, physBaseStorageManifest())
				return physBuildZip(t, [][2]string{{currentStorageManifestName, raw[:len(raw)-1] + `,"zzz_unknown_member":42}`}})
			},
		},
		{
			name:      "duplicate manifest field",
			formatErr: true,
			archive: func(t *testing.T) []byte {
				raw := physMarshalManifest(t, physBaseStorageManifest())
				return physBuildZip(t, [][2]string{{currentStorageManifestName, physDuplicateFirstMember(t, raw)}})
			},
		},
		{
			name: "attachment references unknown bin",
			archive: func(t *testing.T) []byte {
				return physMutatedArchive(t, func(m *StorageManifest) {
					m.PhysicalStorage.BinAttachments = append(m.PhysicalStorage.BinAttachments, db.BinDrawerAttachment{
						BinID: 999, DrawerID: 1, X: 0, Y: 0, Width: 1, Height: 1,
					})
				})
			},
		},
		{
			name: "duplicate attachment for one bin",
			archive: func(t *testing.T) []byte {
				return physMutatedArchive(t, func(m *StorageManifest) {
					m.PhysicalStorage.BinAttachments = append(m.PhysicalStorage.BinAttachments, m.PhysicalStorage.BinAttachments[0])
				})
			},
		},
		{
			name: "two cabinets share one controller",
			archive: func(t *testing.T) []byte {
				return physMutatedArchive(t, func(m *StorageManifest) {
					m.PhysicalStorage.Cabinets = append(m.PhysicalStorage.Cabinets, db.Cabinet{
						ID: 3, Name: "Second", ControllerID: sql.NullInt64{Int64: 1, Valid: true}, CoordinateSystem: "grid",
					})
				})
			},
		},
		{
			name: "non-positive drawer geometry",
			archive: func(t *testing.T) []byte {
				return physMutatedArchive(t, func(m *StorageManifest) { m.PhysicalStorage.Drawers[0].Width = 0 })
			},
		},
		{
			name: "nonfinite drawer geometry",
			archive: func(t *testing.T) []byte {
				// JSON cannot encode Inf/NaN, so a finite-exceeding literal stands in
				// for a nonfinite geometry value.
				base := physBaseStorageManifest()
				base.PhysicalStorage.Drawers[0].Width = 123456.5
				raw := physMarshalManifest(t, base)
				if !strings.Contains(raw, "123456.5") {
					t.Fatalf("geometry sentinel value missing from marshaled manifest")
				}
				raw = strings.Replace(raw, "123456.5", "1e400", 1)
				return physBuildZip(t, [][2]string{{currentStorageManifestName, raw}})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := physNewFixture(t, "reject")
			target.initSettings()
			physSeedLegacy(t, target.conn)
			physSeedPhysical(t, target.conn)
			physSeedUploadSentinels(t, target.uploadsDir)

			snap := physTakeSnapshot(t, target)
			uploads := physUploadsSnapshot(t, target.uploadsDir)

			err := target.restore(tc.archive(t))
			if err == nil {
				t.Fatalf("invalid current archive was accepted")
			}
			if tc.formatErr {
				var formatErr *BackupFormatError
				if !errors.As(err, &formatErr) {
					t.Errorf("expected a *BackupFormatError, got %T: %v", err, err)
				}
			}
			physAssertUnchanged(t, rejected, target, snap)
			if got := physUploadsSnapshot(t, target.uploadsDir); !reflect.DeepEqual(got, uploads) {
				t.Errorf("uploads changed by a rejected current archive: want %v got %v", uploads, got)
			}
		})
	}
}

// TestPhysicalBackup_InjectedPhysicalWriteFailureRollsBack injects a real
// database failure into the physical write phase and requires the whole restore
// transaction to roll back, so a partially rewritten legacy or physical graph
// cannot survive.
func TestPhysicalBackup_InjectedPhysicalWriteFailureRollsBack(t *testing.T) {
	source := physNewFixture(t, "inject_source")
	source.initSettings()
	physSeedLegacy(t, source.conn)
	physSeedPhysical(t, source.conn)
	archive := source.export()

	target := physNewFixture(t, "inject_target")
	target.initSettings()
	physSeedLegacy(t, target.conn)
	physSeedPhysical(t, target.conn)
	physWriteUpload(t, target.uploadsDir, "sentinel.txt", "keep-me")

	snap := physTakeSnapshot(t, target)

	// The first drawers insert aborts, so the restore fails after it has begun
	// rewriting the graph: any legacy rows already cleared/rewritten and any
	// physical rows already written must roll back with it.
	physExec(t, target.conn, `CREATE TRIGGER phys_inject_physical_failure BEFORE INSERT ON drawers BEGIN SELECT RAISE(ABORT, 'injected physical write failure'); END`)

	if err := target.restore(archive); err == nil {
		t.Fatalf("restore succeeded despite the injected physical write failure")
	}

	physAssertUnchanged(t, "injected-physical-write-failure", target, snap)
	if _, statErr := os.Stat(filepath.Join(target.uploadsDir, "sentinel.txt")); statErr != nil {
		t.Errorf("upload sentinel lost after a rolled-back restore: %v", statErr)
	}
}
