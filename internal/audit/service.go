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

	err := q.CreateAuditLog(ctx, db.CreateAuditLogParams{
		UserID:     nullUserID,
		ActionType: action,
		EntityType: entityType,
		EntityID:   entityID,
		Details:    sql.NullString{String: details, Valid: details != ""},
		OldValue:   oldJSON,
		NewValue:   newJSON,
	})

	if err != nil {
		slog.Error("failed to create audit log", "error", err)
	}
}

// Global helper for legacy logging. Prefer using Service methods.
func Log(ctx context.Context, q db.Querier, action, entityType string, entityID int64, details string, oldVal, newVal any) {
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

	err := q.CreateAuditLog(ctx, db.CreateAuditLogParams{
		UserID:     nullUserID,
		ActionType: action,
		EntityType: entityType,
		EntityID:   entityID,
		Details:    sql.NullString{String: details, Valid: details != ""},
		OldValue:   oldJSON,
		NewValue:   newJSON,
	})

	if err != nil {
		slog.Error("failed to create audit log", "error", err)
	}
}
