package hardware

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/hardware/mapper"
	"github.com/tuxedocurly/wledger/internal/ledspace"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func convLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// setupFileAllocTest opens a file-backed database so that SQLite's cross-
// connection locking (rather than shared-cache semantics) is exercised.
func setupFileAllocTest(t *testing.T) (Service, db.Store, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open("file:" + dir + "/conversion.db")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	store := db.NewStore(conn)
	return NewService(store, wled.NewClient(), convLogger()), store, conn
}

// mkBinID creates a mapped bin and returns its id.
func mkBinID(t *testing.T, store db.Store, ctx context.Context, contID int64, name string, idx, width int64) int64 {
	t.Helper()
	id, err := store.CreateBin(ctx, db.CreateBinParams{
		Name:        name,
		ContainerID: contID,
		LedIndex:    sql.NullInt64{Int64: idx, Valid: true},
		Width:       sql.NullInt64{Int64: width, Valid: true},
	})
	if err != nil {
		t.Fatalf("create bin %s: %v", name, err)
	}
	return id
}

func checkBin(t *testing.T, store db.Store, ctx context.Context, containerID int64, name string, want int64) {
	t.Helper()
	bins, err := store.GetBinsByContainer(ctx, containerID)
	if err != nil {
		t.Fatalf("get bins: %v", err)
	}
	for _, b := range bins {
		if b.Name == name {
			if !b.LedIndex.Valid || b.LedIndex.Int64 != want {
				t.Errorf("bin %q index = %d (valid %v), want %d", name, b.LedIndex.Int64, b.LedIndex.Valid, want)
			}
			return
		}
	}
	t.Errorf("bin %q not found in container %d", name, containerID)
}

