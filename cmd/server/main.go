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
	"github.com/tuxedocurly/wledger/internal/backup"
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
	backup   backup.Service
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

	// Backup Service
	backupService := backup.NewService(database, queries, "./app/uploads", log)

	// app struct
	app := &application{
		logger:   log,
		queries:  queries,
		session:  sessionManager,
		wled:     wledClient,
		database: database,
		backup:   backupService,
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
	r.Use(mw.Authenticate)
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

	// -------------------------------------------------------------------------
	// PUBLIC ROUTES
	// -------------------------------------------------------------------------
	r.Get("/setup", app.handleSetup)
	r.Post("/setup", app.handleSetupPost)
	r.Get("/login", app.handleLogin)
	r.Post("/login", app.handleLoginPost)
	r.Post("/logout", app.handleLogout)

	// -------------------------------------------------------------------------
	// READ-ONLY GROUP ROUTES (Protected by RequireReadAuth + RequirePasswordChange)
	// -------------------------------------------------------------------------
	// These routes respect the "Require Login for Read" setting.
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireReadAuth)
		r.Use(mw.RequirePasswordChange)

		// Dashboard
		r.Get("/", app.handleDashboard)

		// Parts (Read)
		r.Get("/parts", app.handlePartsList)
		r.Get("/parts/{id}", app.handlePartDetail)
		r.Get("/parts/bins_options", app.handleBinOptions)
		// Locate (Viewers, if permission is enabled, can light up parts)
		r.Post("/hardware/{id}/locate", app.handleHardwareLocate)

		// Hardware (Read)
		r.Get("/hardware", app.handleHardwareList)
		r.Get("/hardware/{id}/status", app.handleHardwareStatus)
		r.Get("/hardware/{id}/grid", app.handleHardwareGrid)
	})

	// -------------------------------------------------------------------------
	// WRITE / PROTECTED GROUP ROUTES
	// -------------------------------------------------------------------------
	// These routes ALWAYS require a logged-in user
	r.Group(func(r chi.Router) {
		// Base Gates
		r.Use(mw.RequireAuth)           // Must be logged in
		r.Use(mw.RequirePasswordChange) // Must not have pending reset

		// -----------------------------------------------------------
		// OPEN ROUTES (Any logged-in user)
		// -----------------------------------------------------------
		// Force Reset
		r.Get("/force-reset", app.handleForceReset)
		r.Post("/force-reset", app.handleForceResetPost)

		// Settings View (Needed so users can access "Change Password")
		r.Get("/settings", app.handleSettings)

		// Self-Service Password Change
		r.Post("/settings/password", app.handleSettingsPassword)

		// -----------------------------------------------------------
		// INVENTORY MANAGEMENT ROUTES (Editors & Admins)
		// -----------------------------------------------------------
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("editor", "admin"))

			// Parts CRUD
			r.Get("/parts/new", app.handlePartsNew)
			r.Post("/parts", app.handlePartsCreate)
			r.Get("/parts/import/template", app.handlePartsImportTemplate) // Download Bulk Import Template
			r.Post("/parts/import", app.handlePartsImport)                 // Bulk Import

			r.Get("/parts/{id}/edit", app.handlePartEdit)
			// UPDATED: Changed from /edit to /update to match the form action
			r.Post("/parts/{id}/update", app.handlePartUpdate)

			r.Post("/parts/{id}/delete", app.handlePartDelete)

			// Sub-Resources (HTMX Deletion)
			r.Delete("/parts/links/{id}", app.handleLinkDelete)
			r.Delete("/parts/docs/{id}", app.handleDocDelete)

			// Stock Management
			r.Post("/parts/{id}/assign", app.handlePartAssign)
			r.Post("/parts/{id}/stock/{assignment_id}/move", app.handlePartStockMove)
			r.Post("/parts/{id}/stock/{assignment_id}/delete", app.handlePartStockRemove)
		})

		// -----------------------------------------------------------
		// SYSTEM ADMINISTRATION ROUTES (Admins Only)
		// -----------------------------------------------------------
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("admin"))

			// Hardware Configuration
			r.Post("/hardware", app.handleHardwareCreate)
			r.Post("/hardware/{id}/delete", app.handleHardwareDelete)
			r.Post("/hardware/{id}/grid", app.handleHardwareGridSave)
			r.Post("/hardware/off", app.handleGlobalOff)

			// System Settings Update
			r.Post("/settings", app.handleSettingsUpdate)
			r.Get("/settings/backup/download", app.handleBackupDownload)
			r.Post("/settings/backup/restore", app.handleBackupRestore)

			// User Management
			r.Post("/settings/users", app.handleUserCreate)
			r.Post("/settings/users/{id}/delete", app.handleUserDelete)
			r.Post("/settings/users/{id}/reset", app.handleUserForceReset)
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