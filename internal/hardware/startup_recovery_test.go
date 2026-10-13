package hardware

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestCompletedStartupMigrationsAreNoOpsWithDurableLocateOwnership(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open("file:" + filepath.Join(t.TempDir(), "startup-recovery.sqlite"))
	if err != nil {
		t.Fatalf("open disposable database: %v", err)
	}
	defer conn.Close()
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("migrate disposable database: %v", err)
	}
	store := db.NewStore(conn)
	controller, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Mock controller", IpAddress: "127.0.0.1"})
	if err != nil {
		t.Fatalf("create controller: %v", err)
	}
	containerID, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name: "Already allocated string", ControllerID: controller.ID, SegmentID: 0,
		LedStart: 0, LedCount: 100,
	})
	if err != nil {
		t.Fatalf("create allocated string: %v", err)
	}
	for _, flag := range []db.SetFlagParams{
		{Key: Migration005FlagKey, Value: "true"},
		{Key: "drawer_allocation_backfilled", Value: "true"},
	} {
		if err := store.SetFlag(ctx, flag); err != nil {
			t.Fatalf("set completed migration flag %q: %v", flag.Key, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO physical_locate_sessions
		(controller_id,controller_address,controller_port,session_id,drawer_id,bin_id,owner_id,state,ranges_json,commands_json,fingerprint,color,brightness,last_operation,last_operation_at,last_successful_operation,last_successful_at,last_error)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, 'activation',CURRENT_TIMESTAMP,'activation',CURRENT_TIMESTAMP,'recovery pending')`,
		controller.ID, "127.0.0.1", 80, "startup-recovery", 1, 1, 7, "activating",
		`[{"start":1,"count":5}]`, `[{"segment_id":10,"start":0,"stop":5}]`, "fingerprint", "#0000ff", 128); err != nil {
		t.Fatalf("insert durable active Locate session: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO physical_locate_session_targets
		(session_id,controller_id,controller_address,controller_port,ranges_json,commands_json,fingerprint,may_have_received,last_error)
		VALUES (?,?,?,?,?,?,?,1,'response uncertain')`, "startup-recovery", controller.ID, "127.0.0.1", 80,
		`[{"start":1,"count":5}]`, `[{"segment_id":10,"start":0,"stop":5}]`, "fingerprint"); err != nil {
		t.Fatalf("insert durable uncertain target: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := MigrateLegacyLedIndices(ctx, store, logger); err != nil {
		t.Fatalf("completed legacy migration should be a no-op during Locate recovery: %v", err)
	}
	if err := BackfillDrawerAllocations(ctx, store, logger); err != nil {
		t.Fatalf("completed allocation backfill should be a no-op during Locate recovery: %v", err)
	}

	container, err := store.GetContainer(ctx, containerID)
	if err != nil {
		t.Fatalf("read container after no-op startup migrations: %v", err)
	}
	if container.LedStart != 0 || container.LedCount != 100 {
		t.Fatalf("startup no-op changed allocated string geometry: start=%d count=%d", container.LedStart, container.LedCount)
	}
	for _, flagKey := range []string{Migration005FlagKey, "drawer_allocation_backfilled"} {
		flag, err := store.GetFlag(ctx, flagKey)
		if err != nil || flag != "true" {
			t.Errorf("completed migration flag %q changed: value=%q err=%v", flagKey, flag, err)
		}
	}
	var state string
	if err := conn.QueryRowContext(ctx, `SELECT state FROM physical_locate_sessions WHERE session_id='startup-recovery'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "activating" {
		t.Fatalf("startup migration changed durable Locate state: %q", state)
	}
	var unresolvedTargets int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM physical_locate_session_targets WHERE session_id='startup-recovery' AND may_have_received=1`).Scan(&unresolvedTargets); err != nil {
		t.Fatal(err)
	}
	if unresolvedTargets != 1 {
		t.Fatalf("startup migration changed the unresolved target marker: count=%d", unresolvedTargets)
	}
}
