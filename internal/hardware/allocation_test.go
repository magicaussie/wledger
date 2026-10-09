package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func setupAllocTest(t *testing.T, name string) (Service, db.Store, *sql.DB) {
	t.Helper()
	dbConn, err := db.Open("file:" + name + "?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	store := db.NewStore(dbConn)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewService(store, wled.NewClient(), logger), store, dbConn
}

func mkContainer(t *testing.T, store db.Store, ctx context.Context, ctrlID int64, name string, seg int64, cfg string, start, count int64) int64 {
	t.Helper()
	id, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:         name,
		ControllerID: ctrlID,
		SegmentID:    seg,
		ConfigJson:   sql.NullString{String: cfg, Valid: true},
		LedStart:     start,
		LedCount:     count,
	})
	if err != nil {
		t.Fatalf("create container %s: %v", name, err)
	}
	return id
}

func mkBin(t *testing.T, store db.Store, ctx context.Context, contID int64, name string, idx, width int64) {
	t.Helper()
	_, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        name,
		ContainerID: contID,
		LedIndex:    sql.NullInt64{Int64: idx, Valid: true},
		Width:       sql.NullInt64{Int64: width, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin %s: %v", name, err)
	}
}

func TestValidateAllocation(t *testing.T) {
	cases := []struct {
		name       string
		start, cnt int64
		wantErr    bool
	}{
		{"valid zero start", 0, 10, false},
		{"valid nonzero start", 10, 5, false},
		{"negative start", -1, 5, true},
		{"zero count", 0, 0, true},
		{"negative count", 0, -5, true},
		{"overflow", math.MaxInt64, 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidateAllocation(c.start, c.cnt); (err != nil) != c.wantErr {
				t.Errorf("ValidateAllocation(%d,%d) err=%v wantErr=%v", c.start, c.cnt, err, c.wantErr)
			}
		})
	}
}

func TestValidateAllocationsOverlap(t *testing.T) {
	ok := []db.Container{
		{Name: "A", SegmentID: 0, LedStart: 0, LedCount: 10},
		{Name: "B", SegmentID: 0, LedStart: 10, LedCount: 10}, // adjacent, no overlap
		{Name: "C", SegmentID: 1, LedStart: 0, LedCount: 10},  // different segment
	}
	if err := ValidateAllocations(ok); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	overlap := []db.Container{
		{Name: "A", SegmentID: 0, LedStart: 0, LedCount: 10},
		{Name: "B", SegmentID: 0, LedStart: 5, LedCount: 10},
	}
	if err := ValidateAllocations(overlap); err == nil {
		t.Error("expected overlap error, got nil")
	}

	// Same range on different segments is allowed.
	diffSeg := []db.Container{
		{Name: "A", SegmentID: 0, LedStart: 0, LedCount: 10},
		{Name: "B", SegmentID: 1, LedStart: 0, LedCount: 10},
	}
	if err := ValidateAllocations(diffSeg); err != nil {
		t.Errorf("expected no error for different segments, got %v", err)
	}
}

func TestPreflightDrawerAllocations(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "preflight")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}

	// Segment 0: A (linear 10) clean, B (linear 10) clean, C (linear 10) empty.
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 0)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 0, 0)
	mkContainer(t, store, ctx, ctrl.ID, "C", 0, `{"type":"linear","total":10}`, 0, 0)
	mkBin(t, store, ctx, a, "a1", 0, 1)
	mkBin(t, store, ctx, a, "a2", 9, 1)
	mkBin(t, store, ctx, b, "b1", 10, 1)
	mkBin(t, store, ctx, b, "b2", 19, 1)

	report, err := PreflightDrawerAllocations(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if report.Clean != 2 || report.Empty != 1 || report.Inconsistent != 0 {
		t.Fatalf("unexpected report: clean=%d empty=%d inconsistent=%d", report.Clean, report.Empty, report.Inconsistent)
	}
	// Verify proposed allocations follow the UI ordering.
	byName := map[string]AllocationFinding{}
	for _, f := range report.Findings {
		byName[f.ContainerName] = f
	}
	if byName["A"].ProposedStart != 0 || byName["A"].ProposedCount != 10 {
		t.Errorf("A proposal = [%d,%d), want [0,10)", byName["A"].ProposedStart, byName["A"].ProposedCount)
	}
	if byName["B"].ProposedStart != 10 || byName["B"].ProposedCount != 10 {
		t.Errorf("B proposal = [%d,%d), want [10,20)", byName["B"].ProposedStart, byName["B"].ProposedCount)
	}
}

