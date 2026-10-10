// Command wall-browser is an optional, disposable staging harness for the
// WLEDger dashboard Wall browser regression test (see wall_browser.test.js).
//
// It is NOT part of the shipped application and is never run in production. It
// creates a throwaway SQLite database in a temp directory, seeds a synthetic
// Wall (one demo controller on an RFC5737 TEST-NET address, two demo containers
// - one populated, one empty - and representative bins) and serves the real
// WLEDger router on 127.0.0.1 only.
//
// Safety properties:
//   - Binds to 127.0.0.1 only; never a LAN interface.
//   - Uses a temp database, temp uploads and temp logs; never ./data or ./app.
//   - The demo controller uses 192.0.2.10 (RFC5737 TEST-NET-1); no real
//     hardware address is used and no WLED/LED call is made.
//
// Usage (from the repository root):
//
//	go run -tags fts5 ./scripts/wall-browser -port 18080
//
// then, in another shell:
//
//	DEMO_URL=http://127.0.0.1:18080 node scripts/wall-browser/wall_browser.test.js
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/audit"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/backup"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/dashboard"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/documents"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/hardware"
	"github.com/tuxedocurly/wledger/internal/i18n"
	"github.com/tuxedocurly/wledger/internal/inspiration"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/router"
	settingsServicePkg "github.com/tuxedocurly/wledger/internal/settings"
	"github.com/tuxedocurly/wledger/internal/stock"
	"github.com/tuxedocurly/wledger/internal/suppliers"
	_ "github.com/tuxedocurly/wledger/internal/suppliers/providers"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

func main() {
	repo := flag.String("repo", ".", "path to the repository root")
	port := flag.String("port", "18080", "loopback port to listen on")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	repoAbs, err := filepath.Abs(*repo)
	if err != nil {
		fatal(log, "resolve repo root", err)
	}

	// Disposable staging directory: the server resolves its config paths
	// (./data, ./web/static, ./locales, ./app/...) relative to the working
	// directory, so we run from here with symlinks back to the real assets.
	stage, err := os.MkdirTemp("", "wledger-wall-browser-")
	if err != nil {
		fatal(log, "create staging dir", err)
	}
	defer os.RemoveAll(stage)

	if err := os.MkdirAll(filepath.Join(stage, "web"), 0755); err != nil {
		fatal(log, "create staging web dir", err)
	}
	if err := os.Symlink(filepath.Join(repoAbs, "web", "static"), filepath.Join(stage, "web", "static")); err != nil {
		fatal(log, "link static assets", err)
	}
	if err := os.Symlink(filepath.Join(repoAbs, "locales"), filepath.Join(stage, "locales")); err != nil {
		fatal(log, "link locales", err)
	}
	if err := os.Chdir(stage); err != nil {
		fatal(log, "enter staging dir", err)
	}

	if err := i18n.Init(); err != nil {
		fatal(log, "i18n init", err)
	}
	if err := os.MkdirAll(config.DirData, 0755); err != nil {
		fatal(log, "create data dir", err)
	}
	if err := os.MkdirAll(config.DirUploads, 0755); err != nil {
		fatal(log, "create uploads dir", err)
	}
	if err := os.MkdirAll(config.DirLogs, 0755); err != nil {
		fatal(log, "create logs dir", err)
	}

	database, err := db.Open(config.DirDatabase)
	if err != nil {
		fatal(log, "open db", err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		fatal(log, "migrate", err)
	}

	store := db.NewStore(database)
	ctx := context.Background()
	if err := hardware.MigrateLegacyLedIndices(ctx, store, log); err != nil {
		fatal(log, "legacy led migration", err)
	}
	if err := hardware.BackfillDrawerAllocations(ctx, store, log); err != nil {
		fatal(log, "backfill allocations", err)
	}
	if err := store.InitSettings(ctx); err != nil {
		fatal(log, "init settings", err)
	}
	if err := seed(ctx, store); err != nil {
		fatal(log, "seed", err)
	}

	sessionManager := scs.New()
	sessionManager.Store = auth.NewStore(store)
	sessionManager.Lifetime = 24 * time.Hour
	sessionManager.Cookie.Persist = true
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	sessionManager.Cookie.Secure = false // loopback HTTP staging only

	wledClient := wled.NewClient()
	wledService := wled.NewService(store, wledClient, log)
	uiErrorResponder := uierror.New(log)
	backupService := backup.NewService(database, store, config.DirUploads, log)
	tagsService := tags.NewService(database, store)
	docsService := documents.NewService(store, log)
	stockService := stock.NewService(store, log)
	partsService := parts.NewService(database, store, log, tagsService, docsService)
	hardwareService := hardware.NewService(store, wledClient, log)
	settingsService := settingsServicePkg.NewService(store)
	auditService := audit.NewService(store)
	dashboardService := dashboard.NewService(store)
	inspirationService := inspiration.NewService(store)
	_ = inspirationService.SeedTemplates(ctx)
	supplierCache := suppliers.NewCache(store, log)
	suppliersService := suppliers.NewService(store, supplierCache, log)

	h := handler.New(
		log, store, sessionManager, wledService, database, backupService,
		partsService, tagsService, inspirationService, uiErrorResponder,
		hardwareService, settingsService, auditService, dashboardService,
		stockService, docsService, suppliersService,
	)
	mw := middleware.New(store, sessionManager, log, uiErrorResponder)
	r := router.New(mw, sessionManager, h)

	addr := "127.0.0.1:" + *port
	srv := &http.Server{Addr: addr, Handler: r}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal(log, "serve", err)
		}
	}()

	fmt.Printf("WALL_BROWSER_READY http://%s\n", addr)
	os.Stdout.Sync()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func fatal(log *slog.Logger, msg string, err error) {
	log.Error(msg, "err", err)
	os.Exit(1)
}

