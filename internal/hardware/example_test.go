package hardware

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// TestExamplesImport verifies every examples/hardware_config_*.json imports
// cleanly through ImportConfig and survives an export round-trip.
func TestExamplesImport(t *testing.T) {
	matches, err := filepath.Glob("../../examples/hardware_config_*.json")
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no example files found")
	}

	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read failed: %v", err)
			}

			dbConn, err := db.Open("file:example_test?mode=memory&cache=shared")
			if err != nil {
				t.Fatalf("failed to open db: %v", err)
			}
			defer dbConn.Close()
			if err := db.Migrate(dbConn); err != nil {
				t.Fatalf("failed to migrate test db: %v", err)
			}

			store := db.NewStore(dbConn)
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			s := NewService(store, wled.NewClient(), logger)
			ctx := context.Background()

			// Import using the name/IP/port embedded in the file.
			id, err := s.ImportConfig(ctx, "", "", 0, data)
			if err != nil {
				t.Fatalf("ImportConfig failed: %v", err)
			}

			// Must have at least one container and one bin.
			containers, err := s.GetContainers(ctx, id)
			if err != nil {
				t.Fatalf("GetContainers failed: %v", err)
			}
			if len(containers) == 0 {
				t.Fatal("expected at least 1 container")
			}

			bins, err := s.GetBinsByController(ctx, id)
			if err != nil {
				t.Fatalf("GetBinsByController failed: %v", err)
			}
			if len(bins) == 0 {
				t.Fatal("expected at least 1 bin")
			}

			// Every bin's width must be clamped >= 1.
			for _, b := range bins {
				if b.Width.Int64 < 1 {
					t.Errorf("bin %q has invalid width %d", b.Name, b.Width.Int64)
				}
			}

			// Export round-trip must be non-empty.
			exported, err := s.ExportConfig(ctx, id)
			if err != nil {
				t.Fatalf("ExportConfig failed: %v", err)
			}
			if len(exported) == 0 {
				t.Fatal("empty export")
			}
		})
	}
}