func TestPreflightDrawerAllocationsInconsistent(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "preflight_bad")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})

	// A: bin outside its proposed [0,10) allocation.
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 0)
	mkBin(t, store, ctx, a, "a1", 15, 1)

	// B: malformed config.
	mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{not json`, 0, 0)

	// C: variable-width bin that overruns the allocation.
	c := mkContainer(t, store, ctx, ctrl.ID, "C", 0, `{"type":"linear","total":10}`, 0, 0)
	mkBin(t, store, ctx, c, "c1", 8, 5) // [8,13) exceeds [0,10)

	report, err := PreflightDrawerAllocations(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if report.Inconsistent != 3 {
		t.Fatalf("expected 3 inconsistent, got %d (report=%+v)", report.Inconsistent, report.Findings)
	}
}

func TestPreflightAllowsPartialAndVariableWidth(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "preflight_partial")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":20}`, 0, 0)
	// Partial mapping with a variable-width bin, all within [0,20).
	mkBin(t, store, ctx, a, "a1", 3, 5) // [3,8)

	report, err := PreflightDrawerAllocations(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if report.Clean != 1 || report.Inconsistent != 0 {
		t.Fatalf("expected 1 clean, got clean=%d inconsistent=%d", report.Clean, report.Inconsistent)
	}
}

func TestBackfillDrawerAllocations(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "backfill")
	defer dbConn.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 0)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 0, 0)
	mkBin(t, store, ctx, a, "a1", 0, 1)
	mkBin(t, store, ctx, b, "b1", 10, 1)

	if err := BackfillDrawerAllocations(ctx, store, logger); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	ca, _ := store.GetContainer(ctx, a)
	cb, _ := store.GetContainer(ctx, b)
	if ca.LedStart != 0 || ca.LedCount != 10 {
		t.Errorf("A allocation = [%d,%d), want [0,10)", ca.LedStart, ca.LedCount)
	}
	if cb.LedStart != 10 || cb.LedCount != 10 {
		t.Errorf("B allocation = [%d,%d), want [10,20)", cb.LedStart, cb.LedCount)
	}

	// Idempotent: second run is a no-op.
	if err := BackfillDrawerAllocations(ctx, store, logger); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	ca2, _ := store.GetContainer(ctx, a)
	if ca2.LedStart != 0 || ca2.LedCount != 10 {
		t.Errorf("A allocation changed on second run: [%d,%d)", ca2.LedStart, ca2.LedCount)
	}
}

func TestBackfillDoesNotOverwriteExistingAllocation(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "backfill_nooverwrite")
	defer dbConn.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	// Explicit allocation that differs from the layout-derived one.
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 100, 10)

	if err := BackfillDrawerAllocations(ctx, store, logger); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	ca, _ := store.GetContainer(ctx, a)
	if ca.LedStart != 100 || ca.LedCount != 10 {
		t.Errorf("existing allocation was overwritten: [%d,%d), want [100,110)", ca.LedStart, ca.LedCount)
	}
}

func TestBackfillLeavesInconsistentUnallocated(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "backfill_inconsistent")
	defer dbConn.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 0)
	mkBin(t, store, ctx, a, "a1", 15, 1) // outside [0,10)

	if err := BackfillDrawerAllocations(ctx, store, logger); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	ca, _ := store.GetContainer(ctx, a)
	if ca.LedCount != 0 {
		t.Errorf("inconsistent drawer should remain unallocated, got count %d", ca.LedCount)
	}
	// The bin index must be untouched.
	bins, _ := store.GetBinsByContainer(ctx, a)
	if len(bins) != 1 || bins[0].LedIndex.Int64 != 15 {
		t.Errorf("bin index was altered: %+v", bins)
	}
}

