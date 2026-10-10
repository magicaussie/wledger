package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

func newBackupService(t *testing.T) (*sql.DB, db.Store, Service, string, func()) {
	t.Helper()
	database, s, dbCleanup := setupTestDB(t)
	uploadsDir, fsCleanup := setupTestUploads(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	cleanup := func() {
		fsCleanup()
		dbCleanup()
	}
	return database, s, svc, uploadsDir, cleanup
}

func minimalSettings() db.Setting {
	now := sql.NullTime{Time: time.Now(), Valid: true}
	return db.Setting{CreatedAt: now, UpdatedAt: now}
}

// buildRawZip writes an arbitrary restore_data.json payload into a zip.
func buildRawZip(t *testing.T, jsonStr string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	f, err := zw.Create("restore_data.json")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := f.Write([]byte(jsonStr)); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func mustJSONEqual(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w interface{}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("restored value is not valid JSON: %v (%q)", err, string(got))
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("expected value is not valid JSON: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("JSON mismatch: got %s want %s", string(got), want)
	}
}

func auditByID(t *testing.T, s db.Store, ctx context.Context) map[int64]db.GetAllAuditLogsRow {
	t.Helper()
	rows, err := s.GetAllAuditLogs(ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs: %v", err)
	}
	out := make(map[int64]db.GetAllAuditLogsRow, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

// TestRestore_AuditValuesAllJSONTypes verifies that every JSON value type in an
// audit old_value/new_value restores correctly (this is the historical archive
// shape, so it also covers backward compatibility).
func TestRestore_AuditValuesAllJSONTypes(t *testing.T) {
	_, s, svc, _, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()

	manifest := Manifest{
		Version:  "1.0",
		Settings: minimalSettings(),
		AuditLogs: []AuditLogEntry{
			{ID: 1, ActionType: "UPDATE", EntityType: "PART", EntityID: 1,
				OldValue: json.RawMessage(`{"a":1}`), NewValue: json.RawMessage(`[1,2,3]`)},
			{ID: 2, ActionType: "CREATE", EntityType: "BIN", EntityID: 2,
				OldValue: json.RawMessage(`"hello"`), NewValue: json.RawMessage(`42`)},
			{ID: 3, ActionType: "DELETE", EntityType: "PART", EntityID: 3,
				OldValue: json.RawMessage(`true`), NewValue: json.RawMessage(`null`)},
			{ID: 4, ActionType: "UPDATE", EntityType: "PART", EntityID: 4,
				NewValue: json.RawMessage(`{"nested":{"x":true}}`)},
		},
	}
	zipBytes := buildRestoreZip(t, manifest)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	got := auditByID(t, s, ctx)
	if len(got) != 4 {
		t.Fatalf("expected 4 audit rows, got %d", len(got))
	}
	mustJSONEqual(t, got[1].OldValue, `{"a":1}`)
	mustJSONEqual(t, got[1].NewValue, `[1,2,3]`)
	mustJSONEqual(t, got[2].OldValue, `"hello"`)
	mustJSONEqual(t, got[2].NewValue, `42`)
	mustJSONEqual(t, got[3].OldValue, `true`)
	// JSON null is preserved as a JSON null value; an absent value is SQL NULL,
	// which the read query coalesces to an empty object.
	mustJSONEqual(t, got[3].NewValue, `null`)
	mustJSONEqual(t, got[4].OldValue, `{}`)
	mustJSONEqual(t, got[4].NewValue, `{"nested":{"x":true}}`)
}

func TestRestore_AuditEmptyHistory(t *testing.T) {
	_, s, svc, _, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()

	manifest := Manifest{Version: "1.0", Settings: minimalSettings()}
	zipBytes := buildRestoreZip(t, manifest)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore with empty audit history failed: %v", err)
	}
	rows, _ := s.GetAllAuditLogs(ctx)
	if len(rows) != 0 {
		t.Fatalf("expected no audit rows, got %d", len(rows))
	}
}

func TestRestore_AuditMultipleRecordsPreserveOrderAndIdentity(t *testing.T) {
	_, s, svc, _, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()

	t1 := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2024, 2, 2, 11, 0, 0, 0, time.UTC)
	t3 := time.Date(2024, 3, 3, 12, 0, 0, 0, time.UTC)
	manifest := Manifest{
		Version:  "1.0",
		Settings: minimalSettings(),
		AuditLogs: []AuditLogEntry{
			{ID: 5, ActionType: "CREATE", EntityType: "PART", EntityID: 50, CreatedAt: sql.NullTime{Time: t1, Valid: true}},
			{ID: 6, ActionType: "UPDATE", EntityType: "BIN", EntityID: 60, CreatedAt: sql.NullTime{Time: t2, Valid: true}},
			{ID: 7, ActionType: "DELETE", EntityType: "PART", EntityID: 70, CreatedAt: sql.NullTime{Time: t3, Valid: true}},
		},
	}
	zipBytes := buildRestoreZip(t, manifest)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore failed: %v", err)
	}

	rows, err := s.GetAllAuditLogs(ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	// GetAllAuditLogs orders by id.
	if rows[0].ID != 5 || rows[1].ID != 6 || rows[2].ID != 7 {
		t.Fatalf("audit ordering not preserved: %d,%d,%d", rows[0].ID, rows[1].ID, rows[2].ID)
	}
	if rows[0].ActionType != "CREATE" || rows[0].EntityType != "PART" || rows[0].EntityID != 50 {
		t.Errorf("row 5 identity not preserved: %+v", rows[0])
	}
	if !rows[2].CreatedAt.Valid || !rows[2].CreatedAt.Time.Equal(t3) {
		t.Errorf("row 7 timestamp not preserved: %+v", rows[2].CreatedAt)
	}
}

// TestRestore_AuditSQLNullVsJSONNullVsEmptyObject verifies that SQL NULL, a JSON
// null value and an empty object are preserved distinctly across a round trip.
func TestRestore_AuditSQLNullVsJSONNullVsEmptyObject(t *testing.T) {
	_, srcStore, srcSvc, _, srcCleanup := newBackupService(t)
	defer srcCleanup()
	ctx := context.Background()
	if err := srcStore.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	// row 1: SQL NULL; row 2: JSON null text; row 3: empty object text.
	seed := []db.CreateAuditLogParams{
		{ActionType: "UPDATE", EntityType: "PART", EntityID: 1, NewValue: []byte(`{"a":1}`)},
		{ActionType: "UPDATE", EntityType: "PART", EntityID: 2, OldValue: []byte(`null`), NewValue: []byte(`{"a":2}`)},
		{ActionType: "UPDATE", EntityType: "PART", EntityID: 3, OldValue: []byte(`{}`), NewValue: []byte(`{"a":3}`)},
	}
	for _, p := range seed {
		if err := srcStore.CreateAuditLog(ctx, p); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := srcSvc.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	// The manifest must omit the SQL NULL value and keep the JSON null and {}.
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	var manifest Manifest
	for _, f := range zr.File {
		if f.Name == "restore_data.json" {
			rc, _ := f.Open()
			if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
				t.Fatalf("decode manifest: %v", err)
			}
			rc.Close()
		}
	}
	if len(manifest.AuditLogs) != 3 {
		t.Fatalf("expected 3 manifest audit rows, got %d", len(manifest.AuditLogs))
	}
	if len(manifest.AuditLogs[0].OldValue) != 0 {
		t.Errorf("SQL NULL should be omitted from the manifest, got %q", manifest.AuditLogs[0].OldValue)
	}
	if string(manifest.AuditLogs[1].OldValue) != "null" {
		t.Errorf("JSON null should be preserved as null, got %q", manifest.AuditLogs[1].OldValue)
	}
	if string(manifest.AuditLogs[2].OldValue) != "{}" {
		t.Errorf("empty object should be preserved as {}, got %q", manifest.AuditLogs[2].OldValue)
	}

	dstDatabase, _, dstSvc, _, dstCleanup := newBackupService(t)
	defer dstCleanup()
	if err := dstSvc.Restore(ctx, bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Inspect the raw stored values (the read query coalesces NULL to '{}').
	rows, err := dstDatabase.QueryContext(ctx, "SELECT old_value IS NULL, old_value FROM audit_logs ORDER BY id")
	if err != nil {
		t.Fatalf("raw query: %v", err)
	}
	defer rows.Close()
	type raw struct {
		isNull int
		value  sql.NullString
	}
	var got []raw
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.isNull, &r.value); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 restored rows, got %d", len(got))
	}
	if got[0].isNull != 1 {
		t.Errorf("row 1 old_value should be SQL NULL, got %q", got[0].value.String)
	}
	if !got[1].value.Valid || got[1].value.String != "null" {
		t.Errorf("row 2 old_value should be JSON null, got %q", got[1].value.String)
	}
	if !got[2].value.Valid || got[2].value.String != "{}" {
		t.Errorf("row 3 old_value should be {}, got %q", got[2].value.String)
	}
}

// TestRestore_AuditMalformedJSONRejected verifies a malformed manifest is rejected
// before any data is written.
func TestRestore_AuditMalformedJSONRejected(t *testing.T) {
	_, s, svc, _, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	bad := `{"version":"1.0","settings":{"created_at":{"Time":"2024-01-01T00:00:00Z","Valid":true},"updated_at":{"Time":"2024-01-01T00:00:00Z","Valid":true}},"audit_logs":[{"id":1,"old_value":{"unterminated":}]}`
	zipBytes := buildRawZip(t, bad)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err == nil {
		t.Fatal("expected malformed manifest to be rejected")
	}
	rows, _ := s.GetAllAuditLogs(ctx)
	if len(rows) != 0 {
		t.Fatalf("malformed restore wrote audit rows: %d", len(rows))
	}
	if _, err := s.GetSettings(ctx); err != nil {
		t.Errorf("settings missing after rejected restore: %v", err)
	}
}

// TestRestore_AuditRollbackOnFailure verifies that a failure while inserting audit
// rows rolls the whole restore back, leaving pre-existing audit history intact.
func TestRestore_AuditRollbackOnFailure(t *testing.T) {
	_, s, svc, _, cleanup := newBackupService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	// Seed a pre-existing audit row that must survive a rolled-back restore.
	if err := s.CreateAuditLog(ctx, db.CreateAuditLogParams{
		ActionType: "CREATE", EntityType: "PART", EntityID: 1,
		NewValue: []byte(`{"seed":true}`),
	}); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	// Two entries with the same primary key -> the second insert violates the PK.
	manifest := Manifest{
		Version:  "1.0",
		Settings: minimalSettings(),
		AuditLogs: []AuditLogEntry{
			{ID: 1, ActionType: "UPDATE", EntityType: "PART", EntityID: 1, NewValue: json.RawMessage(`{"a":1}`)},
			{ID: 1, ActionType: "UPDATE", EntityType: "PART", EntityID: 1, NewValue: json.RawMessage(`{"a":2}`)},
		},
	}
	zipBytes := buildRestoreZip(t, manifest)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err == nil {
		t.Fatal("expected duplicate audit id to fail the restore")
	}

	rows, _ := s.GetAllAuditLogs(ctx)
	if len(rows) != 1 {
		t.Fatalf("expected the seeded audit row to survive rollback, got %d rows", len(rows))
	}
	mustJSONEqual(t, rows[0].NewValue, `{"seed":true}`)
}

// TestExportRestore_AuditRoundTrip creates real audit records, exports, restores
// into a fresh database and verifies semantic equivalence.
func TestExportRestore_AuditRoundTrip(t *testing.T) {
	_, srcStore, srcSvc, _, srcCleanup := newBackupService(t)
	defer srcCleanup()
	ctx := context.Background()
	if err := srcStore.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	seed := []db.CreateAuditLogParams{
		{ActionType: "CREATE", EntityType: "HARDWARE", EntityID: 1, Details: sql.NullString{String: "added", Valid: true}, NewValue: []byte(`{"id":1,"name":"C"}`)},
		{ActionType: "UPDATE", EntityType: "HARDWARE", EntityID: 1, OldValue: []byte(`{"led_count":7}`), NewValue: []byte(`{"led_count":9}`)},
		{ActionType: "DELETE", EntityType: "PART", EntityID: 2, OldValue: []byte(`[1,2,3]`)},
		{ActionType: "UPDATE", EntityType: "PART", EntityID: 3, NewValue: []byte(`"scalar"`)},
	}
	for _, p := range seed {
		if err := srcStore.CreateAuditLog(ctx, p); err != nil {
			t.Fatalf("seed audit: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := srcSvc.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	zipBytes := buf.Bytes()

	_, dstStore, dstSvc, _, dstCleanup := newBackupService(t)
	defer dstCleanup()
	if err := dstSvc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got := auditByID(t, dstStore, ctx)
	if len(got) != len(seed) {
		t.Fatalf("expected %d audit rows, got %d", len(seed), len(got))
	}
	mustJSONEqual(t, got[1].NewValue, `{"id":1,"name":"C"}`)
	mustJSONEqual(t, got[2].OldValue, `{"led_count":7}`)
	mustJSONEqual(t, got[2].NewValue, `{"led_count":9}`)
	mustJSONEqual(t, got[3].OldValue, `[1,2,3]`)
	mustJSONEqual(t, got[4].NewValue, `"scalar"`)
}

// TestExportRestore_AuditAndDrawerSpace verifies an audit-bearing backup also
// round-trips the drawer-relative coordinate space and its bins.
func TestExportRestore_AuditAndDrawerSpace(t *testing.T) {
	_, srcStore, srcSvc, _, srcCleanup := newBackupService(t)
	defer srcCleanup()
	ctx := context.Background()
	if err := srcStore.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	if err := ledspace.Set(ctx, srcStore, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}
	ctrl, _ := srcStore.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont, _ := srcStore.CreateContainer(ctx, db.CreateContainerParams{
		Name: "A", ControllerID: ctrl.ID, SegmentID: 0, LedStart: 5, LedCount: 10,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	if _, err := srcStore.CreateBin(ctx, db.CreateBinParams{
		Name: "a1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 3, Valid: true},
		Width: sql.NullInt64{Int64: 2, Valid: true},
	}); err != nil {
		t.Fatalf("create bin: %v", err)
	}
	if err := srcStore.CreateAuditLog(ctx, db.CreateAuditLogParams{
		ActionType: "UPDATE", EntityType: "HARDWARE", EntityID: 0,
		NewValue: []byte(`{"bins":1,"drawers":1,"space":"drawer"}`),
	}); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	var buf bytes.Buffer
	if err := srcSvc.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	zipBytes := buf.Bytes()

	_, dstStore, dstSvc, _, dstCleanup := newBackupService(t)
	defer dstCleanup()
	if err := dstSvc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore: %v", err)
	}

	space, err := ledspace.Current(ctx, dstStore)
	if err != nil || space != ledspace.Drawer {
		t.Fatalf("restored space = %q (err %v), want drawer", space, err)
	}
	bins, _ := dstStore.GetBinsByContainer(ctx, cont)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 3 {
		t.Fatalf("restored bin = %+v, want drawer-relative index 3", bins)
	}
	got := auditByID(t, dstStore, ctx)
	if len(got) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(got))
	}
	mustJSONEqual(t, got[1].NewValue, `{"bins":1,"drawers":1,"space":"drawer"}`)
}

// TestExportRestore_AuditAndSegmentSpace is the segment-relative counterpart.
func TestExportRestore_AuditAndSegmentSpace(t *testing.T) {
	_, srcStore, srcSvc, _, srcCleanup := newBackupService(t)
	defer srcCleanup()
	ctx := context.Background()
	if err := srcStore.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	ctrl, _ := srcStore.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont, _ := srcStore.CreateContainer(ctx, db.CreateContainerParams{
		Name: "A", ControllerID: ctrl.ID, SegmentID: 0, LedStart: 0, LedCount: 10,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	if _, err := srcStore.CreateBin(ctx, db.CreateBinParams{
		Name: "a1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 7, Valid: true},
		Width: sql.NullInt64{Int64: 1, Valid: true},
	}); err != nil {
		t.Fatalf("create bin: %v", err)
	}
	if err := srcStore.CreateAuditLog(ctx, db.CreateAuditLogParams{
		ActionType: "CREATE", EntityType: "HARDWARE", EntityID: 1, NewValue: []byte(`{"id":1}`),
	}); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	var buf bytes.Buffer
	if err := srcSvc.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	zipBytes := buf.Bytes()

	_, dstStore, dstSvc, _, dstCleanup := newBackupService(t)
	defer dstCleanup()
	if err := dstSvc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore: %v", err)
	}
	space, _ := ledspace.Current(ctx, dstStore)
	if space != ledspace.Segment {
		t.Fatalf("restored space = %q, want segment", space)
	}
	bins, _ := dstStore.GetBinsByContainer(ctx, cont)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 7 {
		t.Fatalf("restored bin = %+v, want segment index 7", bins)
	}
	got := auditByID(t, dstStore, ctx)
	if len(got) != 1 {
		t.Fatalf("expected 1 audit row, got %d", len(got))
	}
	mustJSONEqual(t, got[1].NewValue, `{"id":1}`)
}
