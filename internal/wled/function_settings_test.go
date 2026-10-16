package wled

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/ledspace"
)

func functionSettingsDB(t *testing.T) *db.Queries {
	t.Helper()
	conn, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(`CREATE TABLE system_flags (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	return db.New(conn)
}

type functionFlagStore struct {
	db.Store
	q db.Querier
}

func (s functionFlagStore) GetFlag(ctx context.Context, key string) (string, error) {
	return s.q.GetFlag(ctx, key)
}

func TestFunctionSettingsLegacyCompatibility(t *testing.T) {
	q := functionSettingsDB(t)
	ctx := context.Background()
	service := &service{store: functionFlagStore{q: q}}
	style, err := service.legacyFunctionStyle(ctx, "error", "#ABCDEF")
	if err != nil {
		t.Fatal(err)
	}
	color, err := style.legacyColor()
	if err != nil || color != "#ABCDEF" {
		t.Fatal(color, err)
	}
	if err := SaveFunctionSettings(ctx, q, FunctionSettings{"error": {Color: "#123456", Brightness: 128}}); err != nil {
		t.Fatal(err)
	}
	style, err = service.legacyFunctionStyle(ctx, "error", "#ABCDEF")
	if err != nil {
		t.Fatal(err)
	}
	color, err = style.legacyColor()
	if err != nil || color != "#091A2B" {
		t.Fatal(color, err)
	}
	if err := q.SetFlag(ctx, db.SetFlagParams{Key: FunctionSettingsKey, Value: "null"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.legacyFunctionStyle(ctx, "error", "#ABCDEF"); err == nil {
		t.Fatal("malformed central settings fell back to legacy")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if got, err := LoadFunctionSettings(cancelled, q); err == nil || got != nil {
		t.Fatal("database error silently defaulted", got, err)
	}
}

func TestFunctionSettingsLegacyMissingSettings(t *testing.T) {
	t.Setenv("WLEDGER_PHYSICAL_OUTPUT_ENABLED", "true")
	var mu sync.Mutex
	var requests string
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		requests += string(body) + "\n"
		hits++
		mu.Unlock()
		writeWLEDWriteSuccess(w)
	}))
	defer server.Close()
	snapshot := func() (string, int) { mu.Lock(); defer mu.Unlock(); return requests, hits }
	conn, err := db.Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(conn)
	ctx := context.Background()
	if _, err := store.GetSettings(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no legacy settings row: %v", err)
	}
	controller, err := store.CreateController(ctx, db.CreateControllerParams{Name: "mock", IpAddress: server.URL[7:]})
	if err != nil {
		t.Fatal(err)
	}
	container, err := store.CreateContainer(ctx, db.CreateContainerParams{Name: "legacy", ControllerID: controller.ID, SegmentID: 2, LedStart: 10, LedCount: 5, ConfigJson: sql.NullString{String: `{"type":"linear","total":5}`, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	bin, err := store.CreateBin(ctx, db.CreateBinParams{Name: "bin", ContainerID: sql.NullInt64{Int64: container, Valid: true}, LedIndex: sql.NullInt64{Int64: 2, Valid: true}, Width: sql.NullInt64{Int64: 1, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	part, err := store.CreatePart(ctx, db.CreatePartParams{Name: "part"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{PartID: part, BinID: sql.NullInt64{Int64: bin, Valid: true}, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	if err := ledspace.Set(ctx, store, ledspace.Drawer); err != nil {
		t.Fatal(err)
	}
	svc := NewService(store, NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	operations := []struct {
		name     string
		run      func() error
		function string
	}{
		{"LocatePart", func() error { return svc.LocatePart(ctx, part) }, "locate_part"},
		{"LocateBin", func() error { return svc.LocateBin(ctx, controller.ID, bin) }, "locate_bin"},
		{"LocateDrawer", func() error { return svc.LocateDrawer(ctx, controller.ID, container) }, "locate_drawer"},
		{"FlashError", func() error { return svc.FlashError(ctx, controller.ID, bin) }, "error"},
	}
	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			previous, _ := snapshot()
			if err := op.run(); err != nil {
				t.Fatal(err)
			}
			body, _ := snapshot()
			body = body[len(previous):]
			expected := "[0,0,255]"
			if op.function == "error" {
				expected = "[255,0,0]"
			}
			if !strings.Contains(body, expected) {
				t.Fatalf("legacy fallback missing %s: %s", expected, body)
			}
		})
	}
	central := DefaultFunctionSettings()
	for name := range central {
		central[name] = FunctionStyle{Color: "#123456", Brightness: 255}
	}
	if err := SaveFunctionSettings(ctx, store, central); err != nil {
		t.Fatal(err)
	}
	for _, op := range operations {
		t.Run(op.name+"Central", func(t *testing.T) {
			previous, _ := snapshot()
			if err := op.run(); err != nil {
				t.Fatal(err)
			}
			body, _ := snapshot()
			body = body[len(previous):]
			if !strings.Contains(body, "[18,52,86]") {
				t.Fatalf("central color ignored: %s", body)
			}
		})
	}
	if err := store.SetFlag(ctx, db.SetFlagParams{Key: FunctionSettingsKey, Value: "null"}); err != nil {
		t.Fatal(err)
	}
	_, before := snapshot()
	for _, op := range operations {
		t.Run(op.name+"Malformed", func(t *testing.T) {
			if err := op.run(); err == nil {
				t.Fatal("malformed central flag accepted")
			}
		})
	}
	if _, after := snapshot(); after != before {
		t.Fatalf("malformed flag sent commands: %d -> %d", before, after)
	}
}

func TestFunctionStyleLegacyColorRejectsInvalid(t *testing.T) {
	for _, style := range []FunctionStyle{{Color: "#GG0000", Brightness: 128}, {Color: "rgb(256,0,0)", Brightness: 128}, {Color: "#FFFFFF", Brightness: -1}, {Color: "#FFFFFF", Brightness: 256}} {
		if color, err := style.legacyColor(); err == nil || color != "" {
			t.Fatalf("invalid style returned color: %q %v", color, err)
		}
	}
}

func TestFunctionSettingsDefaultsAndPersistence(t *testing.T) {
	q := functionSettingsDB(t)
	ctx := context.Background()
	defaults := DefaultFunctionSettings()
	if len(defaults) != 10 {
		t.Fatal(defaults)
	}
	expected := []string{"#00FF00", "#0000FF", "#FFFF00", "#800080", "#FFFFFF", "#00FFFF", "#FFA500", "#FF0000", "#00FF00", "#FF00FF"}
	for i, name := range FunctionNames() {
		if defaults[name] != (FunctionStyle{expected[i], 128}) {
			t.Fatal(name, defaults[name])
		}
	}
	got, err := LoadFunctionSettings(ctx, q)
	if err != nil || !reflect.DeepEqual(got, defaults) {
		t.Fatalf("missing: %v %v", got, err)
	}
	got["locate_part"] = FunctionStyle{"rgb(12, 34, 56)", 0}
	if err := SaveFunctionSettings(ctx, q, got); err != nil {
		t.Fatal(err)
	}
	got, err = LoadFunctionSettings(ctx, q)
	if err != nil || got["locate_part"] != (FunctionStyle{"#0C2238", 0}) {
		t.Fatalf("roundtrip: %v %v", got, err)
	}
	if DefaultFunctionSettings()["locate_part"] != defaults["locate_part"] {
		t.Fatal("defaults mutated")
	}
	if err := q.SetFlag(ctx, db.SetFlagParams{Key: FunctionSettingsKey, Value: `{"test_pixel":{"color":"abcdef","brightness":255}}`}); err != nil {
		t.Fatal(err)
	}
	got, err = LoadFunctionSettings(ctx, q)
	if err != nil || len(got) != 10 || got["test_pixel"].Color != "#ABCDEF" || got["error"] != defaults["error"] {
		t.Fatalf("partial: %v %v", got, err)
	}
	if err := SaveFunctionSettings(ctx, q, DefaultFunctionSettings()); err != nil {
		t.Fatal(err)
	}
	got, err = LoadFunctionSettings(ctx, q)
	if err != nil || !reflect.DeepEqual(got, defaults) {
		t.Fatalf("reset: %v %v", got, err)
	}
}

func TestFunctionSettingsRejectMalformed(t *testing.T) {
	q := functionSettingsDB(t)
	ctx := context.Background()
	for _, raw := range []string{"", "null", "[]", "42", "{", "{} {}", `{"unknown":{"color":"#FFFFFF","brightness":128}}`, `{"error":null}`, `{"error":{"color":"#FF0000"}}`, `{"error":{"brightness":128}}`, `{"error":{"color":"red","brightness":128}}`, `{"error":{"color":"#FF0000","brightness":256}}`, `{"error":{"color":"#FF0000","brightness":-1}}`, `{"error":{"color":"#FF0000","brightness":1.5}}`, `{"error":{"color":"#FF0000","brightness":"128"}}`, `{"error":{"color":"#FF0000","brightness":128,"extra":true}}`, `{"error":{"color":"#FF0000","Color":"#FFFFFF","brightness":128}}`, `{"error":{"color":"#FF0000","brightness":128,"brightness":255}}`, `{"error":{"color":"#FF0000","brightness":128},"error":{"color":"#FFFFFF","brightness":1}}`} {
		t.Run(raw, func(t *testing.T) {
			if err := q.SetFlag(ctx, db.SetFlagParams{Key: FunctionSettingsKey, Value: raw}); err != nil {
				t.Fatal(err)
			}
			if got, err := LoadFunctionSettings(ctx, q); err == nil || got != nil {
				t.Fatalf("accepted malformed persisted settings: %v %v", got, err)
			}
		})
	}
}

func TestFunctionSettingsColorsAndBrightness(t *testing.T) {
	for input, expected := range map[string]string{"#abcdef": "#ABCDEF", "123456": "#123456", " rgb(0, 128, 255) ": "#0080FF", "RGB(255,0,1)": "#FF0001"} {
		got, err := NormalizeFunctionColor(input)
		if err != nil || got != expected {
			t.Fatalf("%q: %q %v", input, got, err)
		}
	}
	for _, input := range []string{"red", "#abc", "#12345678", "rgb(-1,0,0)", "rgb(256,0,0)", "rgb(1.5,0,0)", "rgba(1,2,3,1)", "#GG0000", "rgb(1,2,3)junk"} {
		if _, err := NormalizeFunctionColor(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	for _, brightness := range []int{0, 1, 128, 255} {
		rgb, err := (FunctionStyle{"#FF8040", brightness}).RGB()
		if err != nil || rgb != [3]int{brightness, 128 * brightness / 255, 64 * brightness / 255} {
			t.Fatal(rgb, err)
		}
	}
	q := functionSettingsDB(t)
	ctx := context.Background()
	if err := SaveFunctionSettings(ctx, q, FunctionSettings{"error": {"#FF0000", 256}}); err == nil {
		t.Fatal("invalid save accepted")
	}
	if _, err := q.GetFlag(ctx, FunctionSettingsKey); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("invalid save wrote data", err)
	}
	for _, brightness := range []int{-1, 256} {
		if _, err := (FunctionStyle{"#FFFFFF", brightness}).RGB(); err == nil {
			t.Fatal("invalid RGB brightness accepted")
		}
	}
}