func TestBackfillFailedContextLeavesDataIntact(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "backfill_fail")
	defer dbConn.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 0)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := BackfillDrawerAllocations(cancelled, store, logger); err == nil {
		t.Fatal("expected error from cancelled context")
	}

	ca, _ := store.GetContainer(ctx, a)
	if ca.LedCount != 0 {
		t.Errorf("allocation should be unchanged after failed backfill, got count %d", ca.LedCount)
	}
	if flag, err := store.GetFlag(ctx, "drawer_allocation_backfilled"); err == nil && flag == "true" {
		t.Error("backfill flag should not be set after failure")
	}
}

// TestBackfillSkipsUnresolved verifies that drawer allocations are never
// derived from bin indices whose coordinate system is unresolved.
func TestBackfillSkipsUnresolved(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "backfill_unresolved")
	defer dbConn.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 0)
	mkBin(t, store, ctx, a, "a1", 0, 1)

	if err := ledspace.Set(ctx, store, ledspace.Unresolved); err != nil {
		t.Fatalf("set unresolved: %v", err)
	}
	if err := BackfillDrawerAllocations(ctx, store, logger); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	ca, _ := store.GetContainer(ctx, a)
	if ca.LedCount != 0 {
		t.Errorf("unresolved space must not be backfilled, got count %d", ca.LedCount)
	}
}

// TestUnresolvedStatePersistsAcrossStartup verifies that the unresolved state
// survives a restart and that the startup migrations leave the restored data
// untouched.
func TestUnresolvedStatePersistsAcrossStartup(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "unresolved_persist")
	defer dbConn.Close()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 0)
	mkBin(t, store, ctx, a, "a1", 0, 1)

	if err := ledspace.Set(ctx, store, ledspace.Unresolved); err != nil {
		t.Fatalf("set unresolved: %v", err)
	}

	// Simulate a restart: a fresh store over the same database.
	restarted := db.NewStore(dbConn)
	if unresolved, err := ledspace.IsUnresolved(ctx, restarted); err != nil || !unresolved {
		t.Fatalf("unresolved state did not persist: unresolved=%v err=%v", unresolved, err)
	}

	if err := MigrateLegacyLedIndices(ctx, restarted, logger); err != nil {
		t.Fatalf("migration: %v", err)
	}
	if err := BackfillDrawerAllocations(ctx, restarted, logger); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	ca, _ := restarted.GetContainer(ctx, a)
	if ca.LedCount != 0 {
		t.Errorf("unresolved data was backfilled across restart: count %d", ca.LedCount)
	}
	bins, _ := restarted.GetBinsByContainer(ctx, a)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 0 {
		t.Errorf("unresolved bins were modified across restart: %+v", bins)
	}
}

func TestSaveGridRejectsOverlappingAllocations(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_overlap")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})

	configData := `[
		{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}},
		{"id":null,"name":"B","segment_id":0,"led_start":5,"led_count":10,"config":{"type":"linear","total":10}}
	]`
	if _, err := svc.SaveGrid(ctx, ctrl.ID, `[]`, configData); err == nil {
		t.Fatal("expected overlap error, got nil")
	}
}

func TestSaveGridRejectsInvalidAllocations(t *testing.T) {
	cases := map[string]string{
		"negative start":     `[{"id":null,"name":"A","segment_id":0,"led_start":-1,"led_count":10,"config":{"type":"linear","total":10}}]`,
		"overflow":           `[{"id":null,"name":"A","segment_id":0,"led_start":9223372036854775807,"led_count":2,"config":{"type":"linear","total":10}}]`,
		"zero-length config": `[{"id":null,"name":"A","segment_id":0,"config":{"type":"grid","rows":0,"cols":0}}]`,
	}
	for name, configData := range cases {
		t.Run(name, func(t *testing.T) {
			svc, store, dbConn := setupAllocTest(t, "savegrid_invalid_"+name)
			defer dbConn.Close()
			ctx := context.Background()
			ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
			if _, err := svc.SaveGrid(ctx, ctrl.ID, `[]`, configData); err == nil {
				t.Fatalf("expected error for %s, got nil", name)
			}
		})
	}
}

