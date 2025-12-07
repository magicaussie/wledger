package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
)

// Migrate reads SQL files from the disk (sql/schema) and applies them.
func Migrate(db *sql.DB) error {
	schemaDir := "sql/schema"

	files, err := os.ReadDir(schemaDir)
	if err != nil {
		return fmt.Errorf("failed to read schema directory: %w", err)
	}

	// Sort by name
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		slog.Info("applying migration", "file", file.Name())
		path := filepath.Join(schemaDir, file.Name())

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", file.Name(), err)
		}

		if _, err := db.ExecContext(context.Background(), string(content)); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", file.Name(), err)
		}
	}

	return nil
}
