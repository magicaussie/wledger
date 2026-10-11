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
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// envelopeSentinelFlagKey is a system flag seeded before a rejected restore so a
// failure that mutated the database would be caught.
const envelopeSentinelFlagKey = "envelope_test_flag"

// envelopeFixture is an isolated restore environment: a real temporary
// file-backed SQLite database (not shared in-memory state) plus a temporary
// uploads directory holding filesystem sentinels that a rejected backup must
// leave untouched.
type envelopeFixture struct {
	store      db.Store
	svc        Service
	uploadsDir string
	ctx        context.Context
}

// newEnvelopeFixture opens a migrated SQLite database in its own temp directory
// and prepares an uploads directory with three filesystem sentinels: a normal
// upload, a preserved recovery directory, and an abandoned staging directory.
// The staging sentinel is decisive: envelope validation runs before cleanup, so
// a rejected backup must leave it in place.
func newEnvelopeFixture(t *testing.T) *envelopeFixture {
	t.Helper()
	dir := t.TempDir()

	conn, err := db.Open("file:" + filepath.Join(dir, "wledger.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		conn.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	uploadsDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		t.Fatalf("create uploads dir: %v", err)
	}
	writeSentinel(t, uploadsDir, "sentinel.txt")
	writeSentinel(t, uploadsDir, filepath.Join(".uploads_bak_sentinel", "precious.txt"))
	writeSentinel(t, uploadsDir, filepath.Join(".restore_tmp_envelope", "leftover.txt"))

	store := db.NewStore(conn)
	return &envelopeFixture{
		store:      store,
		svc:        NewService(conn, store, uploadsDir, slog.New(slog.NewTextHandler(io.Discard, nil))),
		uploadsDir: uploadsDir,
		ctx:        context.Background(),
	}
}

func writeSentinel(t *testing.T, root, rel string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("create sentinel dir for %q: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte("keep-me"), 0644); err != nil {
		t.Fatalf("write sentinel %q: %v", rel, err)
	}
}

// seed creates the pre-existing database state that a rejected restore must
// preserve exactly: a user, a controller, an audit row and a system flag.
func (f *envelopeFixture) seed(t *testing.T) {
	t.Helper()
	if err := f.store.InitSettings(f.ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	if _, err := f.store.CreateUser(f.ctx, db.CreateUserParams{
		Email: "keep@example.com", PasswordHash: "h", Role: "admin",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := f.store.CreateController(f.ctx, db.CreateControllerParams{Name: "keep-controller", IpAddress: "1.1.1.1"}); err != nil {
		t.Fatalf("seed controller: %v", err)
	}
	if err := f.store.CreateAuditLog(f.ctx, db.CreateAuditLogParams{
		ActionType: "CREATE", EntityType: "PART", EntityID: 1, Details: sql.NullString{String: "keep", Valid: true},
	}); err != nil {
		t.Fatalf("seed audit log: %v", err)
	}
	if err := f.store.SetFlag(f.ctx, db.SetFlagParams{Key: envelopeSentinelFlagKey, Value: "keep"}); err != nil {
		t.Fatalf("seed flag: %v", err)
	}
}

// envelopeSnapshot captures the parts of the database a rejected restore must
// not change.
type envelopeSnapshot struct {
	userCount       int
	controllerCount int
	auditCount      int
	flagValue       string
}

func (f *envelopeFixture) snapshot(t *testing.T) envelopeSnapshot {
	t.Helper()
	users, err := f.store.GetAllUsers(f.ctx)
	if err != nil {
		t.Fatalf("GetAllUsers: %v", err)
	}
	controllers, err := f.store.GetControllers(f.ctx)
	if err != nil {
		t.Fatalf("GetControllers: %v", err)
	}
	logs, err := f.store.GetAllAuditLogs(f.ctx)
	if err != nil {
		t.Fatalf("GetAllAuditLogs: %v", err)
	}
	flag, err := f.store.GetFlag(f.ctx, envelopeSentinelFlagKey)
	if err != nil {
		t.Fatalf("GetFlag: %v", err)
	}
	return envelopeSnapshot{len(users), len(controllers), len(logs), flag}
}

// assertUntouched verifies every row, flag and filesystem sentinel survived a
// rejected backup unchanged.
func (f *envelopeFixture) assertUntouched(t *testing.T, want envelopeSnapshot) {
	t.Helper()
	if got := f.snapshot(t); got != want {
		t.Errorf("database changed by a rejected backup: got %+v, want %+v", got, want)
	}
	if _, err := f.store.GetUserByEmail(f.ctx, "keep@example.com"); err != nil {
		t.Errorf("seeded user lost after a rejected backup: %v", err)
	}
	for _, rel := range []string{
		"sentinel.txt",
		filepath.Join(".uploads_bak_sentinel", "precious.txt"),
		filepath.Join(".restore_tmp_envelope", "leftover.txt"),
	} {
		if _, err := os.Stat(filepath.Join(f.uploadsDir, rel)); err != nil {
			t.Errorf("filesystem sentinel %q lost after a rejected backup: %v", rel, err)
		}
	}
}

// buildEnvelopeZip builds a ZIP from name/content pairs.
func buildEnvelopeZip(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	for _, e := range entries {
		f, err := zw.Create(e[0])
		if err != nil {
			t.Fatalf("create zip entry %q: %v", e[0], err)
		}
		if _, err := f.Write([]byte(e[1])); err != nil {
			t.Fatalf("write zip entry %q: %v", e[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// manifestJSONWith renders a manifest to JSON.
func manifestJSONWith(t *testing.T, m Manifest) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return string(b)
}

// manifestJSON renders a manifest with the given version.
func manifestJSON(t *testing.T, version string) string {
	t.Helper()
	return manifestJSONWith(t, Manifest{Version: version, Settings: minimalSettings()})
}

// TestRestoreEnvelope_RejectsUnsupportedFramingBeforeMutation verifies that every
// unsupported, ambiguous, duplicate, missing, unknown-version or malformed
// archive envelope is rejected with a typed format error before any database,
// audit, flag, or filesystem state changes.
func TestRestoreEnvelope_RejectsUnsupportedFramingBeforeMutation(t *testing.T) {
	cases := []struct {
		name     string
		wantCode BackupFormatCode
		zip      func(t *testing.T) []byte
	}{
		{
			name:     "missing manifest",
			wantCode: FormatMissingManifest,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{{"uploads/x.txt", "x"}})
			},
		},
		{
			name:     "duplicate legacy manifest",
			wantCode: FormatDuplicateLegacyManifest,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{
					{legacyManifestName, manifestJSON(t, "1.0")},
					{legacyManifestName, manifestJSON(t, "1.0")},
				})
			},
		},
		{
			name:     "ambiguous mixed manifests",
			wantCode: FormatAmbiguousMixedManifests,
			zip: func(t *testing.T) []byte {
				// Current entry first, so an implementation that stopped at the
				// first manifest-shaped name would miss the ambiguity.
				return buildEnvelopeZip(t, [][2]string{
					{currentStorageManifestName, `{"storage":true}`},
					{legacyManifestName, manifestJSON(t, "1.0")},
				})
			},
		},
		{
			name:     "current storage manifest alone",
			wantCode: FormatUnsupportedCurrentManifest,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{{currentStorageManifestName, `{"storage":true}`}})
			},
		},
		{
			name:     "unknown version",
			wantCode: FormatUnsupportedManifestVersion,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{{legacyManifestName, manifestJSON(t, "2.0")}})
			},
		},
		{
			name:     "missing version",
			wantCode: FormatUnsupportedManifestVersion,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{{legacyManifestName, manifestJSON(t, "")}})
			},
		},
		{
			name:     "trailing JSON after manifest",
			wantCode: FormatMalformedManifestJSON,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{{legacyManifestName, manifestJSON(t, "1.0") + "\n{\"extra\":true}"}})
			},
		},
		{
			name:     "malformed JSON",
			wantCode: FormatMalformedManifestJSON,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{{legacyManifestName, `{"version":`}})
			},
		},
		{
			name:     "duplicate version member",
			wantCode: FormatDuplicateManifestField,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{{legacyManifestName, `{"version":"1.0","version":"1.0"}`}})
			},
		},
		{
			name:     "unknown future top-level field",
			wantCode: FormatUnknownManifestField,
			zip: func(t *testing.T) []byte {
				return buildEnvelopeZip(t, [][2]string{{legacyManifestName, `{"version":"1.0","storage_layout":{"future":true}}`}})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newEnvelopeFixture(t)
			f.seed(t)
			before := f.snapshot(t)

			zipBytes := tc.zip(t)
			err := f.svc.Restore(f.ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
			if err == nil {
				t.Fatal("expected the restore to be rejected")
			}
			var formatErr *BackupFormatError
			if !errors.As(err, &formatErr) {
				t.Fatalf("expected *BackupFormatError, got %T: %v", err, err)
			}
			if formatErr.Code != tc.wantCode {
				t.Errorf("format code = %q, want %q", formatErr.Code, tc.wantCode)
			}
			f.assertUntouched(t, before)
		})
	}
}

