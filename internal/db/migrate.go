package db

import (
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/sql/schema"
)

// Migrate applies the schema migrations to the database.
func Migrate(db *sql.DB) error {
	goose.SetBaseFS(schema.Migrations)

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	slog.Info("running database migrations")
	// Goose will look for migrations in the current directory of the FS provided.
	// Since files in sql/schema are embedded as the root of schema.Migrations,
	// use "." as the directory.
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}