func TestSaveGridPersistsAllocations(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_persist")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	configData := `[
		{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}},
		{"id":null,"name":"B","segment_id":0,"led_start":10,"led_count":10,"config":{"type":"linear","total":10}}
	]`
	if _, err := svc.SaveGrid(ctx, ctrl.ID, `[]`, configData); err != nil {
		t.Fatalf("SaveGrid: %v", err)
	}
	containers, _ := store.GetContainersByController(ctx, ctrl.ID)
	if len(containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(containers))
	}
	if containers[0].LedStart != 0 || containers[0].LedCount != 10 {
		t.Errorf("A = [%d,%d), want [0,10)", containers[0].LedStart, containers[0].LedCount)
	}
	if containers[1].LedStart != 10 || containers[1].LedCount != 10 {
		t.Errorf("B = [%d,%d), want [10,20)", containers[1].LedStart, containers[1].LedCount)
	}
}

func TestSaveGridDerivesAllocationWhenUnset(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_derive")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	// No led_start/led_count supplied: derived from layout capacity, in order.
	configData := `[
		{"id":null,"name":"A","segment_id":0,"config":{"type":"linear","total":10}},
		{"id":null,"name":"B","segment_id":0,"config":{"type":"linear","total":5}}
	]`
	if _, err := svc.SaveGrid(ctx, ctrl.ID, `[]`, configData); err != nil {
		t.Fatalf("SaveGrid: %v", err)
	}
	containers, _ := store.GetContainersByController(ctx, ctrl.ID)
	if containers[0].LedStart != 0 || containers[0].LedCount != 10 {
		t.Errorf("A = [%d,%d), want [0,10)", containers[0].LedStart, containers[0].LedCount)
	}
	if containers[1].LedStart != 10 || containers[1].LedCount != 5 {
		t.Errorf("B = [%d,%d), want [10,15)", containers[1].LedStart, containers[1].LedCount)
	}
}

func TestSaveGridRejectsBinOutsideAllocation(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_bin_outside")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":15,"width":1,"name":"bad"}]`
	if _, err := svc.SaveGrid(ctx, ctrl.ID, gridData, configData); err == nil {
		t.Fatal("expected bin outside allocation to be rejected")
	}
}