// assertConsistentState fails if the committed database mixes a coordinate space
// with bin indices that do not belong to it.
func assertConsistentState(t *testing.T, store db.Store) {
	t.Helper()
	ctx := context.Background()
	var problem string
	err := store.ExecTx(ctx, func(q db.Querier) error {
		space, err := ledspace.Current(ctx, q)
		if err != nil {
			return err
		}
		if space == ledspace.Unresolved {
			return nil
		}
		controllers, err := q.GetControllers(ctx)
		if err != nil {
			return err
		}
		for _, ctrl := range controllers {
			containers, err := q.GetContainersByController(ctx, ctrl.ID)
			if err != nil {
				return err
			}
			for _, c := range containers {
				bins, err := q.GetBinsByContainer(ctx, c.ID)
				if err != nil {
					return err
				}
				for _, b := range bins {
					if !b.LedIndex.Valid {
						continue
					}
					idx := b.LedIndex.Int64
					switch space {
					case ledspace.Drawer:
						if c.LedCount <= 0 || idx < 0 || idx >= c.LedCount {
							problem = fmt.Sprintf("mixed state: space=drawer but bin %d index %d is outside drawer [0,%d)", b.ID, idx, c.LedCount)
							return nil
						}
					case ledspace.Segment:
						if idx < c.LedStart || idx >= c.LedStart+c.LedCount {
							problem = fmt.Sprintf("mixed state: space=segment but bin %d index %d is outside [%d,%d)", b.ID, idx, c.LedStart, c.LedStart+c.LedCount)
							return nil
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("assert consistent state: %v", err)
	}
	if problem != "" {
		t.Fatal(problem)
	}
}

func TestConvertToDrawerRelative_Success(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_success")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)
	mkBin(t, store, ctx, a, "a1", 3, 1)
	mkBin(t, store, ctx, a, "a2", 7, 1)
	mkBin(t, store, ctx, b, "b1", 12, 1)
	mkBin(t, store, ctx, b, "b2", 15, 1)

	res, err := ConvertToDrawerRelative(ctx, store, convLogger())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if res.Outcome != ConversionConverted {
		t.Fatalf("outcome = %q, want %q", res.Outcome, ConversionConverted)
	}
	if res.ConvertedBins != 4 {
		t.Errorf("converted bins = %d, want 4", res.ConvertedBins)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Drawer {
		t.Errorf("space = %q, want %q", space, ledspace.Drawer)
	}
	checkBin(t, store, ctx, a, "a1", 3)
	checkBin(t, store, ctx, a, "a2", 7)
	checkBin(t, store, ctx, b, "b1", 2)
	checkBin(t, store, ctx, b, "b2", 5)
	assertConsistentState(t, store)
}

// TestConvertToDrawerRelative_MultipleDrawersOneSegment verifies that several
// drawers sharing one segment are each offset by their own allocation start.
func TestConvertToDrawerRelative_MultipleDrawersOneSegment(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_multi_drawer")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":4}`, 0, 4)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":4}`, 4, 4)
	c := mkContainer(t, store, ctx, ctrl.ID, "C", 0, `{"type":"linear","total":4}`, 8, 4)
	mkBin(t, store, ctx, a, "a1", 1, 1)
	mkBin(t, store, ctx, b, "b1", 5, 1)
	mkBin(t, store, ctx, c, "c1", 11, 1)

	if _, err := ConvertToDrawerRelative(ctx, store, convLogger()); err != nil {
		t.Fatalf("convert: %v", err)
	}
	checkBin(t, store, ctx, a, "a1", 1)
	checkBin(t, store, ctx, b, "b1", 1)
	checkBin(t, store, ctx, c, "c1", 3)
}

func TestConvertToDrawerRelative_InvalidAllocation(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_invalid_alloc")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, -1, 10)
	mkBin(t, store, ctx, a, "a1", 0, 1)

	res, err := ConvertToDrawerRelative(ctx, store, convLogger())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if res.Outcome != ConversionRefused {
		t.Fatalf("outcome = %q, want %q", res.Outcome, ConversionRefused)
	}
	if res.Report.Blocked != 1 {
		t.Errorf("blocked = %d, want 1", res.Report.Blocked)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Segment {
		t.Errorf("space changed to %q after refusal", space)
	}
	checkBin(t, store, ctx, a, "a1", 0)
}

func TestConvertToDrawerRelative_OverlappingAllocations(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_overlap")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 5, 10)
	mkBin(t, store, ctx, a, "a1", 0, 1)
	mkBin(t, store, ctx, b, "b1", 5, 1)

	res, err := ConvertToDrawerRelative(ctx, store, convLogger())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if res.Outcome != ConversionRefused {
		t.Fatalf("outcome = %q, want %q", res.Outcome, ConversionRefused)
	}
	if res.Report.Blocked != 2 {
		t.Errorf("blocked = %d, want 2", res.Report.Blocked)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Segment {
		t.Errorf("space changed to %q after refusal", space)
	}
	checkBin(t, store, ctx, a, "a1", 0)
	checkBin(t, store, ctx, b, "b1", 5)
}

func TestConvertToDrawerRelative_OutOfRangeBin(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_out_of_range")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	mkBin(t, store, ctx, a, "a1", 15, 1)

	res, err := ConvertToDrawerRelative(ctx, store, convLogger())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if res.Outcome != ConversionRefused {
		t.Fatalf("outcome = %q, want %q", res.Outcome, ConversionRefused)
	}
	if res.Report.Blocked != 1 {
		t.Errorf("blocked = %d, want 1", res.Report.Blocked)
	}
	checkBin(t, store, ctx, a, "a1", 15)
}

func TestConvertToDrawerRelative_NullIndicesPreserved(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_null")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	mkBin(t, store, ctx, a, "a1", 3, 1)
	if _, err := store.CreateBin(ctx, db.CreateBinParams{
		Name: "a2", ContainerID: a, LedIndex: sql.NullInt64{Valid: false}, Width: sql.NullInt64{Int64: 1, Valid: true},
	}); err != nil {
		t.Fatalf("create unmapped bin: %v", err)
	}

	if _, err := ConvertToDrawerRelative(ctx, store, convLogger()); err != nil {
		t.Fatalf("convert: %v", err)
	}
	checkBin(t, store, ctx, a, "a1", 3)

	bins, _ := store.GetBinsByContainer(ctx, a)
	for _, b := range bins {
		if b.Name == "a2" && b.LedIndex.Valid {
			t.Errorf("unmapped bin a2 gained an index: %d", b.LedIndex.Int64)
		}
	}
}

// failingStore wraps a Store and injects a failure on one bin update so the
// conversion's rollback behaviour can be exercised.
type failingStore struct {
	db.Store
	failBinID int64
}

func (f *failingStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	return f.Store.ExecImmediateTx(ctx, func(q db.Querier) error {
		return fn(&failingQuerier{Querier: q, failBinID: f.failBinID})
	})
}

type failingQuerier struct {
	db.Querier
	failBinID int64
}

func (f *failingQuerier) UpdateBinLedIndex(ctx context.Context, arg db.UpdateBinLedIndexParams) error {
	if arg.ID == f.failBinID {
		return errors.New("injected conversion failure")
	}
	return f.Querier.UpdateBinLedIndex(ctx, arg)
}

func TestConvertToDrawerRelative_RollbackOnFailure(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_rollback")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":8}`, 2, 8)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)
	mkBin(t, store, ctx, a, "a1", 5, 1)
	failID := mkBinID(t, store, ctx, b, "b1", 12, 1)

	fs := &failingStore{Store: store, failBinID: failID}
	if _, err := ConvertToDrawerRelative(ctx, fs, convLogger()); err == nil {
		t.Fatal("expected the injected failure to abort the conversion")
	}

	if space, _ := ledspace.Current(ctx, store); space != ledspace.Segment {
		t.Errorf("space = %q after rollback, want %q", space, ledspace.Segment)
	}
	checkBin(t, store, ctx, a, "a1", 5)
	checkBin(t, store, ctx, b, "b1", 12)
}

func TestConvertToDrawerRelative_Idempotent(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_idempotent")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 10, 10)
	mkBin(t, store, ctx, a, "a1", 13, 1)

	first, err := ConvertToDrawerRelative(ctx, store, convLogger())
	if err != nil || first.Outcome != ConversionConverted {
		t.Fatalf("first convert: outcome=%q err=%v", first.Outcome, err)
	}
	checkBin(t, store, ctx, a, "a1", 3)

	second, err := ConvertToDrawerRelative(ctx, store, convLogger())
	if err != nil {
		t.Fatalf("second convert: %v", err)
	}
	if second.Outcome != ConversionAlreadyDrawer {
		t.Fatalf("second outcome = %q, want %q", second.Outcome, ConversionAlreadyDrawer)
	}
	if second.ConvertedBins != 0 {
		t.Errorf("second conversion reported %d converted bins, want 0", second.ConvertedBins)
	}
	checkBin(t, store, ctx, a, "a1", 3)
}

func TestConvertToDrawerRelative_Unresolved(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_unresolved")
	defer dbConn.Close()
	ctx := context.Background()

	if err := ledspace.Set(ctx, store, ledspace.Unresolved); err != nil {
		t.Fatalf("set unresolved: %v", err)
	}
	if _, err := ConvertToDrawerRelative(ctx, store, convLogger()); !errors.Is(err, ErrConversionUnresolved) {
		t.Fatalf("expected ErrConversionUnresolved, got %v", err)
	}
}

func TestConvertToDrawerRelative_UnknownState(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_unknown")
	defer dbConn.Close()
	ctx := context.Background()

	if err := store.SetFlag(ctx, db.SetFlagParams{Key: ledspace.FlagKey, Value: "bogus"}); err != nil {
		t.Fatalf("set flag: %v", err)
	}
	if _, err := ConvertToDrawerRelative(ctx, store, convLogger()); err == nil {
		t.Fatal("expected an error for an unknown coordinate space")
	}
}

func TestPreflightConversion_Report(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_preflight")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)
	mkBin(t, store, ctx, a, "a1", 3, 1)
	mkBin(t, store, ctx, b, "b1", 25, 1) // outside [10,20): blocked

	report, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if report.Space != ledspace.Segment {
		t.Errorf("space = %q, want %q", report.Space, ledspace.Segment)
	}
	if report.Convertible != 1 || report.Blocked != 1 {
		t.Errorf("convertible=%d blocked=%d, want 1/1", report.Convertible, report.Blocked)
	}
	if report.AffectedBins != 1 {
		t.Errorf("affected bins = %d, want 1", report.AffectedBins)
	}

	// The preflight must not modify anything.
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Segment {
		t.Errorf("preflight changed the space to %q", space)
	}
	checkBin(t, store, ctx, a, "a1", 3)
	checkBin(t, store, ctx, b, "b1", 25)
}

// TestConvertToDrawerRelative_PhysicalTargetUnchanged verifies that the physical
// WLED target computed for a bin is identical before and after conversion.
func TestConvertToDrawerRelative_PhysicalTargetUnchanged(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_physical")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 2, `{"type":"linear","total":5}`, 10, 5)
	binID := mkBinID(t, store, ctx, a, "a1", 13, 1)

	containers, _ := store.GetContainersByController(ctx, ctrl.ID)
	bin, _ := store.GetBin(ctx, binID)
	segBefore, idxBefore, err := mapper.CalculateGlobalIndex(ledspace.Segment, containers, bin)
	if err != nil {
		t.Fatalf("map before: %v", err)
	}

	if _, err := ConvertToDrawerRelative(ctx, store, convLogger()); err != nil {
		t.Fatalf("convert: %v", err)
	}

	containers, _ = store.GetContainersByController(ctx, ctrl.ID)
	bin, _ = store.GetBin(ctx, binID)
	segAfter, idxAfter, err := mapper.CalculateGlobalIndex(ledspace.Drawer, containers, bin)
	if err != nil {
		t.Fatalf("map after: %v", err)
	}

	if segBefore != segAfter || idxBefore != idxAfter {
		t.Fatalf("physical target changed: before=(%d,%d) after=(%d,%d)", segBefore, idxBefore, segAfter, idxAfter)
	}
	if idxBefore != 13 {
		t.Errorf("physical index = %d, want 13", idxBefore)
	}
}

// TestConvertToDrawerRelative_ConcurrentSaveGrid verifies that a grid save racing
// a conversion can never leave the database in a mixed coordinate state.
func TestConvertToDrawerRelative_ConcurrentSaveGrid(t *testing.T) {
	svc, store, dbConn := setupFileAllocTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)

	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":3,"width":1,"name":"a1"}]`

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = ConvertToDrawerRelative(ctx, store, convLogger())
	}()
	go func() {
		defer wg.Done()
		_, _ = svc.SaveGrid(ctx, ctrl.ID, gridData, configData)
	}()
	wg.Wait()

	assertConsistentState(t, store)
}

// TestConvertToDrawerRelative_ConcurrentLocate verifies that a locate racing a
// conversion always targets the same physical LED, because the conversion
// preserves the physical mapping.
func TestConvertToDrawerRelative_ConcurrentLocate(t *testing.T) {
	var mu sync.Mutex
	var payloads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		payloads = append(payloads, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	_, store, dbConn := setupFileAllocTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 2, `{"type":"linear","total":5}`, 10, 5)
	binID := mkBinID(t, store, ctx, a, "a1", 13, 1)

	wsvc := wled.NewService(store, wled.NewClient(), convLogger())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = ConvertToDrawerRelative(ctx, store, convLogger())
	}()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = wsvc.LocateBin(ctx, ctrl.ID, binID)
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) == 0 {
		t.Fatal("no locate requests were captured")
	}
	for _, body := range payloads {
		if !strings.Contains(body, `"i":[13,14`) {
			t.Errorf("locate targeted a different physical LED: %s", body)
		}
	}
}

// TestPreflightConversion_EmptyDrawerDoesNotBlock verifies that an unallocated
// drawer with no mapped bins is harmless and does not block a conversion, while
// still being reported distinctly from an invalid mapped drawer.
func TestPreflightConversion_EmptyDrawerDoesNotBlock(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_preflight_empty")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	mkBin(t, store, ctx, a, "a1", 3, 1)
	// Unallocated and unmapped: harmless, must not block.
	mkContainer(t, store, ctx, ctrl.ID, "Empty", 0, `{"type":"linear","total":4}`, 0, 0)

	report, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if report.Blocked != 0 {
		t.Fatalf("blocked = %d, want 0 (an empty drawer must not block)", report.Blocked)
	}
	if report.Convertible != 2 {
		t.Errorf("convertible = %d, want 2", report.Convertible)
	}
	if report.AffectedBins != 1 {
		t.Errorf("affected bins = %d, want 1", report.AffectedBins)
	}

	res, err := ConvertToDrawerRelative(ctx, store, convLogger())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if res.Outcome != ConversionConverted {
		t.Fatalf("outcome = %q, want %q", res.Outcome, ConversionConverted)
	}
}

// TestConvertToDrawerRelative_PhysicalTargetUnchangedVariableWidth verifies that
// variable-width bins across multiple drawers keep the same physical LED target
// before and after conversion.
func TestConvertToDrawerRelative_PhysicalTargetUnchangedVariableWidth(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_physical_width")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)
	binA := mkBinID(t, store, ctx, a, "a1", 2, 4)  // [2,6)
	binB := mkBinID(t, store, ctx, b, "b1", 12, 3) // [12,15)
	binC := mkBinID(t, store, ctx, b, "b2", 17, 3) // [17,20)

	before := map[int64][2]int64{}
	for _, id := range []int64{binA, binB, binC} {
		containers, _ := store.GetContainersByController(ctx, ctrl.ID)
		bin, _ := store.GetBin(ctx, id)
		seg, idx, err := mapper.CalculateGlobalIndex(ledspace.Segment, containers, bin)
		if err != nil {
			t.Fatalf("map before bin %d: %v", id, err)
		}
		before[id] = [2]int64{seg, idx}
	}

	if _, err := ConvertToDrawerRelative(ctx, store, convLogger()); err != nil {
		t.Fatalf("convert: %v", err)
	}

	for _, id := range []int64{binA, binB, binC} {
		containers, _ := store.GetContainersByController(ctx, ctrl.ID)
		bin, _ := store.GetBin(ctx, id)
		seg, idx, err := mapper.CalculateGlobalIndex(ledspace.Drawer, containers, bin)
		if err != nil {
			t.Fatalf("map after bin %d: %v", id, err)
		}
		if before[id] != [2]int64{seg, idx} {
			t.Errorf("bin %d physical target changed: before=%v after=(%d,%d)", id, before[id], seg, idx)
		}
	}
}

// TestConvertToDrawerRelative_LocatePartAndFlashEquivalence verifies that
// LocatePart and FlashError target the same physical LED before and after
// conversion.
func TestConvertToDrawerRelative_LocatePartAndFlashEquivalence(t *testing.T) {
	var mu sync.Mutex
	var payloads []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		payloads = append(payloads, string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ip := server.URL[7:]

	_, store, dbConn := setupFileAllocTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: ip})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 2, `{"type":"linear","total":5}`, 10, 5)
	binID := mkBinID(t, store, ctx, a, "a1", 13, 1)
	partID, err := store.CreatePart(ctx, db.CreatePartParams{Name: "P"})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID: partID, BinID: sql.NullInt64{Int64: binID, Valid: true}, Quantity: 1,
	}); err != nil {
		t.Fatalf("create assignment: %v", err)
	}

	wsvc := wled.NewService(store, wled.NewClient(), convLogger())
	run := func() {
		if err := wsvc.LocatePart(ctx, partID); err != nil {
			t.Fatalf("LocatePart: %v", err)
		}
		if err := wsvc.FlashError(ctx, ctrl.ID, binID); err != nil {
			t.Fatalf("FlashError: %v", err)
		}
	}

	run() // segment space
	if _, err := ConvertToDrawerRelative(ctx, store, convLogger()); err != nil {
		t.Fatalf("convert: %v", err)
	}
	run() // drawer space

	// LocatePart is synchronous; FlashError is asynchronous. Wait for at least
	// the two synchronous LocatePart requests plus one flash request.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(payloads)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) < 3 {
		t.Fatalf("expected at least 3 WLED requests, got %d", len(payloads))
	}
	for _, body := range payloads {
		if !strings.Contains(body, `"i":[13,14`) {
			t.Errorf("payload targeted a different physical LED: %s", body)
		}
	}
}

