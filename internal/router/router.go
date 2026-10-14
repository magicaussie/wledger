package router

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/tuxedocurly/wledger/internal/api"
	"github.com/tuxedocurly/wledger/internal/config"
	"github.com/tuxedocurly/wledger/internal/handler"
	"github.com/tuxedocurly/wledger/internal/maintenance"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

// New creates and configures a new chi router
func New(mw *middleware.Manager, sessionManager *scs.SessionManager, h *handler.Handler, gates ...*maintenance.Gate) *chi.Mux {
	r := chi.NewRouter()
	if len(gates) > 0 && gates[0] != nil {
		r.Use(gates[0].Middleware)
	}

	// Global Middlewares
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(chimiddleware.Recoverer)
	r.Use(mw.RequestLogger)
	r.Use(sessionManager.LoadAndSave)
	r.Use(mw.I18n)
	r.Use(mw.Authenticate)
	r.Use(mw.CSRFContext)
	r.Use(mw.RequireCSRF)
	r.Use(mw.FirstRunCheck)

	// Static Files
	filesDir := http.Dir(config.DirStatic)
	r.Handle(config.UrlPrefixStatic+"*", http.StripPrefix(config.UrlPrefixStatic, http.FileServer(filesDir)))

	uploadsDir := http.Dir(config.DirUploads)
	r.Handle(config.UrlPrefixUploads+"*", http.StripPrefix(config.UrlPrefixUploads, http.FileServer(uploadsDir)))

	// -------------------------------------------------------------------------
	// PUBLIC ROUTES
	// -------------------------------------------------------------------------
	r.Get("/setup", h.HandleSetup)
	r.Post("/setup", h.HandleSetupPost)
	r.Get("/login", h.HandleLogin)
	r.Post("/login", h.HandleLoginPost)
	r.Post("/logout", h.HandleLogout)

	// Public image proxy (needed for <img> tags on supplier pages)
	r.Get("/suppliers/image", h.HandleSupplierImageProxy)

	// -------------------------------------------------------------------------
	// MACHINE-TO-MACHINE API (token-authenticated; mounted only when
	// WLEDGER_API_TOKEN is set). Used by Home Assistant and the MCP server.
	// -------------------------------------------------------------------------
	if api.Enabled() {
		apiHandler := api.NewHandler(h.Queries, h.WLED, h.Parts)
		r.Mount("/api/v1", apiHandler.Routes())
	}

	// -------------------------------------------------------------------------
	// READ-ONLY GROUP ROUTES (Protected by RequireReadAuth + RequirePasswordChange)
	// -------------------------------------------------------------------------
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireReadAuth)
		r.Use(mw.RequirePasswordChange)

		// Dashboard
		r.Get("/", h.HandleDashboard)

		// Parts (Read)
		r.Get("/parts", h.HandlePartsList)
		r.Get("/parts/labels", h.HandleProductLabels)
		r.Get("/parts/low-stock", h.HandleLowStock)
		r.Get("/parts/low-stock/export", h.HandleLowStockExport)
		r.Get("/parts/{id}", h.HandlePartDetail)
		r.Get("/parts/{id}/qr", h.HandlePartQR)
		r.Get("/labels/{type}/{id}", h.HandleScanLabel)
		r.Get("/parts/bins_options", h.HandleBinOptions)
		r.Get("/parts/bin_picker", h.HandleBinPicker)

		// Scanning (barcode / QR)
		r.Get("/scan", h.HandleScan)
		r.Get("/scan/resolve", h.HandleScanResolve)
		r.Get("/scan/barcode/{type}/{id}", h.HandleEntityBarcode)
		r.Get("/bin/{id}/qr", h.HandleBinQR)
		r.Get("/cabinet/{id}/qr", h.HandleCabinetQR)
		r.Get("/drawer/{id}/qr", h.HandleDrawerQR)
		r.Get("/storage/bins/{id}/qr", h.HandleBinQR)
		r.Get("/storage/cabinets/{id}/qr", h.HandlePhysicalCabinetQR)
		r.Get("/storage/drawers/{id}/qr", h.HandlePhysicalDrawerQR)

		// Drawers (containers)
		r.Get("/drawers/{id}", h.HandleDrawerDetail)

		// Physical storage browsing (read-only)
		r.Get("/storage/cabinets", h.HandleStorageCabinets)
		r.Get("/storage/cabinets/{id}", h.HandleStorageCabinet)
		r.Get("/storage/drawers/{id}", h.HandleStorageDrawer)
		r.Get("/storage/bin_picker", h.HandleStorageBinPicker)

		// Hardware (Read)
		r.Get("/hardware", h.HandleHardwareList)
		r.Get("/hardware/labels", h.HandleBinLabels)
		r.Get("/hardware/{id}/status", h.HandleHardwareStatus)
		r.Get("/hardware/{id}/grid", h.HandleHardwareGrid)

		// Inspiration
		r.Get("/inspiration", h.HandleInspiration)
		r.Get("/inspiration/{id}/generate", h.HandleInspirationGenerate)

		// Suppliers (Read)
		r.Get("/suppliers", h.HandleSupplierSearch)
		r.Get("/suppliers/search", h.HandleSupplierSearchAPI)
		r.Get("/suppliers/{key}/{id}", h.HandleSupplierDetail)
	})

	// -------------------------------------------------------------------------
	// WRITE / PROTECTED GROUP ROUTES
	// -------------------------------------------------------------------------
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireAuth)
		r.Use(mw.RequirePasswordChange)

		// Force Reset
		r.Get("/force-reset", h.HandleForceReset)
		r.Post("/force-reset", h.HandleForceResetPost)

		// Settings View
		r.Get("/settings", h.HandleSettings)

		// Self-Service Password Change
		r.Post("/settings/password", h.HandleSettingsPassword)

		// -----------------------------------------------------------
		// HARDWARE ACTIONS (Editors & Admins)
		// These operate physical LEDs (locate / global off), so they are
		// write-capable actions and must not be reachable by read-only users
		// or guests. Kept separate from the read-only group on purpose.
		// -----------------------------------------------------------
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("editor", "admin"))

			r.Post("/hardware/{id}/locate", h.HandleHardwareLocate)
			r.Post("/parts/{id}/locate", h.HandlePartLocate)
			r.Post("/parts/{id}/locate-bin", h.HandlePhysicalPartLocate)
			r.Post("/storage/drawers/{id}/locate", h.HandlePhysicalDrawerLocate)
			r.Post("/storage/drawers/{id}/stop", h.HandlePhysicalDrawerStop)
			r.Post("/drawers/{id}/locate", h.HandleDrawerLocate)
			r.Post("/hardware/off", h.HandleGlobalOff)
		})

		// -----------------------------------------------------------
		// INVENTORY MANAGEMENT ROUTES (Editors & Admins)
		// -----------------------------------------------------------
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("editor", "admin"))

			// Physical layout editing (M7): editor/admin, CSRF-protected writes.
			r.Get("/storage/cabinets/{id}/edit", h.HandleLayoutEditor)
			r.Get("/storage/cabinets/{id}/controllers/{controllerID}/outputs", h.HandleCabinetControllerOutputDiscovery)
			r.Group(func(r chi.Router) {
				r.Use(mw.RequireRole("admin"))
				r.Post("/storage/cabinets/{id}/controller", h.HandleCabinetControllerSave)
				r.Post("/storage/cabinets/{id}/wiring", h.HandleCabinetWiringSave)
				r.Post("/hardware/{id}/locate-mapping", h.HandleControllerLocateMappingSave)
			})
			r.Group(func(r chi.Router) {
				r.Post("/storage/cabinets", h.HandleLayoutCabinetCreate)
				r.Post("/storage/cabinets/{id}/edit", h.HandleLayoutSave)
				r.Post("/storage/cabinets/{id}/drawers/{drawerID}/bins", h.HandlePhysicalBinCreate)
				r.Post("/storage/cabinets/{id}/bins/{binID}/move", h.HandlePhysicalBinMove)
				r.Post("/storage/cabinets/{id}/drawers/{drawerID}/led", h.HandleDrawerLEDAllocationsSave)
				r.Post("/storage/cabinets/{id}/archive", h.HandleLayoutCabinetArchive)
				r.Post("/storage/cabinets/{id}/delete", h.HandleLayoutCabinetDelete)
				r.Post("/storage/cabinets/{id}/drawers/{drawerID}/archive", h.HandleLayoutDrawerArchive)
				r.Post("/storage/cabinets/{id}/drawers/{drawerID}/delete", h.HandleLayoutDrawerDelete)
				r.Post("/storage/cabinets/{id}/bins/{binID}/detach", h.HandleLayoutBinDetach)
			})

			// Parts CRUD
			r.Get("/parts/new", h.HandlePartsNew)
			r.Post("/parts", h.HandlePartsCreate)
			r.Get("/parts/import/template", h.HandlePartsImportTemplate)
			r.Post("/parts/import", h.HandlePartsImport)
			r.Handle("/parts/bulk", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost || r.Method == http.MethodDelete {
					h.HandlePartsBulkDelete(w, r)
				} else {
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))

			r.Get("/parts/{id}/edit", h.HandlePartEdit)
			r.Post("/parts/{id}/update", h.HandlePartUpdate)
			r.Post("/parts/{id}/delete", h.HandlePartDelete)
			r.Post("/parts/{id}/favorite", h.HandlePartFavorite)

			// Sub-Resources
			r.Delete("/parts/links/{id}", h.HandleLinkDelete)
			r.Delete("/parts/docs/{id}", h.HandleDocDelete)

			// Stock Management
			r.Post("/parts/{id}/assign", h.HandlePartAssign)
			r.Post("/parts/{id}/stock/{assignment_id}/adjust", h.HandlePartStockAdjust)
			r.Post("/parts/{id}/stock/{assignment_id}/move", h.HandlePartStockMove)
			r.Post("/parts/{id}/stock/{assignment_id}/delete", h.HandlePartStockRemove)

			// Supplier Import
			r.Post("/suppliers/import", h.HandleSupplierImport)
			r.Post("/suppliers/url/parse", h.HandleSupplierURLParse)

			// Inspiration CRUD
			r.Get("/inspiration/new", h.HandleInspirationNew)
			r.Post("/inspiration", h.HandleInspirationCreate)
			r.Get("/inspiration/{id}/edit", h.HandleInspirationEdit)
			r.Put("/inspiration/{id}", h.HandleInspirationUpdate)
			r.Delete("/inspiration/{id}", h.HandleInspirationDelete)
		})

		// -----------------------------------------------------------
		// SYSTEM ADMINISTRATION ROUTES (Admins Only)
		// -----------------------------------------------------------
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireRole("admin"))

			// Audit Logs
			r.Get("/audit-logs", h.HandleAuditLogs)

			// Hardware Configuration
			r.Post("/hardware", h.HandleHardwareCreate)
			r.Post("/hardware/import", h.HandleHardwareImport)
			r.Get("/hardware/{id}/edit", h.HandleHardwareEdit)
			r.Post("/hardware/{id}/update", h.HandleHardwareUpdate)
			r.Get("/hardware/{id}/export", h.HandleHardwareExport)
			r.Post("/hardware/{id}/delete", h.HandleHardwareDelete)
			r.Post("/hardware/{id}/grid", h.HandleHardwareGridSave)
			r.Group(func(r chi.Router) {
				r.Post("/hardware/{id}/capacity", h.HandleHardwareCapacitySave)
			})

			// LED Coordinate Space Conversion (drawer-relative opt-in)
			r.Get("/hardware/conversion", h.HandleConversionPreview)
			r.Post("/hardware/conversion", h.HandleConversionConfirm)

			// System Settings Update
			r.Post("/settings", h.HandleSettingsUpdate)
			r.Get("/settings/backup/download", h.HandleBackupDownload)
			r.Post("/settings/backup/restore", h.HandleBackupRestore)

			// User Management
			r.Post("/settings/users", h.HandleUserCreate)
			r.Post("/settings/users/{id}/delete", h.HandleUserDelete)
			r.Post("/settings/users/{id}/reset", h.HandleUserForceReset)

			// Wall Management
			r.Get("/walls/{id}/edit", h.HandleWallEdit)
			r.Post("/walls", h.HandleWallCreate)
			r.Post("/walls/{id}", h.HandleWallUpdate)
			r.Post("/walls/{id}/delete", h.HandleWallDelete)

			// Supplier Credentials
			r.Get("/suppliers/credentials", h.HandleSupplierCredentialsPage)
			r.Post("/suppliers/credentials", h.HandleSupplierCredentialsSave)
			r.Get("/suppliers/oauth/callback", h.HandleSupplierOAuthCallback)
			r.Get("/suppliers/oauth/start", h.HandleSupplierOAuthStart)
			r.Get("/suppliers/recent", h.HandleSupplierRecent)
			r.Get("/suppliers/compare", h.HandleSupplierCompare)
			r.Get("/suppliers/price-history", h.HandleSupplierPriceHistory)
			r.Post("/suppliers/price-snapshot", h.HandleSupplierRecordSnapshot)
		})
	})

	return r
}
