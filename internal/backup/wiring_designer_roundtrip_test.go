package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wiringdesigner"
)

func TestGraphicalWiringMetadataSystemFlagSurvivesBackupRestore(t *testing.T) {
	database, store, cleanup := setupTestDB(t)
	defer cleanup()
	uploads, cleanupUploads := setupTestUploads(t)
	defer cleanupUploads()
	ctx := context.Background()
	if err := store.InitSettings(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(ctx, db.CreateUserParams{Email: "designer-backup@example.test", PasswordHash: "hash", Role: "admin"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, store, uploads, slog.New(slog.NewTextHandler(io.Discard, nil)))
	key := "led_wiring_designer_v1:42"
	visualMetadata := `{"version":1,"revision":7,"width":800,"height":600,"background":{"data_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC","x":10,"y":20,"width":500,"height":300,"opacity":0.4},"outputs":[{"output_id":9,"x":10,"y":10,"width":400,"height":200,"axis":"rows","rows":[3,5],"sections":[4,4],"rotation":90,"section_layouts":[{"id":"section-a","start":0,"count":4,"x":10,"y":20,"rotation":90},{"id":"section-b","start":4,"count":4,"x":0,"y":0,"rotation":0}],"pixel_positions":[{"index":0,"x":12.5,"y":20.5},{"index":1,"x":13.5,"y":21.5}]}],"jumpers":[{"id":"jumper-a","points":[{"x":1,"y":2},{"x":3,"y":4}]}]}`
	var layout wiringdesigner.Layout
	if err := json.Unmarshal([]byte(visualMetadata), &layout); err != nil {
		t.Fatal(err)
	}
	if err := wiringdesigner.Validate(layout, map[int64]int64{9: 8}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFlag(ctx, db.SetFlagParams{Key: key, Value: visualMetadata}); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := service.Export(ctx, &archive); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := store.SetFlag(ctx, db.SetFlagParams{Key: key, Value: `{"revision":99}`}); err != nil {
		t.Fatal(err)
	}
	if err := service.Restore(ctx, bytes.NewReader(archive.Bytes()), int64(archive.Len())); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, err := store.GetFlag(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(got), &layout); err != nil {
		t.Fatal(err)
	}
	if err := wiringdesigner.Validate(layout, map[int64]int64{9: 8}); err != nil {
		t.Fatalf("restored layout unusable: %v", err)
	}
	if got != visualMetadata {
		t.Fatalf("designer metadata changed across backup/restore: got %s", got)
	}
}
