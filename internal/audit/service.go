package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

type Service interface {
	Log(ctx context.Context, action, entityType string, entityID int64, details string, oldVal, newVal any)
	LogWithTx(ctx context.Context, q db.Querier, action, entityType string, entityID int64, details string, oldVal, newVal any)
	ListLogs(ctx context.Context, params db.ListAuditLogsParams) ([]db.ListAuditLogsRow, error)
	CountLogs(ctx context.Context, params db.CountAuditLogsParams) (int64, error)
}

type service struct {
	store db.Store
}

func NewService(store db.Store) Service {
	return &service{
		store: store,
	}
}

func (s *service) ListLogs(ctx context.Context, params db.ListAuditLogsParams) ([]db.ListAuditLogsRow, error) {
	return s.store.ListAuditLogs(ctx, params)
}

func (s *service) CountLogs(ctx context.Context, params db.CountAuditLogsParams) (int64, error) {
	return s.store.CountAuditLogs(ctx, params)
}

func (s *service) Log(ctx context.Context, action, entityType string, entityID int64, details string, oldVal, newVal any) {
	s.LogWithTx(ctx, s.store, action, entityType, entityID, details, oldVal, newVal)
}

func (s *service) LogWithTx(ctx context.Context, q db.Querier, action, entityType string, entityID int64, details string, oldVal, newVal any) {
	if err := LogTx(ctx, q, action, entityType, entityID, details, oldVal, newVal); err != nil {
		slog.Error("failed to create audit log", "error", err)
	}
}

// Global helper for legacy logging. Prefer using Service methods.
//
// It is best-effort: a failure to write the audit entry is logged but not
// returned. Callers that must treat the audit entry as part of their own atomic
// change should use LogTx instead.
func Log(ctx context.Context, q db.Querier, action, entityType string, entityID int64, details string, oldVal, newVal any) {
	if err := LogTx(ctx, q, action, entityType, entityID, details, oldVal, newVal); err != nil {
		slog.Error("failed to create audit log", "error", err)
	}
}

// LogTx writes an audit entry using q and returns any error. It is intended for
// callers already inside a transaction who want the audit entry to be atomic
// with the change it records, so a failed audit write rolls the change back.
func LogTx(ctx context.Context, q db.Querier, action, entityType string, entityID int64, details string, oldVal, newVal any) error {
	// Extract userID
	var userID int64
	if id, ok := ctx.Value(middleware.UserContextKey).(int64); ok {
		userID = int64(id)
	}

	// Marshal values
	var oldJSON, newJSON []byte
	if oldVal != nil {
		oldJSON, _ = json.Marshal(oldVal)
	}
	if newVal != nil {
		newJSON, _ = json.Marshal(newVal)
	}

	// Nullable userID helper
	var nullUserID sql.NullInt64
	if userID != 0 {
		nullUserID = sql.NullInt64{Int64: userID, Valid: true}
	}

	return q.CreateAuditLog(ctx, db.CreateAuditLogParams{
		UserID:     nullUserID,
		ActionType: action,
		EntityType: entityType,
		EntityID:   entityID,
		Details:    sql.NullString{String: details, Valid: details != ""},
		OldValue:   oldJSON,
		NewValue:   newJSON,
	})
}
