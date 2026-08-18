package wled

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tuxedocurly/wledger/internal/db"
)

func TestService_Locate(t *testing.T) {
	// Setup a mock WLED server
	receivedColor := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/state" {
			body, _ := io.ReadAll(r.Body)
			receivedColor = string(body)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	// Extract IP/Port from server URL (e.g. 127.0.0.1:12345)
	ip := server.URL[7:]

	// Setup DB
	dbConn, err := db.Open("file:wled_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer dbConn.Close()

	if err := db.Migrate(dbConn); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	store := db.NewStore(dbConn)
	ctx := context.Background()

	// Seed Settings
	if err := store.InitSettings(ctx); err != nil {
		t.Fatalf("failed to init settings: %v", err)
	}
	err = store.UpdateColors(ctx, db.UpdateColorsParams{
		ColorLocate: sql.NullString{String: "#FF0000", Valid: true},
	})
	if err != nil {
		t.Fatalf("failed to update colors: %v", err)
	}

	client := NewClient()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(store, client, logger)

	t.Run("LocateBin", func(t *testing.T) {
		c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test", IpAddress: ip})
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}

		cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
			Name:         "Cont",
			ControllerID: c.ID,
			SegmentID:    0,
		})
		if err != nil {
			t.Fatalf("failed to create container: %v", err)
		}

		b, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "B1", ContainerID: cont, LedIndex: sql.NullInt64{Int64: 0, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin: %v", err)
		}

		err = svc.LocateBin(ctx, c.ID, b)
		if err != nil {
			t.Fatalf("LocateBin failed: %v", err)
		}

		// Verify that client sent the correct color
		if receivedColor == "" {
			t.Error("No color received by mock server")
		}
	})

	t.Run("LocateBinMultiLedWidth", func(t *testing.T) {
		receivedColor = ""
		c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test2", IpAddress: ip})
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}

		cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
			Name:         "Cont2",
			ControllerID: c.ID,
			SegmentID:    0,
		})
		if err != nil {
			t.Fatalf("failed to create container: %v", err)
		}

		// Bin at 0-based LED index 685 spanning 15 LEDs (physical LEDs 686-700).
		b, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "B2", ContainerID: cont,
			LedIndex: sql.NullInt64{Int64: 685, Valid: true},
			Width:    sql.NullInt64{Int64: 15, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin: %v", err)
		}

		if err := svc.LocateBin(ctx, c.ID, b); err != nil {
			t.Fatalf("LocateBin failed: %v", err)
		}

		if !strings.Contains(receivedColor, `"i":[685,700`) {
			t.Errorf("expected individual LED range 685->700, got %s", receivedColor)
		}
	})

	t.Run("LocateBinWidthDefaultsToOne", func(t *testing.T) {
		receivedColor = ""
		c, err := store.CreateController(ctx, db.CreateControllerParams{Name: "Test3", IpAddress: ip})
		if err != nil {
			t.Fatalf("failed to create controller: %v", err)
		}

		cont, err := store.CreateContainer(ctx, db.CreateContainerParams{
			Name:         "Cont3",
			ControllerID: c.ID,
			SegmentID:    0,
		})
		if err != nil {
			t.Fatalf("failed to create container: %v", err)
		}

		// Width 0 / missing should behave as width 1: range end == index+1.
		b, err := store.CreateBin(ctx, db.CreateBinParams{
			Name: "B3", ContainerID: cont,
			LedIndex: sql.NullInt64{Int64: 685, Valid: true},
		})
		if err != nil {
			t.Fatalf("failed to create bin: %v", err)
		}

		if err := svc.LocateBin(ctx, c.ID, b); err != nil {
			t.Fatalf("LocateBin failed: %v", err)
		}

		if !strings.Contains(receivedColor, `"i":[685,686`) {
			t.Errorf("expected single LED range 685->686, got %s", receivedColor)
		}
	})
}