// TestConversionFingerprint_Deterministic verifies that the fingerprint of an
// unchanged database state is stable across preflight runs.
func TestConversionFingerprint_Deterministic(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "fingerprint_deterministic")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)
	mkBin(t, store, ctx, a, "a1", 3, 1)
	mkBin(t, store, ctx, b, "b1", 12, 2)

	r1, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight 1: %v", err)
	}
	r2, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight 2: %v", err)
	}
	if r1.Fingerprint() != r2.Fingerprint() {
		t.Fatalf("fingerprint not deterministic: %s != %s", r1.Fingerprint(), r2.Fingerprint())
	}
	if r1.TotalDrawers != 2 {
		t.Errorf("total drawers = %d, want 2", r1.TotalDrawers)
	}
}

// TestConversionFingerprint_ChangesWithState verifies that any relevant database
// change alters the fingerprint, so a stale preview can be detected.
func TestConversionFingerprint_ChangesWithState(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "fingerprint_changes")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	binID := mkBinID(t, store, ctx, a, "a1", 3, 1)

	before, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}

	if err := store.UpdateBinLedIndex(ctx, db.UpdateBinLedIndexParams{
		LedIndex: sql.NullInt64{Int64: 4, Valid: true}, ID: binID,
	}); err != nil {
		t.Fatalf("mutate bin: %v", err)
	}

	after, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight after: %v", err)
	}
	if before.Fingerprint() == after.Fingerprint() {
		t.Fatal("fingerprint unchanged after a relevant state change")
	}
}