// TestSaveGridInvalidSubmissionLeavesDatabaseUnchanged proves that rejected
// submissions (bad bin mappings or overlapping allocations) never modify the
// persisted configuration.
func TestSaveGridInvalidSubmissionLeavesDatabaseUnchanged(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_unchanged")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	mkBin(t, store, ctx, cont, "a1", 0, 1)

	validConfig := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	cases := []struct {
		name   string
		grid   string
		config string
	}{
		{"negative index", `[{"container_index":0,"x":0,"y":0,"led_index":-1,"width":1,"name":"x"}]`, validConfig},
		{"index after allocation", `[{"container_index":0,"x":0,"y":0,"led_index":10,"width":1,"name":"x"}]`, validConfig},
		{"variable width overrun", `[{"container_index":0,"x":0,"y":0,"led_index":8,"width":5,"name":"x"}]`, validConfig},
		{"invalid container reference", `[{"container_index":3,"x":0,"y":0,"led_index":0,"width":1,"name":"x"}]`, validConfig},
		{"overlapping allocations", `[]`, `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}},{"id":null,"name":"B","segment_id":0,"led_start":5,"led_count":10,"config":{"type":"linear","total":10}}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.SaveGrid(ctx, ctrl.ID, tc.grid, tc.config); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			containers, _ := store.GetContainersByController(ctx, ctrl.ID)
			if len(containers) != 1 || containers[0].Name != "A" || containers[0].LedStart != 0 || containers[0].LedCount != 10 {
				t.Fatalf("containers changed after invalid submission: %+v", containers)
			}
			bins, _ := store.GetBinsByContainer(ctx, cont)
			if len(bins) != 1 || bins[0].Name != "a1" || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 0 {
				t.Fatalf("bins changed after invalid submission: %+v", bins)
			}
		})
	}
}

// TestSaveGridAllowsUnmappedBin verifies that a bin with a NULL LED index is
// accepted and preserved as unmapped.
func TestSaveGridAllowsUnmappedBin(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_unmapped")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":null,"width":1,"name":"unmapped"}]`
	if _, err := svc.SaveGrid(ctx, ctrl.ID, gridData, configData); err != nil {
		t.Fatalf("SaveGrid with unmapped bin: %v", err)
	}
	bins, _ := svc.GetBinsByController(ctx, ctrl.ID)
	if len(bins) != 1 {
		t.Fatalf("expected 1 bin, got %d", len(bins))
	}
	if bins[0].LedIndex.Valid {
		t.Errorf("expected unmapped (NULL) led_index, got %+v", bins[0].LedIndex)
	}
}

// TestSaveGridReturnsSentinelError verifies that invalid submissions return the
// typed sentinel so the HTTP layer can distinguish them from internal failures.
func TestSaveGridReturnsSentinelError(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_sentinel")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":15,"width":1,"name":"bad"}]`
	_, err := svc.SaveGrid(ctx, ctrl.ID, gridData, configData)
	if !errors.Is(err, ErrInvalidAllocation) {
		t.Fatalf("expected ErrInvalidAllocation, got %v", err)
	}
}

// TestImportConfigRejectsInvalidBins verifies that imported bins are validated
// against their drawer allocations and that invalid container references are
// rejected rather than silently skipped. Failed imports must not create a
// controller.
func TestImportConfigRejectsInvalidBins(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{
			"out-of-range led index",
			`{"version":"1.0","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":20,"config":{"type":"linear","total":20}}],"bins":[{"container_index":0,"led_index":25,"width":1,"name":"x"}]}`,
		},
		{
			"variable-width overrun",
			`{"version":"1.0","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":20,"config":{"type":"linear","total":20}}],"bins":[{"container_index":0,"led_index":18,"width":5,"name":"x"}]}`,
		},
		{
			"invalid container reference",
			`{"version":"1.0","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":20,"config":{"type":"linear","total":20}}],"bins":[{"container_index":5,"led_index":0,"width":1,"name":"x"}]}`,
		},
		{
			"overlapping allocations",
			`{"version":"1.0","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}},{"name":"B","segment_id":0,"led_start":5,"led_count":10,"config":{"type":"linear","total":10}}],"bins":[]}`,
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, dbConn := setupAllocTest(t, fmt.Sprintf("import_invalid_%d", i))
			defer dbConn.Close()
			ctx := context.Background()
			before, _ := store.GetControllers(ctx)
			if _, err := svc.ImportConfig(ctx, "", "", 0, []byte(tc.data)); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			after, _ := store.GetControllers(ctx)
			if len(after) != len(before) {
				t.Fatalf("failed import changed controllers: before=%d after=%d", len(before), len(after))
			}
		})
	}
}

// TestImportConfigAllowsPartiallyMappedDrawer verifies that a drawer with some
// mapped and some unmapped bins imports successfully.
func TestImportConfigAllowsPartiallyMappedDrawer(t *testing.T) {
	svc, _, dbConn := setupAllocTest(t, "import_partial")
	defer dbConn.Close()
	ctx := context.Background()

	data := `{"version":"1.0","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":20,"config":{"type":"linear","total":20}}],"bins":[{"container_index":0,"led_index":3,"width":5,"name":"mapped"},{"container_index":0,"led_index":null,"width":1,"name":"unmapped"}]}`
	id, err := svc.ImportConfig(ctx, "", "", 0, []byte(data))
	if err != nil {
		t.Fatalf("ImportConfig: %v", err)
	}
	bins, _ := svc.GetBinsByController(ctx, id)
	if len(bins) != 2 {
		t.Fatalf("expected 2 bins, got %d", len(bins))
	}
	var mapped, unmapped int
	for _, b := range bins {
		if b.LedIndex.Valid {
			mapped++
		} else {
			unmapped++
		}
	}
	if mapped != 1 || unmapped != 1 {
		t.Errorf("expected 1 mapped + 1 unmapped, got %d + %d", mapped, unmapped)
	}
}

