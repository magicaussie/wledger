package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/logger"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// application holds shared dependencies
type application struct {
	logger   *slog.Logger
	queries  *db.Queries
	session  *scs.SessionManager
	wled     *wled.Client
	database *sql.DB
}

func main() {
	// Logger init
	log := logger.New(true)
	log.Info("Starting WLEDger V2...")

	// Database
	os.MkdirAll("./data", 0755)
	database, err := db.Open("./data/wledger.db")
	if err != nil {
		log.Error("Failed to open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Apply Migrations
	if err := db.Migrate(database); err != nil {
		log.Error("Failed to migrate database", "error", err)
		os.Exit(1)
	}

	// Initialize queries
	queries := db.New(database)

	// Session Manager
	sessionManager := scs.New()
	sessionManager.Store = auth.NewStore(queries)
	sessionManager.Lifetime = 24 * time.Hour
	sessionManager.Cookie.Persist = true
	sessionManager.Cookie.SameSite = http.SameSiteLaxMode
	sessionManager.Cookie.Secure = false // TODO: Set to true in prod

	// WLED client
	wledClient := wled.New()

	// app struct
	app := &application{
		logger:   log,
		queries:  queries,
		session:  sessionManager,
		wled:     wledClient,
		database: database,
	}

	// Middleware Manager
	mw := middleware.New(queries, sessionManager, log)

	// Router Setup
	r := chi.NewRouter()

	// Global Middlewares
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(mw.RequestLogger)
	r.Use(sessionManager.LoadAndSave)
	r.Use(mw.FirstRunCheck)

	// Initialize Image Processor
	if err := images.Init(); err != nil {
		log.Error("Failed to init images", "error", err)
		os.Exit(1)
	}

	// Static Files
	workDir, _ := os.Getwd()
	filesDir := http.Dir(workDir + "/web/static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(filesDir)))

	uploadsDir := http.Dir(workDir + "/app/uploads")
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(uploadsDir)))

	// Auth Routes (Always Public)
	r.Get("/setup", app.handleSetup)
	r.Post("/setup", app.handleSetupPost)
	r.Get("/login", app.handleLogin)
	r.Post("/login", app.handleLoginPost)
	r.Post("/logout", app.handleLogout)

	// READ-ONLY GROUP
	// These routes check the "Require Login for Read" setting.
	// If setting is OFF, guests can view them. If ON, guests are redirected.
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireReadAuth)

		// Dashboard
		r.Get("/", app.handleDashboard)

		// Parts (View Only)
		r.Get("/parts", app.handlePartsList)
		r.Get("/parts/{id}", app.handlePartDetail)
		r.Get("/parts/bins_options", app.handleBinOptions)

		// Hardware Write (for locate)
		r.Post("/hardware/{id}/locate", app.handleHardwareLocate)

	})

	// WRITE / PROTECTED GROUP
	// These routes ALWAYS require a logged-in user
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireAuth)

		// Parts Write
		r.Get("/parts/new", app.handlePartsNew)
		r.Post("/parts", app.handlePartsCreate)
		// Edit
		r.Get("/parts/{id}/edit", app.handlePartEdit)
		r.Post("/parts/{id}/edit", app.handlePartUpdate)
		// Actions
		r.Post("/parts/{id}/delete", app.handlePartDelete)
		r.Post("/parts/{id}/assign", app.handlePartAssign)
		r.Post("/parts/{id}/stock/{bin_id}/delete", app.handlePartStockRemove)

		// Hardware Write
		r.Post("/hardware", app.handleHardwareCreate)
		r.Post("/hardware/{id}/delete", app.handleHardwareDelete)
		r.Post("/hardware/{id}/grid", app.handleHardwareGridSave)
		r.Post("/hardware/off", app.handleGlobalOff)

		// Hardware Read
		r.Get("/hardware", app.handleHardwareList)
		r.Get("/hardware/{id}/status", app.handleHardwareStatus)
		r.Get("/hardware/{id}/grid", app.handleHardwareGrid)

		// Settings Read/Write
		r.Get("/settings", app.handleSettings)
		r.Post("/settings", app.handleSettingsUpdate)
	})

	// Start Server
	port := "8080"
	log.Info("Server listening", "url", "http://localhost:"+port)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful Shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	log.Info("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
