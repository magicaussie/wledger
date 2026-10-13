package hardware

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func TestConfigurationMutationsRejectDurableLocateOwnershipAcrossConnections(t *testing.T) {
	for _, state := range []string{"active", "partial"} {
		for _, actionName := range []string{"controller create", "controller delete", "legacy grid save", "legacy config import", "coordinate conversion", "legacy index migration", "allocation backfill"} {
			t.Run(state+"/"+actionName, func(t *testing.T) {
				ctx := context.Background()
				path := filepath.Join(t.TempDir(), "coordination.sqlite")
				conn1, err := db.Open("file:" + path)
				if err != nil {
					t.Fatal(err)
				}
				defer conn1.Close()
				if err := db.Migrate(conn1); err != nil {
					t.Fatal(err)
				}
				store1 := db.NewStore(conn1)
				controller, err := store1.CreateController(ctx, db.CreateControllerParams{Name: "Existing", IpAddress: "192.0.2.40"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store1.CreateContainer(ctx, db.CreateContainerParams{Name: "Existing main", ControllerID: controller.ID, SegmentID: 0, LedCount: 20}); err != nil {
					t.Fatal(err)
				}
				if _, err := conn1.ExecContext(ctx, `INSERT INTO physical_locate_sessions
					(controller_id,controller_address,controller_port,session_id,drawer_id,bin_id,owner_id,state,ranges_json,commands_json,fingerprint,color,brightness,last_operation,last_operation_at,last_successful_operation,last_successful_at,last_error)
					VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, 'activation',CURRENT_TIMESTAMP,'activation',CURRENT_TIMESTAMP,'cleanup unresolved')`,
					controller.ID, "192.0.2.40", 80, "coordination-"+state, 1, 1, 7, state,
					`[{"start":1,"count":5}]`, `[{"segment_id":0,"start":0,"stop":5}]`, "fingerprint", "#0000ff", 128); err != nil {
					t.Fatal(err)
				}
				if _, err := conn1.ExecContext(ctx, `INSERT INTO physical_locate_session_targets
					(session_id,controller_id,controller_address,controller_port,ranges_json,commands_json,fingerprint,may_have_received,last_error)
					VALUES (?,?,?,?,?,?,?,1,'cleanup unresolved')`, "coordination-"+state, controller.ID, "192.0.2.40", 80,
					`[{"start":1,"count":5}]`, `[{"segment_id":0,"start":0,"stop":5}]`, "fingerprint"); err != nil {
					t.Fatal(err)
				}

				conn2, err := db.Open("file:" + path)
				if err != nil {
					t.Fatal(err)
				}
				defer conn2.Close()
				store2 := db.NewStore(conn2)
				service := NewService(store2, wled.NewClient(), slog.New(slog.NewTextHandler(io.Discard, nil)))

				var mutationErr error
				switch actionName {
				case "controller create":
					_, mutationErr = service.CreateController(ctx, db.CreateControllerParams{Name: "New", IpAddress: "192.0.2.41", Port: sql.NullInt64{Int64: 80, Valid: true}})
				case "controller delete":
					mutationErr = service.DeleteController(ctx, controller.ID)
				case "legacy grid save":
					_, mutationErr = service.SaveGridInSpace(ctx, controller.ID,
						`[{"container_index":0,"x":1,"y":1,"led_index":2,"width":1,"name":"new bin"}]`,
						`[{"id":null,"name":"replacement","segment_id":0,"led_start":0,"led_count":20,"config":{"type":"grid","rows":4,"cols":5}}]`, "")
				case "legacy config import":
					_, mutationErr = service.ImportConfig(ctx, "", "", 0, []byte(`{"version":"1.0","controller":{"name":"Imported","ip_address":"192.0.2.42","port":80},"containers":[{"name":"A","segment_id":0,"led_start":0,"led_count":20,"config":{"type":"linear","total":20}}],"bins":[{"container_index":0,"led_index":2,"width":1,"name":"imported bin"}]}`))
				case "coordinate conversion":
					_, mutationErr = ConvertToDrawerRelative(ctx, store2, nil)
				case "legacy index migration":
					mutationErr = MigrateLegacyLedIndices(ctx, store2, slog.New(slog.NewTextHandler(io.Discard, nil)))
				case "allocation backfill":
					mutationErr = BackfillDrawerAllocations(ctx, store2, slog.New(slog.NewTextHandler(io.Discard, nil)))
				}
				if !errors.Is(mutationErr, db.ErrPhysicalLocateActive) {
					t.Fatalf("%s during durable %s Locate returned %v, want ErrPhysicalLocateActive", actionName, state, mutationErr)
				}

				var controllers, containers, bins int
				if err := conn1.QueryRowContext(ctx, "SELECT count(*) FROM controllers").Scan(&controllers); err != nil {
					t.Fatal(err)
				}
				if err := conn1.QueryRowContext(ctx, "SELECT count(*) FROM containers").Scan(&containers); err != nil {
					t.Fatal(err)
				}
				if err := conn1.QueryRowContext(ctx, "SELECT count(*) FROM bins").Scan(&bins); err != nil {
					t.Fatal(err)
				}
				if controllers != 1 || containers != 1 || bins != 0 {
					t.Fatalf("rejected %s mutated configuration: controllers=%d containers=%d bins=%d", actionName, controllers, containers, bins)
				}
				var storedState string
				var mayHaveReceived int64
				if err := conn1.QueryRowContext(ctx, `SELECT s.state,t.may_have_received FROM physical_locate_sessions s JOIN physical_locate_session_targets t USING(session_id)`).Scan(&storedState, &mayHaveReceived); err != nil {
					t.Fatal(err)
				}
				if storedState != state || mayHaveReceived != 1 {
					t.Fatalf("rejected mutation changed durable ownership: state=%q may_have_received=%d", storedState, mayHaveReceived)
				}
			})
		}
	}
}