// TestConvertToDrawerRelativeConfirmed_Success verifies that a confirmation whose
// fingerprint matches the reviewed preflight converts and commits.
func TestConvertToDrawerRelativeConfirmed_Success(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "confirmed_success")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	b := mkContainer(t, store, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)
	mkBin(t, store, ctx, a, "a1", 3, 1)
	mkBin(t, store, ctx, b, "b1", 12, 1)

	fp, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}

	res, err := ConvertToDrawerRelativeConfirmed(ctx, store, fp.Fingerprint(), convLogger())
	if err != nil {
		t.Fatalf("confirmed convert: %v", err)
	}
	if res.Outcome != ConversionConverted {
		t.Fatalf("outcome = %q, want converted", res.Outcome)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Drawer {
		t.Fatalf("space = %q, want drawer", space)
	}
	checkBin(t, store, ctx, a, "a1", 3)
	checkBin(t, store, ctx, b, "b1", 2)
	assertConsistentState(t, store)
}

// TestConvertToDrawerRelativeConfirmed_StaleRejected verifies that a confirmation
// against a changed database is rejected without modifying anything.
func TestConvertToDrawerRelativeConfirmed_StaleRejected(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "confirmed_stale")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	binID := mkBinID(t, store, ctx, a, "a1", 3, 1)

	fp, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}

	if err := store.UpdateBinLedIndex(ctx, db.UpdateBinLedIndexParams{
		LedIndex: sql.NullInt64{Int64: 4, Valid: true}, ID: binID,
	}); err != nil {
		t.Fatalf("mutate bin: %v", err)
	}

	res, err := ConvertToDrawerRelativeConfirmed(ctx, store, fp.Fingerprint(), convLogger())
	if err != nil {
		t.Fatalf("confirmed convert: %v", err)
	}
	if res.Outcome != ConversionStale {
		t.Fatalf("outcome = %q, want stale", res.Outcome)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Segment {
		t.Fatalf("stale conversion changed space to %q", space)
	}
	checkBin(t, store, ctx, a, "a1", 4)
}

