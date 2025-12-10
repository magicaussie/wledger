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
	chimiddleware "github.com/go-chi/chi/v5/middleware" // Renamed to avoid collision
	"github.com/tuxedocurly/wledger/internal/auth"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/images"
	"github.com/tuxedocurly/wledger/internal/logger"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/internal/wled"
	"github.com/tuxedocurly/wledger/web/layouts"
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
	// Ensure the /app/data directory exists (will map in Docker later, local now)
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
	sessionManager.Cookie.Secure = false // TODO: Set to true in prod (HTTPS), potentially make configurable in Docker

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
	r.Use(mw.RequestLogger)           // custom logger
	r.Use(sessionManager.LoadAndSave) // load session for every request
	r.Use(mw.FirstRunCheck)           // redirect to /setup if needed, e.g. no users

	// Initialize Image Processor
	if err := images.Init(); err != nil {
		log.Error("Failed to init images", "error", err)
		os.Exit(1)
	}

	// Static Files
	workDir, _ := os.Getwd()
	filesDir := http.Dir(workDir + "/web/static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(filesDir)))

	// Maps /uploads/filename.jpg -> ./app/uploads/filename.jpg
	workDir, _ = os.Getwd()
	uploadsDir := http.Dir(workDir + "/app/uploads")
	r.Handle("/uploads/*", http.StripPrefix("/uploads/", http.FileServer(uploadsDir)))

	// Public Routes
	r.Get("/setup", app.handleSetup)
	r.Post("/setup", app.handleSetupPost)
	r.Get("/login", app.handleLogin)
	r.Post("/login", app.handleLoginPost)
	r.Post("/logout", app.handleLogout)

	// Protected Routes (Require Auth)
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireAuth)

		// Dashboard Routes
		r.Get("/", app.handleDashboard)

		// Hardware Routes
		r.Get("/hardware", app.handleHardwareList)
		r.Post("/hardware", app.handleHardwareCreate)
		r.Post("/hardware/{id}/delete", app.handleHardwareDelete)
		r.Get("/hardware/{id}/status", app.handleHardwareStatus)
		r.Get("/hardware/{id}/grid", app.handleHardwareGrid)      // grid painter
		r.Post("/hardware/{id}/grid", app.handleHardwareGridSave) // grid painter

		// Parts Routes
		r.Get("/parts", app.handlePartsList)
		r.Get("/parts/new", app.handlePartsNew)
		r.Post("/parts", app.handlePartsCreate)
		r.Get("/parts/{id}", app.handlePartDetail)
		r.Post("/parts/{id}/assign", app.handlePartAssign)
		r.Get("/parts/bins_options", app.handleBinOptions)

		// Placeholders for future implementation
		r.Get("/settings", func(w http.ResponseWriter, r *http.Request) {
			layouts.Base("Settings").Render(r.Context(), w)
		})
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
