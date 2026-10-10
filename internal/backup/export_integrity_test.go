package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

// exportStore wraps a db.Store so the Querier used inside ExecTx can be wrapped
// for failure injection, and so successful audit writes can be counted.
type exportStore struct {
	db.Store
	wrap          func(db.Querier) db.Querier
	auditLogCalls int
	auditErr      error
}

func (f *exportStore) ExecTx(ctx context.Context, fn func(db.Querier) error) error {
	return f.Store.ExecTx(ctx, func(q db.Querier) error {
		if f.wrap != nil {
			q = f.wrap(q)
		}
		return fn(q)
	})
}

func (f *exportStore) CreateAuditLog(ctx context.Context, arg db.CreateAuditLogParams) error {
	f.auditLogCalls++
	if f.auditErr != nil {
		return f.auditErr
	}
	return f.Store.CreateAuditLog(ctx, arg)
}

// failingQuerier injects failures into selected export reads.
type failingQuerier struct {
	db.Querier
	usersErr error
	auditErr error
	onParts  func()
}

func (q *failingQuerier) GetAllUsers(ctx context.Context) ([]db.User, error) {
	if q.usersErr != nil {
		return nil, q.usersErr
	}
	return q.Querier.GetAllUsers(ctx)
}

func (q *failingQuerier) GetAllAuditLogs(ctx context.Context) ([]db.GetAllAuditLogsRow, error) {
	if q.auditErr != nil {
		return nil, q.auditErr
	}
	return q.Querier.GetAllAuditLogs(ctx)
}

func (q *failingQuerier) GetAllParts(ctx context.Context) ([]db.Part, error) {
	if q.onParts != nil {
		q.onParts()
	}
	return q.Querier.GetAllParts(ctx)
}

// countingQuerier records which export reads ran through the transaction.
type countingQuerier struct {
	db.Querier
	calls map[string]int
}

func (q *countingQuerier) record(name string) { q.calls[name]++ }

func (q *countingQuerier) GetSettings(ctx context.Context) (db.Setting, error) {
	q.record("GetSettings")
	return q.Querier.GetSettings(ctx)
}

func (q *countingQuerier) GetAllUsers(ctx context.Context) ([]db.User, error) {
	q.record("GetAllUsers")
	return q.Querier.GetAllUsers(ctx)
}

func (q *countingQuerier) GetControllers(ctx context.Context) ([]db.Controller, error) {
	q.record("GetControllers")
	return q.Querier.GetControllers(ctx)
}

func (q *countingQuerier) GetAllContainers(ctx context.Context) ([]db.Container, error) {
	q.record("GetAllContainers")
	return q.Querier.GetAllContainers(ctx)
}

func (q *countingQuerier) GetAllBins(ctx context.Context) ([]db.Bin, error) {
	q.record("GetAllBins")
	return q.Querier.GetAllBins(ctx)
}

func (q *countingQuerier) GetAllParts(ctx context.Context) ([]db.Part, error) {
	q.record("GetAllParts")
	return q.Querier.GetAllParts(ctx)
}

func (q *countingQuerier) GetAllPartAssignments(ctx context.Context) ([]db.PartAssignment, error) {
	q.record("GetAllPartAssignments")
	return q.Querier.GetAllPartAssignments(ctx)
}

func (q *countingQuerier) GetAllAuditLogs(ctx context.Context) ([]db.GetAllAuditLogsRow, error) {
	q.record("GetAllAuditLogs")
	return q.Querier.GetAllAuditLogs(ctx)
}

// fakeZipWriter is an injectable zipFileWriter that can fail on Create or Close.
type fakeZipWriter struct {
	createErr error
	closeErr  error
}

func (f *fakeZipWriter) Create(name string) (io.Writer, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return io.Discard, nil
}

func (f *fakeZipWriter) Close() error { return f.closeErr }

