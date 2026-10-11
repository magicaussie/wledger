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
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// These tests cover the completeness contract of the current storage envelope:
// a current (2.0) archive must declare every member of the envelope, the
// physical storage section and the embedded legacy manifest that its format
// requires, plus a coordinate space consistent with the captured flag. An
// incomplete archive must be rejected before any cleanup or mutation, because
// reading a missing section as "empty" would clear a populated installation.
//
// Every rejection case asserts the machine-readable format code and a complete
// before/after proof: M1 fingerprints and raw legacy rows, the physical graph,
// every flag, the audit count, a stale inventory row and the uploads directory
// (including the abandoned staging sentinel, which only survives while
// validation still precedes cleanup).

type smvFixture struct {
	t          *testing.T
	conn       *sql.DB
	store      db.Store
	svc        Service
	uploadsDir string
	ctx        context.Context
}

func smvNewFixture(t *testing.T, name string) *smvFixture {
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
	return &smvFixture{
		t:          t,
		conn:       conn,
		store:      store,
		svc:        NewService(conn, store, uploadsDir, slog.New(slog.NewTextHandler(io.Discard, nil))),
		uploadsDir: uploadsDir,
		ctx:        context.Background(),
	}
}

func (f *smvFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.conn.Exec(query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

// seed fills the fixture with one small but complete installation: a legacy
// graph, a physical graph, the coordinate and migration flags, a flag outside
// the M1 tracked set and an audit row.
func (f *smvFixture) seed() {
	f.t.Helper()
	if err := f.store.InitSettings(f.ctx); err != nil {
		f.t.Fatalf("init settings: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO controllers (id, name, ip_address, port, led_count) VALUES (1, 'Ctrl', '10.0.0.1', 80, 32)`,
		`INSERT INTO containers (id, name, controller_id, segment_id, led_start, led_count) VALUES (1, 'String', 1, 0, 0, 16)`,
		`INSERT INTO bins (id, name, container_id, led_index, width, grid_x, grid_y) VALUES (1, 'bin1', 1, 0, 1, 0, 0)`,
		`INSERT INTO parts (id, name) VALUES (1, 'part1')`,
		`INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (1, 1, 1, 4)`,
		`INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived) VALUES (1, 'Cab', 1, 'grid', 2, 3, 0, 0)`,
		`INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived) VALUES (1, 1, 'Drawer', 0, 0, 0, 10, 5, 0)`,
		`INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order) VALUES (1, 1, 0, 0, 2, 3, 0)`,
	} {
		f.exec(stmt)
	}
	for _, flag := range [][2]string{
		{ledspace.FlagKey, ledspace.Segment},
		{hardware.Migration005FlagKey, "true"},
		{"smv_custom_flag", "custom-value"},
	} {
		f.exec(`INSERT INTO system_flags (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, flag[0], flag[1])
	}
	if err := f.store.CreateAuditLog(f.ctx, db.CreateAuditLogParams{
		ActionType: "CREATE", EntityType: "PART", EntityID: 1, NewValue: []byte(`{"seed":true}`),
	}); err != nil {
		f.t.Fatalf("seed audit: %v", err)
	}
}

// seedStalePart adds an inventory row no archive in these tests carries, so a
// rejected archive is proven to have left the target's own rows alone and an
// accepted one is proven to have replaced them.
func (f *smvFixture) seedStalePart() {
	f.t.Helper()
	f.exec(`INSERT INTO parts (id, name) VALUES (999, 'stale-part')`)
}

// seedUploadSentinels writes the filesystem evidence a rejected archive must not
// disturb. The abandoned staging directory is decisive: validation that ran
// after cleanup would already have removed it.
func (f *smvFixture) seedUploadSentinels() {
	f.t.Helper()
	smvWriteFile(f.t, f.uploadsDir, "sentinel.txt", "keep-me")
	smvWriteFile(f.t, f.uploadsDir, filepath.Join(".uploads_bak_keep", "precious.txt"), "keep-me-too")
	smvWriteFile(f.t, f.uploadsDir, filepath.Join(".restore_tmp_leftover", "leftover.txt"), "keep-me-three")
}

func smvWriteFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create dir for %q: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %q: %v", rel, err)
	}
}

func (f *smvFixture) export() []byte {
	f.t.Helper()
	var buf bytes.Buffer
	if err := f.svc.Export(f.ctx, &buf); err != nil {
		f.t.Fatalf("export: %v", err)
	}
	return buf.Bytes()
}

func (f *smvFixture) restore(archive []byte) error {
	f.t.Helper()
	return f.svc.Restore(f.ctx, bytes.NewReader(archive), int64(len(archive)))
}

type smvPhysicalRows struct {
	cabinets    []db.Cabinet
	drawers     []db.Drawer
	attachments []db.BinDrawerAttachment
}

// smvSnapshot is everything a rejected archive must leave unchanged and an
// accepted archive must replace.
type smvSnapshot struct {
	baseline   hardware.CompatibilityBaseline
	physical   smvPhysicalRows
	flags      map[string]string
	auditCount int
	staleParts int64
}

func (f *smvFixture) snapshot() smvSnapshot {
	f.t.Helper()
	baseline, err := hardware.CaptureCompatibilityBaseline(f.ctx, f.store)
	if err != nil {
		f.t.Fatalf("capture compatibility baseline: %v", err)
	}
	logs, err := f.store.GetAllAuditLogs(f.ctx)
	if err != nil {
		f.t.Fatalf("audit logs: %v", err)
	}
	return smvSnapshot{
		baseline:   baseline,
		physical:   smvReadPhysical(f.t, f.conn),
		flags:      smvReadFlags(f.t, f.conn),
		auditCount: len(logs),
		staleParts: smvCount(f.t, f.conn, `SELECT COUNT(*) FROM parts WHERE id = 999`),
	}
}

func (f *smvFixture) assertUnchanged(label string, want smvSnapshot) {
	f.t.Helper()
	got := f.snapshot()
	if want.baseline.InventoryFingerprint != got.baseline.InventoryFingerprint {
		f.t.Errorf("%s: inventory fingerprint changed: want %v got %v", label, want.baseline.InventoryFingerprint, got.baseline.InventoryFingerprint)
	}
	if want.baseline.MappingFingerprint != got.baseline.MappingFingerprint {
		f.t.Errorf("%s: mapping fingerprint changed: want %v got %v", label, want.baseline.MappingFingerprint, got.baseline.MappingFingerprint)
	}
	if want.baseline.TargetsFingerprint != got.baseline.TargetsFingerprint {
		f.t.Errorf("%s: targets fingerprint changed: want %v got %v", label, want.baseline.TargetsFingerprint, got.baseline.TargetsFingerprint)
	}
	if !reflect.DeepEqual(want.baseline.Raw, got.baseline.Raw) {
		f.t.Errorf("%s: legacy rows changed", label)
	}
	if want.baseline.Flags != got.baseline.Flags {
		f.t.Errorf("%s: compatibility flags changed: want %+v got %+v", label, want.baseline.Flags, got.baseline.Flags)
	}
	if !reflect.DeepEqual(want.physical, got.physical) {
		f.t.Errorf("%s: physical graph changed: want %+v got %+v", label, want.physical, got.physical)
	}
	if !reflect.DeepEqual(want.flags, got.flags) {
		f.t.Errorf("%s: system flags changed: want %v got %v", label, want.flags, got.flags)
	}
	if want.auditCount != got.auditCount {
		f.t.Errorf("%s: audit count changed: want %d got %d", label, want.auditCount, got.auditCount)
	}
	if want.staleParts != got.staleParts {
		f.t.Errorf("%s: stale inventory row count changed: want %d got %d", label, want.staleParts, got.staleParts)
	}
}

func smvReadPhysical(t *testing.T, conn *sql.DB) smvPhysicalRows {
	t.Helper()
	var rows smvPhysicalRows

	cabinetRows, err := conn.QueryContext(context.Background(),
		`SELECT id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived, created_at, updated_at FROM cabinets ORDER BY id`)
	if err != nil {
		t.Fatalf("query cabinets: %v", err)
	}
	for cabinetRows.Next() {
		var c db.Cabinet
		if err := cabinetRows.Scan(&c.ID, &c.Name, &c.ControllerID, &c.CoordinateSystem, &c.LayoutRevision,
			&c.ElectricalRevision, &c.DisplayOrder, &c.Archived, &c.CreatedAt, &c.UpdatedAt); err != nil {
			cabinetRows.Close()
			t.Fatalf("scan cabinet: %v", err)
		}
		rows.cabinets = append(rows.cabinets, c)
	}
	cabinetRows.Close()

	drawerRows, err := conn.QueryContext(context.Background(),
		`SELECT id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at FROM drawers ORDER BY id`)
	if err != nil {
		t.Fatalf("query drawers: %v", err)
	}
	for drawerRows.Next() {
		var d db.Drawer
		if err := drawerRows.Scan(&d.ID, &d.CabinetID, &d.Label, &d.DisplayOrder, &d.X, &d.Y,
			&d.Width, &d.Height, &d.Archived, &d.CreatedAt, &d.UpdatedAt); err != nil {
			drawerRows.Close()
			t.Fatalf("scan drawer: %v", err)
		}
		rows.drawers = append(rows.drawers, d)
	}
	drawerRows.Close()

	attachmentRows, err := conn.QueryContext(context.Background(),
		`SELECT bin_id, drawer_id, x, y, width, height, display_order, created_at, updated_at FROM bin_drawer_attachments ORDER BY bin_id`)
	if err != nil {
		t.Fatalf("query bin attachments: %v", err)
	}
	for attachmentRows.Next() {
		var a db.BinDrawerAttachment
		if err := attachmentRows.Scan(&a.BinID, &a.DrawerID, &a.X, &a.Y, &a.Width, &a.Height,
			&a.DisplayOrder, &a.CreatedAt, &a.UpdatedAt); err != nil {
			attachmentRows.Close()
			t.Fatalf("scan bin attachment: %v", err)
		}
		rows.attachments = append(rows.attachments, a)
	}
	attachmentRows.Close()

	return rows
}

func smvReadFlags(t *testing.T, conn *sql.DB) map[string]string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `SELECT key, value FROM system_flags`)
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

func smvCount(t *testing.T, conn *sql.DB, query string) int64 {
	t.Helper()
	var n int64
	if err := conn.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func smvUploads(t *testing.T, dir string) map[string]string {
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

func smvZipEntries(t *testing.T, archive []byte) map[string][]byte {
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

func smvArchive(t *testing.T, manifest string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, err := zw.Create(currentStorageManifestName)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write([]byte(manifest)); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// smvBaseEnvelope exports a real current envelope from a seeded source fixture,
// so each case below starts from a genuinely complete archive and carries only
// the defect under test.
func smvBaseEnvelope(t *testing.T) (StorageManifest, json.RawMessage) {
	t.Helper()
	source := smvNewFixture(t, "smv_source")
	source.seed()
	entries := smvZipEntries(t, source.export())
	raw, ok := entries[currentStorageManifestName]
	if !ok {
		t.Fatalf("exported archive is missing %s", currentStorageManifestName)
	}
	var env StorageManifest
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode exported envelope: %v", err)
	}
	return env, raw
}

func smvObjectMembers(t *testing.T, object json.RawMessage) map[string]string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(object))
	open, err := dec.Token()
	if err != nil {
		t.Fatalf("read object start: %v", err)
	}
	if delim, ok := open.(json.Delim); !ok || delim != '{' {
		t.Fatalf("value is not a JSON object: %s", object)
	}
	members := map[string]string{}
	for dec.More() {
		nameTok, err := dec.Token()
		if err != nil {
			t.Fatalf("read member name: %v", err)
		}
		name, ok := nameTok.(string)
		if !ok {
			t.Fatalf("member name is not a string: %v", nameTok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.Fatalf("read member %q: %v", name, err)
		}
		members[name] = string(raw)
	}
	return members
}

func smvRender(members map[string]string) string {
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, strconv.Quote(name)+":"+members[name])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// smvRebuild re-renders an envelope after a mutation. Deleting a member from the
// map omits it, which is how the omission cases are built; a repeated member is
// built textually because a JSON object cannot hold one key twice.
func smvRebuild(t *testing.T, envelope json.RawMessage, mutate func(members map[string]string)) string {
	t.Helper()
	members := smvObjectMembers(t, envelope)
	mutate(members)
	return smvRender(members)
}

// smvNestedMutation rewrites one member of the physical storage section.
func smvNestedMutation(t *testing.T, envelope json.RawMessage, mutate func(members map[string]string)) string {
	t.Helper()
	return smvRebuild(t, envelope, func(top map[string]string) {
		physical := smvObjectMembers(t, json.RawMessage(top[storageManifestPhysicalField]))
		mutate(physical)
		top[storageManifestPhysicalField] = smvRender(physical)
	})
}

// smvLegacyMutation rewrites one member of the embedded legacy manifest.
func smvLegacyMutation(t *testing.T, envelope json.RawMessage, mutate func(members map[string]string)) string {
	t.Helper()
	return smvRebuild(t, envelope, func(top map[string]string) {
		legacy := smvObjectMembers(t, json.RawMessage(top[storageManifestLegacyField]))
		mutate(legacy)
		top[storageManifestLegacyField] = smvRender(legacy)
	})
}

// smvDuplicateMember repeats one member of a compact JSON object in place.
func smvDuplicateMember(t *testing.T, object json.RawMessage, key string) string {
	t.Helper()
	trimmed := strings.TrimSpace(string(object))
	members := smvObjectMembers(t, json.RawMessage(trimmed))
	value, ok := members[key]
	if !ok {
		t.Fatalf("object has no member %q to duplicate: %s", key, trimmed)
	}
	return "{" + strconv.Quote(key) + ":" + value + "," + strings.TrimPrefix(trimmed, "{")
}

func smvFlagsRaw(t *testing.T, flags []db.SystemFlag) string {
	t.Helper()
	raw, err := json.Marshal(flags)
	if err != nil {
		t.Fatalf("marshal flags: %v", err)
	}
	return string(raw)
}

func smvWithoutFlag(flags []db.SystemFlag, key string) []db.SystemFlag {
	out := make([]db.SystemFlag, 0, len(flags))
	for _, f := range flags {
		if f.Key != key {
			out = append(out, f)
		}
	}
	return out
}

func smvWithFlag(flags []db.SystemFlag, key, value string) []db.SystemFlag {
	out := make([]db.SystemFlag, 0, len(flags)+1)
	for _, f := range flags {
		if f.Key == key {
			continue
		}
		out = append(out, f)
	}
	return append(out, db.SystemFlag{Key: key, Value: value})
}

// smvRejectedTarget seeds a target whose populated state a rejected archive must
// leave exactly as it was, and returns it with its snapshot and uploads.
func smvRejectedTarget(t *testing.T, name string) (*smvFixture, smvSnapshot, map[string]string) {
	t.Helper()
	target := smvNewFixture(t, name)
	target.seed()
	target.seedStalePart()
	target.seedUploadSentinels()
	snap := target.snapshot()
	return target, snap, smvUploads(t, target.uploadsDir)
}

func TestStorageManifest_RejectsIncompleteEnvelopesBeforeMutation(t *testing.T) {
	_, base := smvBaseEnvelope(t)

	cases := []struct {
		name     string
		wantCode BackupFormatCode
		raw      func(t *testing.T) string
	}{
		{
			name:     "envelope omits version",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { delete(m, "version") })
			},
		},
		{
			name:     "envelope omits exported_at",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { delete(m, "exported_at") })
			},
		},
		{
			name:     "envelope omits legacy",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { delete(m, "legacy") })
			},
		},
		{
			name:     "envelope omits physical_storage",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { delete(m, "physical_storage") })
			},
		},
		{
			name:     "envelope omits system_flags",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { delete(m, "system_flags") })
			},
		},
		{
			name:     "physical_storage is null",
			wantCode: FormatInvalidManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { m[storageManifestPhysicalField] = "null" })
			},
		},
		{
			name:     "physical_storage is not an object",
			wantCode: FormatInvalidManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { m[storageManifestPhysicalField] = "[]" })
			},
		},
		{
			name:     "physical_storage omits cabinets",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvNestedMutation(t, base, func(m map[string]string) { delete(m, "cabinets") })
			},
		},
		{
			name:     "physical_storage omits drawers",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvNestedMutation(t, base, func(m map[string]string) { delete(m, "drawers") })
			},
		},
		{
			name:     "physical_storage omits bin_attachments",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvNestedMutation(t, base, func(m map[string]string) { delete(m, "bin_attachments") })
			},
		},
		{
			name:     "physical_storage cabinets is null",
			wantCode: FormatInvalidManifestField,
			raw: func(t *testing.T) string {
				return smvNestedMutation(t, base, func(m map[string]string) { m["cabinets"] = "null" })
			},
		},
		{
			name:     "physical_storage drawers is not an array",
			wantCode: FormatInvalidManifestField,
			raw: func(t *testing.T) string {
				return smvNestedMutation(t, base, func(m map[string]string) { m["drawers"] = "{}" })
			},
		},
		{
			name:     "physical_storage has an unknown member",
			wantCode: FormatUnknownManifestField,
			raw: func(t *testing.T) string {
				return smvNestedMutation(t, base, func(m map[string]string) { m["zzz_unknown"] = "1" })
			},
		},
		{
			name:     "physical_storage repeats drawers",
			wantCode: FormatDuplicateManifestField,
			raw: func(t *testing.T) string {
				members := smvObjectMembers(t, base)
				physical := smvDuplicateMember(t, json.RawMessage(members[storageManifestPhysicalField]), "drawers")
				members[storageManifestPhysicalField] = physical
				return smvRender(members)
			},
		},
		{
			name:     "system_flags is null",
			wantCode: FormatInvalidManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { m[storageManifestSystemFlagsField] = "null" })
			},
		},
		{
			name:     "system_flags is not an array",
			wantCode: FormatInvalidManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { m[storageManifestSystemFlagsField] = `{"key":"x"}` })
			},
		},
		{
			name:     "legacy omits users",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvLegacyMutation(t, base, func(m map[string]string) { delete(m, "users") })
			},
		},
		{
			name:     "legacy omits settings",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvLegacyMutation(t, base, func(m map[string]string) { delete(m, "settings") })
			},
		},
		{
			name:     "legacy omits bins",
			wantCode: FormatMissingManifestField,
			raw: func(t *testing.T) string {
				return smvLegacyMutation(t, base, func(m map[string]string) { delete(m, "bins") })
			},
		},
		{
			name:     "legacy has an unknown member",
			wantCode: FormatUnknownManifestField,
			raw: func(t *testing.T) string {
				return smvLegacyMutation(t, base, func(m map[string]string) { m["zzz_unknown"] = "1" })
			},
		},
		{
			name:     "legacy repeats a member",
			wantCode: FormatDuplicateManifestField,
			raw: func(t *testing.T) string {
				members := smvObjectMembers(t, base)
				legacy := smvDuplicateMember(t, json.RawMessage(members[storageManifestLegacyField]), binIndexSpaceField)
				members[storageManifestLegacyField] = legacy
				return smvRender(members)
			},
		},
		{
			name:     "legacy is not an object",
			wantCode: FormatInvalidManifestField,
			raw: func(t *testing.T) string {
				return smvRebuild(t, base, func(m map[string]string) { m[storageManifestLegacyField] = "null" })
			},
		},
		{
			name:     "unsupported coordinate marker",
			wantCode: FormatUnsupportedCoordinateSpace,
			raw: func(t *testing.T) string {
				return smvLegacyMutation(t, base, func(m map[string]string) { m[binIndexSpaceField] = `"bogus"` })
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, before, uploads := smvRejectedTarget(t, "smv_reject")

			err := target.restore(smvArchive(t, tc.raw(t)))
			if err == nil {
				t.Fatal("incomplete archive was accepted")
			}
			var formatErr *BackupFormatError
			if !errors.As(err, &formatErr) {
				t.Fatalf("expected *BackupFormatError, got %T: %v", err, err)
			}
			if formatErr.Code != tc.wantCode {
				t.Errorf("format code = %q, want %q (%v)", formatErr.Code, tc.wantCode, err)
			}
			target.assertUnchanged(tc.name, before)
			if got := smvUploads(t, target.uploadsDir); !reflect.DeepEqual(got, uploads) {
				t.Errorf("uploads changed by a rejected archive: want %v got %v", uploads, got)
			}
		})
	}
}

func TestStorageManifest_RejectsContradictoryCoordinateSpace(t *testing.T) {
	_, base := smvBaseEnvelope(t)
	env := func() StorageManifest {
		var parsed StorageManifest
		if err := json.Unmarshal(base, &parsed); err != nil {
			t.Fatalf("decode base envelope: %v", err)
		}
		return parsed
	}

	cases := []struct {
		name     string
		wantCode BackupFormatCode
		raw      func(t *testing.T) string
	}{
		{
			name:     "marker segment with a drawer flag",
			wantCode: FormatInconsistentCoordinateSpace,
			raw: func(t *testing.T) string {
				flags := smvWithFlag(env().SystemFlags, ledspace.FlagKey, ledspace.Drawer)
				return smvRebuild(t, base, func(m map[string]string) { m[storageManifestSystemFlagsField] = smvFlagsRaw(t, flags) })
			},
		},
		{
			name:     "marker drawer with a segment flag",
			wantCode: FormatInconsistentCoordinateSpace,
			raw: func(t *testing.T) string {
				return smvLegacyMutation(t, base, func(m map[string]string) { m[binIndexSpaceField] = `"drawer"` })
			},
		},
		{
			name:     "marker unresolved with a segment flag",
			wantCode: FormatInconsistentCoordinateSpace,
			raw: func(t *testing.T) string {
				return smvLegacyMutation(t, base, func(m map[string]string) { m[binIndexSpaceField] = `"unresolved"` })
			},
		},
		{
			name:     "marker segment with an unresolved flag",
			wantCode: FormatInconsistentCoordinateSpace,
			raw: func(t *testing.T) string {
				flags := smvWithFlag(env().SystemFlags, ledspace.FlagKey, ledspace.Unresolved)
				return smvRebuild(t, base, func(m map[string]string) { m[storageManifestSystemFlagsField] = smvFlagsRaw(t, flags) })
			},
		},
		{
			name:     "unsupported coordinate flag",
			wantCode: FormatUnsupportedCoordinateSpace,
			raw: func(t *testing.T) string {
				flags := smvWithFlag(env().SystemFlags, ledspace.FlagKey, "bogus")
				return smvRebuild(t, base, func(m map[string]string) { m[storageManifestSystemFlagsField] = smvFlagsRaw(t, flags) })
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, before, uploads := smvRejectedTarget(t, "smv_space")

			err := target.restore(smvArchive(t, tc.raw(t)))
			if err == nil {
				t.Fatal("an ambiguous coordinate space was accepted")
			}
			var formatErr *BackupFormatError
			if !errors.As(err, &formatErr) {
				t.Fatalf("expected *BackupFormatError, got %T: %v", err, err)
			}
			if formatErr.Code != tc.wantCode {
				t.Errorf("format code = %q, want %q (%v)", formatErr.Code, tc.wantCode, err)
			}
			target.assertUnchanged(tc.name, before)
			if got := smvUploads(t, target.uploadsDir); !reflect.DeepEqual(got, uploads) {
				t.Errorf("uploads changed by a rejected archive: want %v got %v", uploads, got)
			}
		})
	}
}

func TestStorageManifest_AcceptsCompleteEnvelopesAndReplacesTarget(t *testing.T) {
	parsed, base := smvBaseEnvelope(t)

	// A legacy section that is empty in its own format is emitted as null; it is
	// present, which is what completeness requires, and must not be rejected.
	legacyMembers := smvObjectMembers(t, json.RawMessage(smvObjectMembers(t, base)[storageManifestLegacyField]))
	if got, ok := legacyMembers["users"]; !ok || strings.TrimSpace(got) != "null" {
		t.Errorf("expected an empty legacy section to be present as null, got present=%v value=%s", ok, got)
	}

	withoutFlag := func() string {
		return smvRebuild(t, base, func(m map[string]string) {
			m[storageManifestSystemFlagsField] = smvFlagsRaw(t, smvWithoutFlag(parsed.SystemFlags, ledspace.FlagKey))
		})
	}

	cases := []struct {
		name      string
		raw       func(t *testing.T) string
		wantSpace string
		warnOrNil bool
	}{
		{
			name:      "exported envelope",
			wantSpace: ledspace.Segment,
			raw:       func(t *testing.T) string { return string(base) },
		},
		{
			name: "absent coordinate flag means segment",
			raw:  func(t *testing.T) string { return withoutFlag() },
		},
		{
			name:      "absent marker and absent flag",
			warnOrNil: true,
			raw: func(t *testing.T) string {
				return smvLegacyMutation(t, json.RawMessage(withoutFlag()), func(m map[string]string) { delete(m, binIndexSpaceField) })
			},
		},
		{
			name:      "drawer marker with a drawer flag",
			wantSpace: ledspace.Drawer,
			raw: func(t *testing.T) string {
				flags := smvWithFlag(parsed.SystemFlags, ledspace.FlagKey, ledspace.Drawer)
				return smvRebuild(t, base, func(m map[string]string) {
					m[storageManifestSystemFlagsField] = smvFlagsRaw(t, flags)
					legacy := smvObjectMembers(t, json.RawMessage(m[storageManifestLegacyField]))
					legacy[binIndexSpaceField] = `"drawer"`
					m[storageManifestLegacyField] = smvRender(legacy)
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := smvNewFixture(t, "smv_accept")
			target.seed()
			target.seedStalePart()
			target.seedUploadSentinels()
			// A flag the archive does not carry, so an accepted restore must prove the
			// flag set was replaced exactly rather than merged onto the target's.
			target.exec(`INSERT INTO system_flags (key, value) VALUES ('smv_target_only_flag', 'should-vanish')`)

			err := target.restore(smvArchive(t, tc.raw(t)))
			var formatErr *BackupFormatError
			if errors.As(err, &formatErr) {
				t.Fatalf("a complete envelope was rejected as a format error: %v", err)
			}
			var committed *RestoreCommittedError
			if err != nil && !errors.As(err, &committed) {
				t.Fatalf("complete envelope failed to restore: %v", err)
			}
			if tc.warnOrNil && err == nil {
				t.Logf("restore reported no warning for a space that is not explicitly declared")
			}

			// The archive was accepted, so the target's own state is replaced: the
			// stale inventory row is gone and the flags match the archive exactly.
			if got := smvCount(t, target.conn, `SELECT COUNT(*) FROM parts WHERE id = 999`); got != 0 {
				t.Errorf("stale inventory row survived an accepted restore: %d rows", got)
			}
			want := map[string]string{hardware.Migration005FlagKey: "true", "smv_custom_flag": "custom-value"}
			if tc.wantSpace != "" {
				want[ledspace.FlagKey] = tc.wantSpace
			}
			if flags := smvReadFlags(t, target.conn); !reflect.DeepEqual(flags, want) {
				t.Errorf("restored flags = %v, want %v (the flag set must be replaced exactly)", flags, want)
			}

			// The restore ran to completion, so staging was cleaned and a preserved
			// recovery directory was left alone.
			uploads := smvUploads(t, target.uploadsDir)
			if _, ok := uploads[filepath.Join(".restore_tmp_leftover", "leftover.txt")]; ok {
				t.Errorf("abandoned staging directory survived an accepted restore: %v", uploads)
			}
			if _, ok := uploads[filepath.Join(".uploads_bak_keep", "precious.txt")]; !ok {
				t.Errorf("recovery directory was removed by an accepted restore: %v", uploads)
			}
		})
	}
}