// TestRestoreEnvelope_RejectsUnsupportedBinIndexSpaceBeforeCleanup verifies that
// an otherwise well-formed legacy 1.0 archive whose coordinate marker is
// unsupported is rejected before the abandoned-staging cleanup runs, so the
// staging evidence, database rows, flag and audits all survive the rejection.
func TestRestoreEnvelope_RejectsUnsupportedBinIndexSpaceBeforeCleanup(t *testing.T) {
	f := newEnvelopeFixture(t)
	f.seed(t)
	before := f.snapshot(t)

	manifest := Manifest{Version: "1.0", BinIndexSpace: "bogus", Settings: minimalSettings()}
	zipBytes := buildRestoreZip(t, manifest)
	err := f.svc.Restore(f.ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err == nil {
		t.Fatal("expected the unsupported coordinate marker to be rejected")
	}
	var committed *RestoreCommittedError
	if errors.As(err, &committed) {
		t.Fatalf("unsupported coordinate marker must be a hard failure, got warning: %v", err)
	}
	f.assertUntouched(t, before)
}

// TestRestoreEnvelope_LegacyV1RestoreSucceeds verifies that a legitimate legacy
// 1.0 archive still restores: its data replaces the database and the uploads are
// swapped, while a preserved recovery directory survives and the abandoned
// staging directory is cleaned up.
func TestRestoreEnvelope_LegacyV1RestoreSucceeds(t *testing.T) {
	f := newEnvelopeFixture(t)
	f.seed(t)

	manifest := Manifest{
		Version:  "1.0",
		Settings: minimalSettings(),
		Users: []db.User{{
			ID: 7, Email: "restored@example.com", PasswordHash: "h", Role: "admin",
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		}},
	}
	zipBytes := buildRestoreZipWithFiles(t, manifest, map[string]string{"restored.txt": "restored"})
	if err := f.svc.Restore(f.ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("legacy 1.0 restore failed: %v", err)
	}

	if _, err := f.store.GetUserByEmail(f.ctx, "restored@example.com"); err != nil {
		t.Errorf("restored user missing: %v", err)
	}
	if _, err := f.store.GetUserByEmail(f.ctx, "keep@example.com"); err == nil {
		t.Error("previous user still present after a successful restore")
	}
	if _, err := os.Stat(filepath.Join(f.uploadsDir, "restored.txt")); err != nil {
		t.Errorf("restored upload missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.uploadsDir, "sentinel.txt")); !os.IsNotExist(err) {
		t.Error("previous upload still present after a successful restore")
	}
	// A valid restore cleans up the abandoned staging directory...
	if _, err := os.Stat(filepath.Join(f.uploadsDir, ".restore_tmp_envelope")); !os.IsNotExist(err) {
		t.Errorf("abandoned staging directory not cleaned by a valid restore: %v", err)
	}
	// ...but never removes a recovery directory.
	if _, err := os.Stat(filepath.Join(f.uploadsDir, ".uploads_bak_sentinel", "precious.txt")); err != nil {
		t.Errorf("recovery directory was removed by a valid restore: %v", err)
	}
}
