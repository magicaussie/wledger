package main

import (
	"context"
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
	"github.com/tuxedocurly/wledger/internal/logger"
	"github.com/tuxedocurly/wledger/internal/middleware"
	"github.com/tuxedocurly/wledger/web/layouts"
)

// application holds shared dependencies
type application struct {
	logger  *slog.Logger
	queries *db.Queries
	session *scs.SessionManager
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

	// app struct
	app := &application{
		logger:  log,
		queries: queries,
		session: sessionManager,
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

	// Static Files
	workDir, _ := os.Getwd()
	filesDir := http.Dir(workDir + "/web/static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(filesDir)))

	// Public Routes
	r.Get("/setup", app.handleSetup)
	r.Post("/setup", app.handleSetupPost)
	r.Get("/login", app.handleLogin)
	r.Post("/login", app.handleLoginPost)
	r.Post("/logout", app.handleLogout)

	// Protected Routes (Require Auth)
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireAuth)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			layouts.Base("Dashboard").Render(r.Context(), w)
		})

		// Placeholders for future implementation
		r.Get("/parts", func(w http.ResponseWriter, r *http.Request) {
			layouts.Base("Inventory").Render(r.Context(), w)
		})
		r.Get("/hardware", func(w http.ResponseWriter, r *http.Request) {
			layouts.Base("Hardware").Render(r.Context(), w)
		})
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