// TestConvertToDrawerRelativeConfirmed_RefusedWhenBlocked verifies that a matching
// fingerprint over a blocked preflight is still refused and changes nothing.
func TestConvertToDrawerRelativeConfirmed_RefusedWhenBlocked(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "confirmed_blocked")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	mkBin(t, store, ctx, a, "bad", 15, 1)

	fp, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if fp.Blocked != 1 {
		t.Fatalf("blocked = %d, want 1", fp.Blocked)
	}

	res, err := ConvertToDrawerRelativeConfirmed(ctx, store, fp.Fingerprint(), convLogger())
	if err != nil {
		t.Fatalf("confirmed convert: %v", err)
	}
	if res.Outcome != ConversionRefused {
		t.Fatalf("outcome = %q, want refused", res.Outcome)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Segment {
		t.Fatalf("refused conversion changed space to %q", space)
	}
}

// TestConvertToDrawerRelativeConfirmed_AlreadyDrawer verifies the already-converted
// state is reported without change.
func TestConvertToDrawerRelativeConfirmed_AlreadyDrawer(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "confirmed_already")
	defer dbConn.Close()
	ctx := context.Background()
	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatalf("set drawer: %v", err)
	}

	fp, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	res, err := ConvertToDrawerRelativeConfirmed(ctx, store, fp.Fingerprint(), convLogger())
	if err != nil {
		t.Fatalf("confirmed convert: %v", err)
	}
	if res.Outcome != ConversionAlreadyDrawer {
		t.Fatalf("outcome = %q, want already_drawer", res.Outcome)
	}
}