// seed writes the synthetic Wall fixture. All addresses are RFC5737 TEST-NET.
func seed(ctx context.Context, store db.Store) error {
	if walls, err := store.GetWalls(ctx); err == nil && len(walls) > 0 {
		return nil
	}

	ctrl, err := store.CreateController(ctx, db.CreateControllerParams{
		Name:      "Demo Controller",
		IpAddress: "192.0.2.10",
		Port:      sql.NullInt64{Int64: 80, Valid: true},
	})
	if err != nil {
		return err
	}

	contA, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:          "Demo Drawer A - A Very Long Container Name For Overflow Testing",
		ControllerID:  ctrl.ID,
		SegmentID:     0,
		ConfigJson:    sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true},
		PositionIndex: 0,
		LedCount:      64,
	})
	if err != nil {
		return err
	}
	contB, err := store.CreateContainer(ctx, db.CreateContainerParams{
		Name:          "Demo Drawer B (empty)",
		ControllerID:  ctrl.ID,
		SegmentID:     1,
		ConfigJson:    sql.NullString{String: `{"type":"grid","rows":8,"cols":8}`, Valid: true},
		PositionIndex: 1,
		LedCount:      64,
	})
	if err != nil {
		return err
	}

	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			name := fmt.Sprintf("R%dC%d", y+1, x+1)
			if y == 7 && x == 7 {
				name = "A very long bin name that should truncate"
			}
			if _, err := store.CreateBin(ctx, db.CreateBinParams{
				Name:        name,
				ContainerID: contA,
				Width:       sql.NullInt64{Int64: 1, Valid: true},
				GridX:       sql.NullInt64{Int64: int64(x), Valid: true},
				GridY:       sql.NullInt64{Int64: int64(y), Valid: true},
			}); err != nil {
				return err
			}
		}
	}

	// One part assigned to a bin so the stock-status colour path is exercised.
	partID, err := store.CreatePart(ctx, db.CreatePartParams{
		Name:              "Demo Resistor 10k",
		MinStockThreshold: sql.NullInt64{Int64: 5, Valid: true},
		ReorderLevel:      sql.NullInt64{Int64: 10, Valid: true},
	})
	if err != nil {
		return err
	}
	bins, err := store.GetBinsByContainer(ctx, contA)
	if err != nil {
		return err
	}
	if len(bins) == 0 {
		return fmt.Errorf("expected seeded bins")
	}
	if err := store.CreatePartAssignment(ctx, db.CreatePartAssignmentParams{
		PartID:   partID,
		BinID:    sql.NullInt64{Int64: bins[0].ID, Valid: true},
		Quantity: 3, // <= reorder level -> "low" status
	}); err != nil {
		return err
	}

	wallID, err := store.CreateWall(ctx, db.CreateWallParams{
		Name:        "Demo Wall",
		Description: sql.NullString{String: "Synthetic demo wall (staging only)", Valid: true},
	})
	if err != nil {
		return err
	}
	for i, cID := range []int64{contA, contB} {
		if err := store.AddContainerToWall(ctx, db.AddContainerToWallParams{
			WallID:        wallID,
			ContainerID:   cID,
			PositionIndex: int64(i),
		}); err != nil {
			return err
		}
	}

	hash, err := auth.HashPassword("demo-password-123")
	if err != nil {
		return err
	}
	if _, err := store.CreateUser(ctx, db.CreateUserParams{
		Email:        "demo@example.test",
		PasswordHash: hash,
		Role:         "admin",
	}); err != nil {
		return err
	}
	return nil
}