// TestSaveGridRejectsDrawerSpace verifies that the grid writer refuses to write
// segment-relative indices into a drawer-relative database, so no mixed-space
// write can occur while the frontend still edits in segment space.
func TestSaveGridRejectsDrawerSpace(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_drawer_space")
	defer dbConn.Close()
	ctx := context.Background()

	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"a1"}]`
	if _, err := svc.SaveGrid(ctx, ctrl.ID, gridData, configData); err == nil {
		t.Fatal("expected SaveGrid to reject a drawer-relative database")
	}

	containers, _ := store.GetContainersByController(ctx, ctrl.ID)
	if len(containers) != 0 {
		t.Fatalf("drawer-space SaveGrid modified the database: %+v", containers)
	}
}

// TestSaveGridRejectsUnresolvedSpace verifies that the grid writer refuses to
// write into an unresolved database.
func TestSaveGridRejectsUnresolvedSpace(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "savegrid_unresolved_space")
	defer dbConn.Close()
	ctx := context.Background()

	if err := ledspace.Set(ctx, store, ledspace.Unresolved); err != nil {
		t.Fatalf("set unresolved: %v", err)
	}

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":0,"width":1,"name":"a1"}]`
	if _, err := svc.SaveGrid(ctx, ctrl.ID, gridData, configData); err == nil {
		t.Fatal("expected SaveGrid to reject an unresolved database")
	}
}

// TestBackfillDrawerAllocationsSkipsDrawer verifies that the segment-based
// allocation backfill never derives allocations from drawer-relative indices.
func TestBackfillDrawerAllocationsSkipsDrawer(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "backfill_drawer")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	cont := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 0)
	mkBin(t, store, ctx, cont, "a1", 3, 1)

	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := BackfillDrawerAllocations(ctx, store, logger); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	c, _ := store.GetContainer(ctx, cont)
	if c.LedCount != 0 {
		t.Fatalf("backfill ran on drawer-relative data: led_count = %d", c.LedCount)
	}
}

func TestExportImportPreservesAllocations(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "exportimport")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)

	data, err := svc.ExportConfig(ctx, ctrl.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	newID, err := svc.ImportConfig(ctx, "C2", "2.2.2.2", 0, data)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	containers, _ := store.GetContainersByController(ctx, newID)
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

// TestExportConfig_IncludesBinIndexSpace verifies that the exported config
// declares the active coordinate space.
func TestExportConfig_IncludesBinIndexSpace(t *testing.T) {
	cases := []struct {
		space string
		want  string
	}{
		{"", ledspace.Segment},
		{ledspace.Segment, ledspace.Segment},
		{ledspace.Drawer, ledspace.Drawer},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			svc, store, dbConn := setupAllocTest(t, "export_space_"+tc.want)
			defer dbConn.Close()
			ctx := context.Background()

			if tc.space != "" {
				if err := ledspace.Set(ctx, store, tc.space); err != nil {
					t.Fatalf("set space: %v", err)
				}
			}
			ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
			mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)

			data, err := svc.ExportConfig(ctx, ctrl.ID)
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			var cfg hardwareConfig
			if err := json.Unmarshal(data, &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if cfg.BinIndexSpace != tc.want {
				t.Errorf("bin_index_space = %q, want %q", cfg.BinIndexSpace, tc.want)
			}
		})
	}
}

