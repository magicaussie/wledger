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
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

// setupTestDB creates an in memory DB and applies the schema using db.Migrate
func setupTestDB(t *testing.T) (*sql.DB, db.Store, func()) {
	// Open in-memory DB
	// cache=shared ensures different connections see the same in-memory DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}

	// Apply migrations automatically
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}

	// Create store helper
	s := db.NewStore(conn)

	// return cleanup function
	return conn, s, func() {
		conn.Close()
	}
}

func setupTestUploads(t *testing.T) (string, func()) {
	// Create a temp directory for uploads
	dir, err := os.MkdirTemp("", "wledger_test_uploads_*")
	if err != nil {
		t.Fatalf("failed to create temp uploads dir: %v", err)
	}

	// Create a dummy file
	dummyFile := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(dummyFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	// Create a subdir
	subDir := filepath.Join(dir, "images")
	os.Mkdir(subDir, 0755)
	if err := os.WriteFile(filepath.Join(subDir, "img.png"), []byte("fake image"), 0644); err != nil {
		t.Fatalf("failed to create dummy image: %v", err)
	}

	return dir, func() {
		os.RemoveAll(dir)
	}
}

func TestExport_HappyPath(t *testing.T) {
	// Setup
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	// Seed Data
	s.InitSettings(ctx)
	_, err := s.CreateUser(ctx, db.CreateUserParams{
		Email:        "admin@example.com",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	// Execute Export
	var buf bytes.Buffer
	err = svc.Export(ctx, &buf)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify ZIP Content
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read generated zip: %v", err)
	}

	// Check for expected files
	files := make(map[string]bool)
	for _, f := range zr.File {
		files[f.Name] = true
	}

	if !files["restore_data.json"] {
		t.Error("restore_data.json missing from backup")
	}
	if !files["human_readable_parts.csv"] {
		t.Error("human_readable_parts.csv missing from backup")
	}

	if !files["uploads/test.txt"] {
		t.Error("uploads/test.txt missing from backup")
	}
	if !files["uploads/images/img.png"] {
		t.Error("uploads/images/img.png missing from backup")
	}

	// Verify JSON Content
	rc, _ := zr.Open("restore_data.json")
	defer rc.Close()
	var manifest Manifest
	if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
		t.Fatalf("failed to decode manifest: %v", err)
	}

	if len(manifest.Users) != 1 {
		t.Errorf("expected 1 user in manifest, got %d", len(manifest.Users))
	}
	if manifest.Users[0].Email != "admin@example.com" {
		t.Errorf("expected user email 'admin@example.com', got %s", manifest.Users[0].Email)
	}
}

// TestRestore_PreservesDrawerAllocations verifies that drawer LED allocations
// survive a backup export/restore round-trip.
func TestRestore_PreservesDrawerAllocations(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	now := sql.NullTime{Time: time.Now(), Valid: true}
	manifest := Manifest{
		Version:       "1.0",
		BinIndexSpace: ledspace.Segment,
		Settings: db.Setting{
			CreatedAt: now,
			UpdatedAt: now,
		},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0, LedStart: 0, LedCount: 10,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
			{ID: 2, Name: "B", ControllerID: 1, SegmentID: 0, LedStart: 10, LedCount: 10,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
	}

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	fJson, _ := zw.Create("restore_data.json")
	json.NewEncoder(fJson).Encode(manifest)
	zw.Close()
	zipBytes := buf.Bytes()

	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	containers, err := s.GetContainersByController(ctx, 1)
	if err != nil {
		t.Fatalf("get containers: %v", err)
	}
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	byName := map[string]db.Container{}
	for _, c := range containers {
		byName[c.Name] = c
	}
	if byName["A"].LedStart != 0 || byName["A"].LedCount != 10 {
		t.Errorf("A = [%d,%d), want [0,10)", byName["A"].LedStart, byName["A"].LedCount)
	}
	if byName["B"].LedStart != 10 || byName["B"].LedCount != 10 {
		t.Errorf("B = [%d,%d), want [10,20)", byName["B"].LedStart, byName["B"].LedCount)
	}
}

// buildRestoreZip packages a manifest into an in-memory restore ZIP.
func buildRestoreZip(t *testing.T, manifest Manifest) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	fJson, err := zw.Create("restore_data.json")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if err := json.NewEncoder(fJson).Encode(manifest); err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// TestRestore_LegacyBackupEstablishesAllocations verifies that restoring a
// legacy backup (created before drawer allocations existed) leaves drawer
// locating usable immediately: the restore derives allocations for the restored
// drawers without a restart, and never shifts the restored bin indices.
func TestRestore_LegacyBackupEstablishesAllocations(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	now := sql.NullTime{Time: time.Now(), Valid: true}
	manifest := Manifest{
		Version:       "1.0",
		BinIndexSpace: ledspace.Segment,
		Settings:      db.Setting{CreatedAt: now, UpdatedAt: now},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
			{ID: 2, Name: "B", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			{ID: 1, Name: "a1", ContainerID: 1, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
			{ID: 2, Name: "b1", ContainerID: 2, LedIndex: sql.NullInt64{Int64: 10, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}

	zipBytes := buildRestoreZip(t, manifest)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Allocations must be usable immediately, without a restart.
	containers, _ := s.GetContainersByController(ctx, 1)
	byName := map[string]db.Container{}
	for _, c := range containers {
		byName[c.Name] = c
	}
	if byName["A"].LedStart != 0 || byName["A"].LedCount != 10 {
		t.Errorf("A = [%d,%d), want [0,10)", byName["A"].LedStart, byName["A"].LedCount)
	}
	if byName["B"].LedStart != 10 || byName["B"].LedCount != 10 {
		t.Errorf("B = [%d,%d), want [10,20)", byName["B"].LedStart, byName["B"].LedCount)
	}

	// The restored bin indices must be untouched (no coordinate conversion).
	binsA, _ := s.GetBinsByContainer(ctx, 1)
	if len(binsA) != 1 || !binsA[0].LedIndex.Valid || binsA[0].LedIndex.Int64 != 0 {
		t.Errorf("container 1 bin index changed: %+v", binsA)
	}
	binsB, _ := s.GetBinsByContainer(ctx, 2)
	if len(binsB) != 1 || !binsB[0].LedIndex.Valid || binsB[0].LedIndex.Int64 != 10 {
		t.Errorf("container 2 bin index changed: %+v", binsB)
	}
}

// TestRestore_LegacyBackupAmbiguousMappingPreserved verifies that a legacy
// backup whose mappings cannot be reconciled with a derived allocation is still
// restored: the data is preserved and the drawer is left unallocated rather
// than guessed.
func TestRestore_LegacyBackupAmbiguousMappingPreserved(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	now := sql.NullTime{Time: time.Now(), Valid: true}
	manifest := Manifest{
		Version:       "1.0",
		BinIndexSpace: ledspace.Segment,
		Settings:      db.Setting{CreatedAt: now, UpdatedAt: now},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			// Outside the derived [0,10) allocation: ambiguous, must not be guessed.
			{ID: 1, Name: "a1", ContainerID: 1, LedIndex: sql.NullInt64{Int64: 15, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}

	zipBytes := buildRestoreZip(t, manifest)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	containers, _ := s.GetContainersByController(ctx, 1)
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	if containers[0].LedCount != 0 {
		t.Errorf("ambiguous drawer should remain unallocated, got count %d", containers[0].LedCount)
	}
	bins, _ := s.GetBinsByContainer(ctx, 1)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 15 {
		t.Errorf("restored bin data was altered: %+v", bins)
	}
}

// TestRestore_RejectsInvalidModernBackup verifies that a modern backup with
// overlapping drawer allocations is rejected before any data is changed.
func TestRestore_RejectsInvalidModernBackup(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	// Seed existing state that must survive the rejected restore.
	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	if _, err := s.CreateUser(ctx, db.CreateUserParams{Email: "keep@example.com", PasswordHash: "x", Role: "admin"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	now := sql.NullTime{Time: time.Now(), Valid: true}
	manifest := Manifest{
		Version:       "1.0",
		BinIndexSpace: ledspace.Segment,
		Settings:      db.Setting{CreatedAt: now, UpdatedAt: now},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0, LedStart: 0, LedCount: 10,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
			{ID: 2, Name: "B", ControllerID: 1, SegmentID: 0, LedStart: 5, LedCount: 10,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
	}

	zipBytes := buildRestoreZip(t, manifest)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err == nil {
		t.Fatal("expected restore to reject overlapping allocations")
	}

	// Existing configuration must be unchanged.
	if _, err := s.GetUserByEmail(ctx, "keep@example.com"); err != nil {
		t.Errorf("existing user lost after rejected restore: %v", err)
	}
	controllers, _ := s.GetControllers(ctx)
	if len(controllers) != 0 {
		t.Errorf("rejected restore created controllers: %+v", controllers)
	}
}

// TestRestore_RejectsModernBackupOutOfRangeBin verifies that a modern backup
// whose mapped bin lies outside its drawer's allocation is rejected.
func TestRestore_RejectsModernBackupOutOfRangeBin(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	now := sql.NullTime{Time: time.Now(), Valid: true}
	manifest := Manifest{
		Version:       "1.0",
		BinIndexSpace: ledspace.Segment,
		Settings:      db.Setting{CreatedAt: now, UpdatedAt: now},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0, LedStart: 0, LedCount: 10,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			{ID: 1, Name: "a1", ContainerID: 1, LedIndex: sql.NullInt64{Int64: 15, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}

	zipBytes := buildRestoreZip(t, manifest)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err == nil {
		t.Fatal("expected restore to reject out-of-range bin")
	}
}

// TestExport_IncludesBinIndexSpace verifies that new backups declare the bin
// coordinate system explicitly.
func TestExport_IncludesBinIndexSpace(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf); err != nil {
		t.Fatalf("Export: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	var manifest Manifest
	for _, f := range zr.File {
		if f.Name == "restore_data.json" {
			rc, _ := f.Open()
			_ = json.NewDecoder(rc).Decode(&manifest)
			rc.Close()
		}
	}
	if manifest.BinIndexSpace != ledspace.Segment {
		t.Errorf("bin_index_space = %q, want %q", manifest.BinIndexSpace, ledspace.Segment)
	}
}

// TestRestore_UnmarkedLegacyBackupPreservesBinsAndDoesNotBackfill verifies that
// a backup without a coordinate-space marker is restored with its bin indices
// untouched, is not backfilled, and leaves the coordinate space unresolved.
func TestRestore_UnmarkedLegacyBackupPreservesBinsAndDoesNotBackfill(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	now := sql.NullTime{Time: time.Now(), Valid: true}
	manifest := Manifest{
		Version:  "1.0",
		Settings: db.Setting{CreatedAt: now, UpdatedAt: now},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
			{ID: 2, Name: "B", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			{ID: 1, Name: "a1", ContainerID: 1, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
			{ID: 2, Name: "b1", ContainerID: 2, LedIndex: sql.NullInt64{Int64: 10, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}

	zipBytes := buildRestoreZip(t, manifest)
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	var committed *RestoreCommittedError
	if !errors.As(err, &committed) {
		t.Fatalf("expected RestoreCommittedError, got %v", err)
	}

	// Bin indices must be preserved exactly.
	binsA, _ := s.GetBinsByContainer(ctx, 1)
	if len(binsA) != 1 || !binsA[0].LedIndex.Valid || binsA[0].LedIndex.Int64 != 0 {
		t.Errorf("container 1 bin changed: %+v", binsA)
	}
	binsB, _ := s.GetBinsByContainer(ctx, 2)
	if len(binsB) != 1 || !binsB[0].LedIndex.Valid || binsB[0].LedIndex.Int64 != 10 {
		t.Errorf("container 2 bin changed: %+v", binsB)
	}

	// No allocations may be derived from an unresolved coordinate space.
	containers, _ := s.GetContainersByController(ctx, 1)
	for _, c := range containers {
		if c.LedCount != 0 {
			t.Errorf("drawer %q was backfilled despite unresolved space: count %d", c.Name, c.LedCount)
		}
	}

	unresolved, err := ledspace.IsUnresolved(ctx, s)
	if err != nil || !unresolved {
		t.Errorf("expected unresolved state, got unresolved=%v err=%v", unresolved, err)
	}
}

// TestRestore_KnownGoodBackupClearsUnresolvedState verifies that restoring a
// backup with an explicit segment marker clears a previously unresolved state.
func TestRestore_KnownGoodBackupClearsUnresolvedState(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	now := sql.NullTime{Time: time.Now(), Valid: true}
	unmarked := Manifest{
		Version:  "1.0",
		Settings: db.Setting{CreatedAt: now, UpdatedAt: now},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			{ID: 1, Name: "a1", ContainerID: 1, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}
	zipBytes := buildRestoreZip(t, unmarked)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err == nil {
		t.Fatal("expected unresolved warning from unmarked restore")
	}
	if unresolved, _ := ledspace.IsUnresolved(ctx, s); !unresolved {
		t.Fatal("expected unresolved state after unmarked restore")
	}

	marked := unmarked
	marked.BinIndexSpace = ledspace.Segment
	zipBytes = buildRestoreZip(t, marked)
	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("known-good restore: %v", err)
	}
	if unresolved, _ := ledspace.IsUnresolved(ctx, s); unresolved {
		t.Error("unresolved state was not cleared by a known-good restore")
	}
}

// TestRestore_RejectsUnsupportedBinIndexSpace verifies that an explicit but
// unsupported coordinate-space marker is rejected rather than defaulted.
func TestRestore_RejectsUnsupportedBinIndexSpace(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	now := sql.NullTime{Time: time.Now(), Valid: true}
	manifest := Manifest{
		Version:       "1.0",
		BinIndexSpace: "drawer", // reserved for Task 006B, not supported yet
		Settings:      db.Setting{CreatedAt: now, UpdatedAt: now},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			{ID: 1, Name: "a1", ContainerID: 1, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}

	zipBytes := buildRestoreZip(t, manifest)
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err == nil {
		t.Fatal("expected unsupported bin_index_space to be rejected")
	}
	var committed *RestoreCommittedError
	if errors.As(err, &committed) {
		t.Fatalf("unsupported marker must be a hard failure, got warning: %v", err)
	}
}

// TestRestore_ModernRoundTripRemainsValid verifies that a backup exported by
// the current application restores cleanly with its hardware intact.
func TestRestore_ModernRoundTripRemainsValid(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	if err := s.InitSettings(ctx); err != nil {
		t.Fatalf("init settings: %v", err)
	}
	ctrl, err := s.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	cont, err := s.CreateContainer(ctx, db.CreateContainerParams{
		Name: "A", ControllerID: ctrl.ID, SegmentID: 0, LedStart: 0, LedCount: 10,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	if _, err := s.CreateBin(ctx, db.CreateBinParams{
		Name: "a1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true},
	}); err != nil {
		t.Fatalf("create bin: %v", err)
	}

	var buf bytes.Buffer
	if err := svc.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	zipBytes := buf.Bytes()

	if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
		t.Fatalf("round-trip restore: %v", err)
	}

	containers, _ := s.GetContainersByController(ctx, ctrl.ID)
	if len(containers) != 1 || containers[0].LedStart != 0 || containers[0].LedCount != 10 {
		t.Errorf("container not preserved: %+v", containers)
	}
	bins, _ := s.GetBinsByContainer(ctx, cont)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 0 {
		t.Errorf("bin not preserved: %+v", bins)
	}
	if unresolved, _ := ledspace.IsUnresolved(ctx, s); unresolved {
		t.Error("modern round-trip must not leave an unresolved coordinate space")
	}
}

// TestRestore_KnownGoodBackupBlocksMigration005 verifies that restoring a
// backup explicitly marked segment-relative marks migration 005 applied in the
// same transaction, so a restart can never convert the already-absolute bin
// indices a second time. It covers a database where the flag was absent and one
// where it was explicitly false.
func TestRestore_KnownGoodBackupBlocksMigration005(t *testing.T) {
	cases := []struct {
		name    string
		present bool
		value   string
	}{
		{"flag absent", false, ""},
		{"flag false", true, "false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, s, dbCleanup := setupTestDB(t)
			defer dbCleanup()
			uploadsDir, fsCleanup := setupTestUploads(t)
			defer fsCleanup()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			svc := NewService(database, s, uploadsDir, logger)
			ctx := context.Background()

			if tc.present {
				if err := s.SetFlag(ctx, db.SetFlagParams{Key: hardware.Migration005FlagKey, Value: tc.value}); err != nil {
					t.Fatalf("seed flag: %v", err)
				}
			}

			now := sql.NullTime{Time: time.Now(), Valid: true}
			manifest := Manifest{
				Version:       "1.0",
				BinIndexSpace: ledspace.Segment,
				Settings:      db.Setting{CreatedAt: now, UpdatedAt: now},
				Controllers: []db.Controller{
					{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
				},
				Containers: []db.Container{
					{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0,
						ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
					{ID: 2, Name: "B", ControllerID: 1, SegmentID: 0,
						ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
				},
				Bins: []db.Bin{
					{ID: 1, Name: "a1", ContainerID: 1, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
					{ID: 2, Name: "b1", ContainerID: 2, LedIndex: sql.NullInt64{Int64: 10, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
				},
			}

			zipBytes := buildRestoreZip(t, manifest)
			if err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes))); err != nil {
				t.Fatalf("restore: %v", err)
			}

			if flag, err := s.GetFlag(ctx, hardware.Migration005FlagKey); err != nil || flag != "true" {
				t.Fatalf("migration_005_applied = %q (err %v), want true", flag, err)
			}

			// Simulate a restart: the startup migration must be a no-op.
			if err := hardware.MigrateLegacyLedIndices(ctx, s, logger); err != nil {
				t.Fatalf("migration: %v", err)
			}
			binsA, _ := s.GetBinsByContainer(ctx, 1)
			if len(binsA) != 1 || !binsA[0].LedIndex.Valid || binsA[0].LedIndex.Int64 != 0 {
				t.Errorf("container 1 bin changed after restart: %+v", binsA)
			}
			binsB, _ := s.GetBinsByContainer(ctx, 2)
			if len(binsB) != 1 || !binsB[0].LedIndex.Valid || binsB[0].LedIndex.Int64 != 10 {
				t.Errorf("container 2 bin changed after restart: %+v", binsB)
			}
		})
	}
}

// TestRestore_UnmarkedBackupKeepsMigrationBlocked verifies that an unmarked
// backup leaves the coordinate space unresolved and that a restart cannot run
// migration 005 against the restored (possibly relative) bin indices.
func TestRestore_UnmarkedBackupKeepsMigrationBlocked(t *testing.T) {
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	now := sql.NullTime{Time: time.Now(), Valid: true}
	manifest := Manifest{
		Version:  "1.0",
		Settings: db.Setting{CreatedAt: now, UpdatedAt: now},
		Controllers: []db.Controller{
			{ID: 1, Name: "C", IpAddress: "1.1.1.1", CreatedAt: now},
		},
		Containers: []db.Container{
			{ID: 1, Name: "A", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
			{ID: 2, Name: "B", ControllerID: 1, SegmentID: 0,
				ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true}, CreatedAt: now, UpdatedAt: now},
		},
		Bins: []db.Bin{
			// Container-relative indices that migration 005 would shift by 10.
			{ID: 1, Name: "a1", ContainerID: 1, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
			{ID: 2, Name: "b1", ContainerID: 2, LedIndex: sql.NullInt64{Int64: 0, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}},
		},
	}

	zipBytes := buildRestoreZip(t, manifest)
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	var committed *RestoreCommittedError
	if !errors.As(err, &committed) {
		t.Fatalf("expected RestoreCommittedError, got %v", err)
	}

	// Simulate a restart: migration 005 must remain blocked.
	if err := hardware.MigrateLegacyLedIndices(ctx, s, logger); err != nil {
		t.Fatalf("migration: %v", err)
	}
	binsB, _ := s.GetBinsByContainer(ctx, 2)
	if len(binsB) != 1 || !binsB[0].LedIndex.Valid || binsB[0].LedIndex.Int64 != 0 {
		t.Errorf("unresolved bins were converted by migration 005: %+v", binsB)
	}
	if unresolved, _ := ledspace.IsUnresolved(ctx, s); !unresolved {
		t.Error("coordinate space should remain unresolved after restart")
	}
}

func TestRestore_HappyPath(t *testing.T) {
	// Setup
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	// the "Live" directory that gets replaced
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	// Create a "Backup" to restore
	// create a new zip in memory that represents a backup
	// containing a DIFFERENT user and DIFFERENT file.
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Manifest with 1 new user
	manifest := Manifest{
		Version: "1.0",
		Users: []db.User{
			{
				ID:           99,
				Email:        "restored@example.com",
				PasswordHash: "newhash",
				Role:         "admin",
				CreatedAt:    sql.NullTime{Time: time.Now(), Valid: true},
			},
		},
		Settings: db.Setting{
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
			UpdatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		},
	}
	fJson, _ := zw.Create("restore_data.json")
	json.NewEncoder(fJson).Encode(manifest)

	// File: "uploads/restored_file.txt"
	fFile, _ := zw.Create("uploads/restored_file.txt")
	fFile.Write([]byte("I am new here"))

	zw.Close()
	zipBytes := buf.Bytes()

	// Pre-state Check
	// DB is empty (setupTestDB doesn't seed)
	// uploadsDir has "test.txt" (from setupTestUploads)

	// Execute Restore
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verification

	// DB: Should have the restored user
	u, err := s.GetUserByEmail(ctx, "restored@example.com")
	if err != nil {
		t.Fatalf("failed to find restored user: %v", err)
	}
	if u.ID != 99 {
		t.Errorf("expected restored user ID 99, got %d", u.ID)
	}

	// FS: Should have "restored_file.txt" and NOT "test.txt"
	if _, err := os.Stat(filepath.Join(uploadsDir, "restored_file.txt")); os.IsNotExist(err) {
		t.Error("restored file missing from uploads dir")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); err == nil {
		t.Error("old file 'test.txt' still exists in uploads dir (should be gone)")
	}
}

func TestRestore_RollbackOnDBError(t *testing.T) {
	// Setup
	database, s, dbCleanup := setupTestDB(t)
	defer dbCleanup()
	uploadsDir, fsCleanup := setupTestUploads(t)
	defer fsCleanup()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(database, s, uploadsDir, logger)
	ctx := context.Background()

	// Seed initial DB state
	s.InitSettings(ctx)

	// Create a "Bad" Backup
	// Manifest contains a PartAssignment for a Part that doesn't exist.
	// This should trigger a Foreign Key violation during restore.
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	manifest := Manifest{
		Version: "1.0",
		Settings: db.Setting{
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
			UpdatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		},
		PartAssignments: []db.PartAssignment{
			{
				ID: 1, PartID: 9999, Quantity: 10, // Part 9999 does not exist
			},
		},
	}
	fJson, _ := zw.Create("restore_data.json")
	json.NewEncoder(fJson).Encode(manifest)

	// Add a file - this should NOT end up in uploads if rollback works
	fFile, _ := zw.Create("uploads/should_not_exist.txt")
	fFile.Write([]byte("bad"))

	zw.Close()
	zipBytes := buf.Bytes()

	// Execute Restore
	err := svc.Restore(ctx, bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err == nil {
		t.Fatal("Expected error during restore due to FK violation, got nil")
	}

	// Verify Rollback

	_, err = s.GetSettings(ctx)
	if err != nil {
		t.Errorf("Settings missing after rollback: %v", err)
	}

	// FS: "test.txt" should still exist, "should_not_exist.txt" should NOT exist.
	if _, err := os.Stat(filepath.Join(uploadsDir, "test.txt")); os.IsNotExist(err) {
		t.Error("original file 'test.txt' missing after rollback")
	}
	if _, err := os.Stat(filepath.Join(uploadsDir, "should_not_exist.txt")); err == nil {
		t.Error("new file 'should_not_exist.txt' exists despite rollback")
	}
}
