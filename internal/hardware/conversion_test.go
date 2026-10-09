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