// TestConvertToDrawerRelativeConfirmed_Unresolved verifies an unresolved space is
// rejected with a distinct error.
func TestConvertToDrawerRelativeConfirmed_Unresolved(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "confirmed_unresolved")
	defer dbConn.Close()
	ctx := context.Background()
	if err := ledspace.Set(ctx, store, ledspace.Unresolved); err != nil {
		t.Fatalf("set unresolved: %v", err)
	}

	_, err := ConvertToDrawerRelativeConfirmed(ctx, store, "any", convLogger())
	if !errors.Is(err, ErrConversionUnresolved) {
		t.Fatalf("err = %v, want ErrConversionUnresolved", err)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Unresolved {
		t.Fatalf("space = %q, want unresolved", space)
	}
}

// TestConvertToDrawerRelativeConfirmed_ConcurrentSaveGrid verifies that a
// confirmation racing an ordinary grid save never leaves a mixed coordinate state
// and never partially converts.
func TestConvertToDrawerRelativeConfirmed_ConcurrentSaveGrid(t *testing.T) {
	svc, store, dbConn := setupFileAllocTest(t)
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)

	fp, err := PreflightConversion(ctx, store)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}

	configData := `[{"id":null,"name":"A","segment_id":0,"led_start":0,"led_count":10,"config":{"type":"linear","total":10}}]`
	gridData := `[{"container_index":0,"x":0,"y":0,"led_index":3,"width":1,"name":"a1"}]`

	var wg sync.WaitGroup
	wg.Add(2)
	var outcome ConversionOutcome
	go func() {
		defer wg.Done()
		res, _ := ConvertToDrawerRelativeConfirmed(ctx, store, fp.Fingerprint(), convLogger())
		outcome = res.Outcome
	}()
	go func() {
		defer wg.Done()
		_, _ = svc.SaveGrid(ctx, ctrl.ID, gridData, configData)
	}()
	wg.Wait()

	if outcome != ConversionConverted && outcome != ConversionStale && outcome != ConversionRefused {
		t.Fatalf("unexpected outcome %q", outcome)
	}
	assertConsistentState(t, store)
}