func newExportHarness(t *testing.T, wrap func(db.Querier) db.Querier) (*exportStore, Service, string, func()) {
	t.Helper()
	database, s, dbCleanup := setupTestDB(t)
	uploadsDir, fsCleanup := setupTestUploads(t)
	store := &exportStore{Store: s, wrap: wrap}
	svc := NewService(database, store, uploadsDir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return store, svc, uploadsDir, func() { fsCleanup(); dbCleanup() }
}

func decodeManifest(t *testing.T, buf *bytes.Buffer) Manifest {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read generated zip: %v", err)
	}
	rc, err := zr.Open("restore_data.json")
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer rc.Close()
	var m Manifest
	if err := json.NewDecoder(rc).Decode(&m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return m
}

func TestExport_DatabaseReadFailure(t *testing.T) {
	store, svc, _, cleanup := newExportHarness(t, func(q db.Querier) db.Querier {
		return &failingQuerier{Querier: q, usersErr: errors.New("db read boom")}
	})
	defer cleanup()

	var buf bytes.Buffer
	err := svc.Export(context.Background(), &buf)
	if err == nil {
		t.Fatal("export must fail when a database read fails")
	}
	if !strings.Contains(err.Error(), "users") {
		t.Errorf("error should identify the failed entity, got %v", err)
	}
	if store.auditLogCalls != 0 {
		t.Errorf("no success audit may be recorded on a failed export, got %d", store.auditLogCalls)
	}
}

func TestExport_AuditLogQueryFailure(t *testing.T) {
	store, svc, _, cleanup := newExportHarness(t, func(q db.Querier) db.Querier {
		return &failingQuerier{Querier: q, auditErr: errors.New("audit read boom")}
	})
	defer cleanup()

	var buf bytes.Buffer
	err := svc.Export(context.Background(), &buf)
	if err == nil {
		t.Fatal("export must fail when the audit-log query fails")
	}
	if !strings.Contains(err.Error(), "audit") {
		t.Errorf("error should identify the audit-log query, got %v", err)
	}
	if store.auditLogCalls != 0 {
		t.Errorf("no success audit may be recorded on a failed export, got %d", store.auditLogCalls)
	}
}

func TestExport_UploadTraversalFailure(t *testing.T) {
	orig := walkUploads
	defer func() { walkUploads = orig }()
	walkUploads = func(root string, fn filepath.WalkFunc) error {
		return errors.New("traversal boom")
	}

	store, svc, _, cleanup := newExportHarness(t, nil)
	defer cleanup()

	var buf bytes.Buffer
	err := svc.Export(context.Background(), &buf)
	if err == nil {
		t.Fatal("export must fail when upload traversal fails")
	}
	if !strings.Contains(err.Error(), "uploads") {
		t.Errorf("error should identify the uploads archive step, got %v", err)
	}
	if store.auditLogCalls != 0 {
		t.Errorf("no success audit may be recorded on a failed export, got %d", store.auditLogCalls)
	}
}

func TestExport_UploadReadFailure(t *testing.T) {
	orig := openUpload
	defer func() { openUpload = orig }()
	openUpload = func(name string) (*os.File, error) {
		return nil, errors.New("open boom")
	}

	store, svc, _, cleanup := newExportHarness(t, nil)
	defer cleanup()

	var buf bytes.Buffer
	err := svc.Export(context.Background(), &buf)
	if err == nil {
		t.Fatal("export must fail when an upload cannot be read")
	}
	if !strings.Contains(err.Error(), "uploads") {
		t.Errorf("error should identify the uploads archive step, got %v", err)
	}
	if store.auditLogCalls != 0 {
		t.Errorf("no success audit may be recorded on a failed export, got %d", store.auditLogCalls)
	}
}

func TestExport_ZipWriterFailure(t *testing.T) {
	orig := newBackupZipWriter
	defer func() { newBackupZipWriter = orig }()
	newBackupZipWriter = func(w io.Writer) zipFileWriter {
		return &fakeZipWriter{createErr: errors.New("zip create boom")}
	}

	store, svc, _, cleanup := newExportHarness(t, nil)
	defer cleanup()

	var buf bytes.Buffer
	if err := svc.Export(context.Background(), &buf); err == nil {
		t.Fatal("export must fail when the archive writer fails")
	}
	if store.auditLogCalls != 0 {
		t.Errorf("no success audit may be recorded on a failed export, got %d", store.auditLogCalls)
	}
}

func TestExport_ZipCloseFailure(t *testing.T) {
	orig := newBackupZipWriter
	defer func() { newBackupZipWriter = orig }()
	newBackupZipWriter = func(w io.Writer) zipFileWriter {
		return &fakeZipWriter{closeErr: errors.New("zip close boom")}
	}

	store, svc, _, cleanup := newExportHarness(t, nil)
	defer cleanup()

	var buf bytes.Buffer
	err := svc.Export(context.Background(), &buf)
	if err == nil {
		t.Fatal("export must fail when the archive cannot be finalized")
	}
	if !strings.Contains(err.Error(), "finalize") {
		t.Errorf("error should identify finalization, got %v", err)
	}
	if store.auditLogCalls != 0 {
		t.Errorf("no success audit may be recorded on a failed export, got %d", store.auditLogCalls)
	}
}

func TestExport_SuccessRecordsAuditAndIsRestorable(t *testing.T) {
	store, svc, _, cleanup := newExportHarness(t, nil)
	defer cleanup()
	ctx := context.Background()
	if err := store.Store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	if _, err := store.Store.CreateUser(ctx, db.CreateUserParams{
		Email: "a@b.c", PasswordHash: "h", Role: "admin",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	if store.auditLogCalls != 1 {
		t.Errorf("successful export must record exactly one audit event, got %d", store.auditLogCalls)
	}

	_, dstStore, dstSvc, _, dstCleanup := newBackupService(t)
	defer dstCleanup()
	zipBytes := buf.Bytes()
	if err := dstSvc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("restore of exported archive failed: %v", err)
	}
	if _, err := dstStore.GetUserByEmail(ctx, "a@b.c"); err != nil {
		t.Errorf("restored user missing: %v", err)
	}
}

// TestExport_ManifestReadsUseSingleTransaction verifies that every manifest
// entity is read inside the single export transaction, which is what prevents a
// concurrent write from producing a mixed-time manifest.
func TestExport_ManifestReadsUseSingleTransaction(t *testing.T) {
	var counter *countingQuerier
	_, svc, _, cleanup := newExportHarness(t, func(q db.Querier) db.Querier {
		counter = &countingQuerier{Querier: q, calls: make(map[string]int)}
		return counter
	})
	defer cleanup()

	var buf bytes.Buffer
	if err := svc.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	if counter == nil {
		t.Fatal("export did not read the manifest through a transaction")
	}
	want := []string{
		"GetSettings", "GetAllUsers", "GetControllers", "GetAllContainers",
		"GetAllBins", "GetAllParts", "GetAllPartAssignments", "GetAllAuditLogs",
	}
	for _, name := range want {
		if counter.calls[name] != 1 {
			t.Errorf("%s ran %d times inside the export transaction, want 1", name, counter.calls[name])
		}
	}
}

// TestExport_ConsistentSnapshotUnderConcurrentWrite verifies that a write
// committed by another connection while the export is reading cannot leave the
// manifest with a relationship to an entity it does not contain.
func TestExport_ConsistentSnapshotUnderConcurrentWrite(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open("file:" + dir + "/wledger.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := db.NewStore(conn)
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}

	p1, err := s.CreatePart(ctx, db.CreatePartParams{Name: "P1"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p1, Quantity: 1}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	var injected bool
	store := &exportStore{Store: s, wrap: func(q db.Querier) db.Querier {
		return &failingQuerier{Querier: q, onParts: func() {
			if injected {
				return
			}
			injected = true
			// A concurrent writer commits new, related rows on a separate
			// connection while the export's read transaction is open.
			p2, err := s.CreatePart(ctx, db.CreatePartParams{Name: "P2"})
			if err != nil {
				t.Errorf("concurrent create part: %v", err)
				return
			}
			if err := s.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: p2, Quantity: 1}); err != nil {
				t.Errorf("concurrent create assignment: %v", err)
			}
		}}
	}}
	svc := NewService(conn, store, uploadsDir, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}

	manifest := decodeManifest(t, &buf)
	present := make(map[int64]bool, len(manifest.Parts))
	for _, p := range manifest.Parts {
		present[p.ID] = true
	}
	for _, a := range manifest.PartAssignments {
		if !present[a.PartID] {
			t.Fatalf("manifest is inconsistent: assignment references part %d absent from the same manifest", a.PartID)
		}
	}
	for _, p := range manifest.Parts {
		if p.Name == "P2" {
			t.Fatal("manifest contains a part committed after the snapshot was taken")
		}
	}
}

// orderingStore records the ordering of transaction completion and audit writes,
// so a test can prove the BACKUP audit is written only after the export's read
// transaction has ended and that the export never nests a transaction.
type orderingStore struct {
	db.Store
	txDepth      int
	nestedTx     bool
	txActive     bool
	txCompleted  bool
	auditCalls   int
	auditInTx    bool
	auditAfterTx bool
}

func (o *orderingStore) ExecTx(ctx context.Context, fn func(db.Querier) error) error {
	if o.txDepth > 0 {
		o.nestedTx = true
	}
	o.txDepth++
	o.txActive = true
	err := o.Store.ExecTx(ctx, fn)
	o.txActive = false
	o.txDepth--
	if err == nil {
		o.txCompleted = true
	}
	return err
}

func (o *orderingStore) CreateAuditLog(ctx context.Context, arg db.CreateAuditLogParams) error {
	o.auditCalls++
	if o.txActive {
		o.auditInTx = true
	}
	if o.txCompleted {
		o.auditAfterTx = true
	}
	return o.Store.CreateAuditLog(ctx, arg)
}

// TestExport_AuditWrittenAfterTransactionCompletes verifies the export ends its
// read transaction before writing the BACKUP audit event, and does not nest
// transactions.
func TestExport_AuditWrittenAfterTransactionCompletes(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()
	ctx := context.Background()
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	store := &orderingStore{Store: s}
	svc := NewService(database, store, uploadsDir, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	if store.nestedTx {
		t.Error("export nested its database transaction")
	}
	if store.auditInTx {
		t.Error("backup audit was written inside the export read transaction")
	}
	if !store.txCompleted {
		t.Error("export read transaction did not complete")
	}
	if store.auditCalls != 1 {
		t.Fatalf("want exactly 1 backup audit event, got %d", store.auditCalls)
	}
	if !store.auditAfterTx {
		t.Error("backup audit was not written after the read transaction completed")
	}
}

// TestExport_AuditWriteFailureDoesNotFailExport documents and verifies the chosen
// behaviour: recording the BACKUP audit event is best-effort. Losing the audit
// record must not discard an otherwise complete, valid archive.
func TestExport_AuditWriteFailureDoesNotFailExport(t *testing.T) {
	store, svc, _, cleanup := newExportHarness(t, nil)
	defer cleanup()
	ctx := context.Background()
	if err := store.Store.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	store.auditErr = errors.New("audit write boom")

	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf); err != nil {
		t.Fatalf("an audit-write failure must not fail a completed backup: %v", err)
	}
	if store.auditLogCalls != 1 {
		t.Errorf("want the audit write to be attempted once, got %d", store.auditLogCalls)
	}

	// The archive remains complete and restorable.
	zipBytes := buf.Bytes()
	_, _, dstSvc, _, dstCleanup := newBackupService(t)
	defer dstCleanup()
	if err := dstSvc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("archive from an export whose audit write failed is not restorable: %v", err)
	}
}

// TestExport_SkipsSymlinks verifies that a symlink in the uploads directory
// cannot pull a file from outside the uploads directory into an archive.
func TestExport_SkipsSymlinks(t *testing.T) {
	_, svc, uploadsDir, cleanup := newExportHarness(t, nil)
	defer cleanup()

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	const secretContent = "TOP-SECRET-OUTSIDE-UPLOADS"
	if err := os.WriteFile(secret, []byte(secretContent), 0644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.Symlink(secret, filepath.Join(uploadsDir, "leak.txt")); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(uploadsDir, "leakdir")); err != nil {
		t.Skipf("directory symlinks not supported: %v", err)
	}

	var buf bytes.Buffer
	if err := svc.Export(context.Background(), &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read generated zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "uploads/leak.txt" || strings.HasPrefix(f.Name, "uploads/leakdir/") {
			t.Errorf("symlink entry %q was archived", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %q: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %q: %v", f.Name, err)
		}
		if bytes.Contains(data, []byte(secretContent)) {
			t.Fatalf("archive leaked symlink target content via %q", f.Name)
		}
	}
}
