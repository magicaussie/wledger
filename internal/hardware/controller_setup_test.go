package hardware

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/controllerconfig"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wiringdesigner"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func TestGuidedSetupCommitsTogetherAndRollsBackOnLayoutFailure(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "setup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = db.Migrate(conn); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(conn)
	svc := NewService(store, wled.NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	params := db.CreateControllerParams{Name: "Offline draft", IpAddress: "127.0.0.1", Port: sql.NullInt64{Int64: 65534, Valid: true}}
	draft := func() (controllerconfig.Settings, wiringdesigner.Layout) {
		return controllerconfig.Settings{ControllerType: "wled", Outputs: []controllerconfig.Output{{ID: -1, Name: "A", PixelCount: 9}, {ID: -2, DisplayOrder: 1, Name: "B", PixelCount: 7}}}, wiringdesigner.Layout{Version: 1, Width: 1200, Height: 800, Outputs: []wiringdesigner.OutputLayout{{OutputID: -1, X: 40, Y: 40, Width: 240, Height: 180, Axis: "rows", Rows: []int64{4, 2, 3}, Sections: []int64{9}}, {OutputID: -2, X: 320, Y: 40, Width: 240, Height: 180, Axis: "columns", Rows: []int64{2, 5}, Sections: []int64{7}}}}
	}
	count := func(table string) int {
		t.Helper()
		var n int
		if e := conn.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	if _, err = conn.Exec("CREATE TRIGGER fail_setup_flag BEFORE INSERT ON system_flags WHEN NEW.key LIKE 'led_wiring_designer_v1:%' BEGIN SELECT RAISE(ABORT,'forced layout failure'); END"); err != nil {
		t.Fatal(err)
	}
	settings, layout := draft()
	if id, e := svc.CreateControllerWithLayout(ctx, params, settings, layout); e == nil || id != 0 {
		t.Fatalf("failure must roll back: id=%d err=%v", id, e)
	}
	if count("controllers") != 0 || count("controller_outputs") != 0 {
		t.Fatal("failed save retained controller/output rows")
	}
	if _, err = conn.Exec("DROP TRIGGER fail_setup_flag"); err != nil {
		t.Fatal(err)
	}
	settings, layout = draft()
	id, err := svc.CreateControllerWithLayout(ctx, params, settings, layout)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := svc.GetControllerSettings(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Outputs) != 2 {
		t.Fatal(saved)
	}
	raw, err := store.GetFlag(ctx, "led_wiring_designer_v1:1")
	if err != nil {
		t.Fatal(err)
	}
	var document wiringdesigner.Layout
	if err = json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	pixels := map[int64]int64{}
	for _, output := range saved.Outputs {
		if output.ID <= 0 {
			t.Fatal("draft ID persisted")
		}
		pixels[output.ID] = output.PixelCount
	}
	if err = wiringdesigner.Validate(document, pixels); err != nil {
		t.Fatal(err)
	}
	if document.Revision != 1 || document.TopologySignature == "" || document.Outputs[0].Rows[1] != 2 {
		t.Fatal("graphical layout not committed")
	}
	settings, layout = draft()
	if _, err = svc.CreateControllerWithLayout(ctx, params, settings, layout); err == nil {
		t.Fatal("duplicate endpoint accepted")
	}
	if count("controllers") != 1 || count("controller_outputs") != 2 {
		t.Fatal("duplicate save changed records")
	}
	settings, layout = draft()
	settings.Outputs[1].ID = -1
	params.Port.Int64 = 65533
	if _, err = svc.CreateControllerWithLayout(ctx, params, settings, layout); err == nil {
		t.Fatal("duplicate draft identities accepted")
	}
	settings, layout = draft()
	layout.Outputs[0].Rows = []int64{8}
	if _, err = svc.CreateControllerWithLayout(ctx, params, settings, layout); err == nil {
		t.Fatal("invalid graphical pixel coverage accepted")
	}
	if count("controllers") != 1 || count("controller_outputs") != 2 {
		t.Fatal("invalid draft changed existing data")
	}
}