// failingAuditStore injects a failure into the conversion's audit write so the
// atomicity of conversion + audit can be exercised.
type failingAuditStore struct {
	db.Store
}

func (f *failingAuditStore) ExecImmediateTx(ctx context.Context, fn func(db.Querier) error) error {
	return f.Store.ExecImmediateTx(ctx, func(q db.Querier) error {
		return fn(&failingAuditQuerier{Querier: q})
	})
}

type failingAuditQuerier struct {
	db.Querier
}

func (f *failingAuditQuerier) CreateAuditLog(ctx context.Context, arg db.CreateAuditLogParams) error {
	return errors.New("injected audit failure")
}

// TestConvertToDrawerRelative_AuditFailureRollsBack verifies that the conversion
// and its audit entry are atomic: if the audit write fails, the whole conversion
// is rolled back and is never reported as a success.
func TestConvertToDrawerRelative_AuditFailureRollsBack(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "convert_audit_fail")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	mkBin(t, store, ctx, a, "a1", 3, 1)

	fs := &failingAuditStore{Store: store}
	if _, err := ConvertToDrawerRelative(ctx, fs, convLogger()); err == nil {
		t.Fatal("expected the injected audit failure to abort the conversion")
	}

	if space, _ := ledspace.Current(ctx, store); space != ledspace.Segment {
		t.Errorf("space = %q after audit failure, want segment", space)
	}
	checkBin(t, store, ctx, a, "a1", 3)
}

// TestConversionFingerprint_SensitiveToRelevantChanges verifies that each piece of
// conversion-relevant state is covered by the fingerprint.
func TestConversionFingerprint_SensitiveToRelevantChanges(t *testing.T) {
	t.Run("bin width", func(t *testing.T) {
		_, store, dbConn := setupAllocTest(t, "fp_width")
		defer dbConn.Close()
		ctx := context.Background()
		ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
		a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
		binID := mkBinID(t, store, ctx, a, "a1", 2, 1)
		before, _ := PreflightConversion(ctx, store)
		if err := store.UpdateBin(ctx, db.UpdateBinParams{
			ID: binID, Name: "a1", LedIndex: sql.NullInt64{Int64: 2, Valid: true},
			Width: sql.NullInt64{Int64: 3, Valid: true},
		}); err != nil {
			t.Fatalf("update width: %v", err)
		}
		after, _ := PreflightConversion(ctx, store)
		if before.Fingerprint() == after.Fingerprint() {
			t.Fatal("a bin-width change did not change the fingerprint")
		}
	})

	t.Run("drawer allocation", func(t *testing.T) {
		_, store, dbConn := setupAllocTest(t, "fp_alloc")
		defer dbConn.Close()
		ctx := context.Background()
		ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
		a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
		mkBin(t, store, ctx, a, "a1", 2, 1)
		before, _ := PreflightConversion(ctx, store)
		if err := store.UpdateContainerConfig(ctx, db.UpdateContainerConfigParams{
			ID: a, Name: "A", SegmentID: 0, PositionIndex: 0, LedStart: 0, LedCount: 20,
			ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
		}); err != nil {
			t.Fatalf("update allocation: %v", err)
		}
		after, _ := PreflightConversion(ctx, store)
		if before.Fingerprint() == after.Fingerprint() {
			t.Fatal("an allocation change did not change the fingerprint")
		}
	})

	t.Run("blocked condition", func(t *testing.T) {
		_, store, dbConn := setupAllocTest(t, "fp_blocked")
		defer dbConn.Close()
		ctx := context.Background()
		ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
		a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
		binID := mkBinID(t, store, ctx, a, "a1", 2, 1)
		before, _ := PreflightConversion(ctx, store)
		if err := store.UpdateBinLedIndex(ctx, db.UpdateBinLedIndexParams{
			LedIndex: sql.NullInt64{Int64: 25, Valid: true}, ID: binID,
		}); err != nil {
			t.Fatalf("update index: %v", err)
		}
		after, _ := PreflightConversion(ctx, store)
		if after.Blocked != 1 {
			t.Fatalf("expected the drawer to become blocked, got %d", after.Blocked)
		}
		if before.Fingerprint() == after.Fingerprint() {
			t.Fatal("a blocking condition did not change the fingerprint")
		}
	})

	t.Run("coordinate state", func(t *testing.T) {
		_, store, dbConn := setupAllocTest(t, "fp_space")
		defer dbConn.Close()
		ctx := context.Background()
		ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
		a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
		mkBin(t, store, ctx, a, "a1", 2, 1)
		before, _ := PreflightConversion(ctx, store)
		if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
			t.Fatalf("set drawer: %v", err)
		}
		after, _ := PreflightConversion(ctx, store)
		if before.Fingerprint() == after.Fingerprint() {
			t.Fatal("a coordinate-state change did not change the fingerprint")
		}
	})
}

