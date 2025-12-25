package handler

import (
	"database/sql"
	"log/slog"

	"github.com/alexedwards/scs/v2"
	"github.com/tuxedocurly/wledger/internal/backup"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/inspiration"
	"github.com/tuxedocurly/wledger/internal/parts"
	"github.com/tuxedocurly/wledger/internal/tags"
	"github.com/tuxedocurly/wledger/internal/uierror"
	"github.com/tuxedocurly/wledger/internal/wled"
)

// Handler holds shared dependencies for all HTTP Handlers
type Handler struct {
	Logger      *slog.Logger
	Queries     db.Store
	Session     *scs.SessionManager
	WLED        *wled.Client
	Database    *sql.DB
	Backup      backup.Service
	Parts       parts.Service
	Tags        tags.Service
	Inspiration inspiration.Service
	UIError     *uierror.Responder
}

// New creates a new Handler with dependencies
func New(
	logger *slog.Logger,
	queries db.Store,
	session *scs.SessionManager,
	wledClient *wled.Client,
	database *sql.DB,
	backupService backup.Service,
	partsService parts.Service,
	tagsService tags.Service,
	inspirationService inspiration.Service,
	uiError *uierror.Responder,
) *Handler {
	return &Handler{
		Logger:      logger,
		Queries:     queries,
		Session:     session,
		WLED:        wledClient,
		Database:    database,
		Backup:      backupService,
		Parts:       partsService,
		Tags:        tagsService,
		Inspiration: inspirationService,
		UIError:     uiError,
	}
}

