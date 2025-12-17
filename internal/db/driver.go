package db

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Open() opens a database connection and configures it for use
func Open(dsn string) (*sql.DB, error) {
	// Ensure Foreign Keys are enabled for all connections in the pool via DSN
	if !strings.Contains(dsn, "?") {
		dsn += "?_foreign_keys=on"
	} else {
		dsn += "&_foreign_keys=on"
	}

	// Add WAL mode to DSN for better concurrency
	dsn += "&_journal_mode=WAL"
	dsn += "&_busy_timeout=5000"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}

	slog.Info("SQLite database connection opened", "dsn", dsn)

	return db, nil
}