// TestConversionFingerprint_BinOwnership verifies that the drawer a bin belongs
// to is reflected in the fingerprint.
func TestConversionFingerprint_BinOwnership(t *testing.T) {
	ctx := context.Background()

	_, storeA, dbA := setupAllocTest(t, "fp_owner_a")
	defer dbA.Close()
	ctrl, _ := storeA.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	ca := mkContainer(t, storeA, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	cb := mkContainer(t, storeA, ctx, ctrl.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)
	mkBin(t, storeA, ctx, ca, "x", 2, 1)
	mkBin(t, storeA, ctx, cb, "y", 12, 1)

	_, storeB, dbB := setupAllocTest(t, "fp_owner_b")
	defer dbB.Close()
	ctrlB, _ := storeB.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	caB := mkContainer(t, storeB, ctx, ctrlB.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	cbB := mkContainer(t, storeB, ctx, ctrlB.ID, "B", 0, `{"type":"linear","total":10}`, 10, 10)
	// Same index (2, drawer-relative to its own drawer) but owned by a different drawer.
	mkBin(t, storeB, ctx, caB, "x", 2, 1)
	mkBin(t, storeB, ctx, cbB, "y", 2, 1)

	rA, _ := PreflightConversion(ctx, storeA)
	rB, _ := PreflightConversion(ctx, storeB)
	if rA.Fingerprint() == rB.Fingerprint() {
		t.Fatal("differing bin ownership produced the same fingerprint")
	}
}

// TestConvertToDrawerRelativeConfirmed_WidthChangeStale verifies that a width-only
// change between preview and confirmation is rejected as stale.
func TestConvertToDrawerRelativeConfirmed_WidthChangeStale(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "confirmed_width_stale")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	binID := mkBinID(t, store, ctx, a, "a1", 2, 1)

	fp, _ := PreflightConversion(ctx, store)
	if err := store.UpdateBin(ctx, db.UpdateBinParams{
		ID: binID, Name: "a1", LedIndex: sql.NullInt64{Int64: 2, Valid: true},
		Width: sql.NullInt64{Int64: 4, Valid: true},
	}); err != nil {
		t.Fatalf("update width: %v", err)
	}

	res, err := ConvertToDrawerRelativeConfirmed(ctx, store, fp.Fingerprint(), convLogger())
	if err != nil {
		t.Fatalf("confirmed convert: %v", err)
	}
	if res.Outcome != ConversionStale {
		t.Fatalf("outcome = %q, want stale", res.Outcome)
	}
	if space, _ := ledspace.Current(ctx, store); space != ledspace.Segment {
		t.Fatalf("stale width change converted the database to %q", space)
	}
}

// TestConvertToDrawerRelativeConfirmed_AllocationChangeStale verifies that an
// allocation change between preview and confirmation is rejected as stale.
func TestConvertToDrawerRelativeConfirmed_AllocationChangeStale(t *testing.T) {
	_, store, dbConn := setupAllocTest(t, "confirmed_alloc_stale")
	defer dbConn.Close()
	ctx := context.Background()

	ctrl, _ := store.CreateController(ctx, db.CreateControllerParams{Name: "C", IpAddress: "1.1.1.1"})
	a := mkContainer(t, store, ctx, ctrl.ID, "A", 0, `{"type":"linear","total":10}`, 0, 10)
	mkBin(t, store, ctx, a, "a1", 2, 1)

	fp, _ := PreflightConversion(ctx, store)
	if err := store.UpdateContainerConfig(ctx, db.UpdateContainerConfigParams{
		ID: a, Name: "A", SegmentID: 0, PositionIndex: 0, LedStart: 0, LedCount: 20,
		ConfigJson: sql.NullString{String: `{"type":"linear","total":10}`, Valid: true},
	}); err != nil {
		t.Fatalf("update allocation: %v", err)
	}

	res, err := ConvertToDrawerRelativeConfirmed(ctx, store, fp.Fingerprint(), convLogger())
	if err != nil {
		t.Fatalf("confirmed convert: %v", err)
	}
	if res.Outcome != ConversionStale {
		t.Fatalf("outcome = %q, want stale", res.Outcome)
	}
}
