package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/internal/middleware"
)

// TODO: fix this implementation before implementing the ledger functionality
// Log records an action to the audit_logs table.
// Handles JSON marshaling and user extraction automatically
func Log(ctx context.Context, q *db.Queries, action, entityType string, entityID int64, details string, oldVal, newVal any) {
	// Extract userID
	var userID int64
	if id, ok := ctx.Value(middleware.UserContextKey).(int); ok {
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

	// Insert Async (Don't block the request for auditing)
	// Note: Async is fine to keep the UI snappy, but if absolute audits
	// are eeventually desired, this would need to be synchronous.
	go func() {
		// Need a new context since the request context might cancel
		bgCtx := context.Background()

		// Nullable userID helper
		var nullUserID sql.NullInt64
		if userID != 0 {
			nullUserID = sql.NullInt64{Int64: userID, Valid: true}
		}

		err := q.CreateAuditLog(bgCtx, db.CreateAuditLogParams{
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
	}()
}