// TestImportConfig_CrossSpaceSegmentToDrawer verifies that a segment-relative
// config imported into a drawer-relative database is normalised to
// drawer-relative indices using each drawer's allocation.
func TestImportConfig_CrossSpaceSegmentToDrawer(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "import_seg_to_drawer")
	defer dbConn.Close()
	ctx := context.Background()

	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	// Segment-relative bin index 13 lies inside drawer A's [10,20) allocation.
	data := `{"version":"1.0","bin_index_space":"segment","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":10,"led_count":10,"config":{"type":"linear","total":10}}],"bins":[{"container_index":0,"led_index":13,"width":1,"name":"a1"}]}`
	id, err := svc.ImportConfig(ctx, "", "", 0, []byte(data))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	bins, _ := svc.GetBinsByController(ctx, id)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 3 {
		t.Fatalf("expected drawer-relative index 3, got %+v", bins)
	}
}

// TestImportConfig_CrossSpaceDrawerToSegment verifies that a drawer-relative
// config imported into a segment-relative database is normalised to
// segment-absolute indices using each drawer's allocation.
func TestImportConfig_CrossSpaceDrawerToSegment(t *testing.T) {
	svc, _, dbConn := setupAllocTest(t, "import_drawer_to_seg")
	defer dbConn.Close()
	ctx := context.Background()

	data := `{"version":"1.0","bin_index_space":"drawer","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":10,"led_count":10,"config":{"type":"linear","total":10}}],"bins":[{"container_index":0,"led_index":3,"width":1,"name":"a1"}]}`
	id, err := svc.ImportConfig(ctx, "", "", 0, []byte(data))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	bins, _ := svc.GetBinsByController(ctx, id)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 13 {
		t.Fatalf("expected segment-absolute index 13, got %+v", bins)
	}
}

// TestImportConfig_RejectsUnresolvedDatabase verifies that importing into an
// unresolved database is rejected.
func TestImportConfig_RejectsUnresolvedDatabase(t *testing.T) {
	svc, store, dbConn := setupAllocTest(t, "import_unresolved_db")
	defer dbConn.Close()
	ctx := context.Background()

	if err := ledspace.Set(ctx, store, ledspace.Unresolved); err != nil {
		t.Fatalf("set unresolved: %v", err)
	}

	data := `{"version":"1.0","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}],"bins":[{"container_index":0,"led_index":0,"width":1,"name":"a1"}]}`
	if _, err := svc.ImportConfig(ctx, "", "", 0, []byte(data)); err == nil {
		t.Fatal("expected import into an unresolved database to be rejected")
	}
}

// TestImportConfig_RejectsUnknownBinIndexSpace verifies that an explicit but
// unknown config coordinate space is rejected.
func TestImportConfig_RejectsUnknownBinIndexSpace(t *testing.T) {
	svc, _, dbConn := setupAllocTest(t, "import_unknown_space")
	defer dbConn.Close()
	ctx := context.Background()

	data := `{"version":"1.0","bin_index_space":"bogus","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}],"bins":[{"container_index":0,"led_index":0,"width":1,"name":"a1"}]}`
	if _, err := svc.ImportConfig(ctx, "", "", 0, []byte(data)); err == nil {
		t.Fatal("expected an unknown bin_index_space to be rejected")
	}
}

// TestImportConfig_UnmarkedIsSegmentRelative verifies that an unmarked config is
// treated as segment-relative, preserving the historical format contract.
func TestImportConfig_UnmarkedIsSegmentRelative(t *testing.T) {
	svc, _, dbConn := setupAllocTest(t, "import_unmarked")
	defer dbConn.Close()
	ctx := context.Background()

	data := `{"version":"1.0","controller":{"name":"C","ip_address":"1.1.1.1"},"containers":[{"name":"A","segment_id":0,"led_start":10,"led_count":10,"config":{"type":"linear","total":10}}],"bins":[{"container_index":0,"led_index":13,"width":1,"name":"a1"}]}`
	id, err := svc.ImportConfig(ctx, "", "", 0, []byte(data))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	bins, _ := svc.GetBinsByController(ctx, id)
	if len(bins) != 1 || !bins[0].LedIndex.Valid || bins[0].LedIndex.Int64 != 13 {
		t.Fatalf("expected segment-relative index 13 to be preserved, got %+v", bins)
	}
}
