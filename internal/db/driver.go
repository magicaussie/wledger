package db

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Open() opens a database connection and configures it for use
func Open(dsn string) (*sql.DB, error) {
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

	// Performance and safety settings
	// WAL (write ahead logging) mode allows simultaneous readers and writers
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL;"); err != nil {
		return nil, err
	}
	// Enable foreign keys, as they are disabled by default in SQLite
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON;"); err != nil {
		return nil, err
	}
	// Busy timeout prevents "database is locked" errors, since SQLite allows only 1 writer at a time
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout=5000;"); err != nil {
		return nil, err
	}

	slog.Info("SQLite database connection opened")

	return db, nil
}
