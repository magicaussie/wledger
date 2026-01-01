package db_test

import (
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/tuxedocurly/wledger/internal/db"
	"github.com/tuxedocurly/wledger/sql/schema"
)

func TestMigrate_004_Hierarchy(t *testing.T) {
	// Setup DB
	conn, err := db.Open("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer conn.Close()

	goose.SetBaseFS(schema.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("failed to set dialect: %v", err)
	}

	// Migrate to 003
	if err := goose.UpTo(conn, ".", 3); err != nil {
		t.Fatalf("failed to migrate to 003: %v", err)
	}

	// Insert Test Data (Old Schema)
	// Insert a controller
	_, err = conn.Exec(`
        INSERT INTO controllers (id, name, ip_address, config_json) 
        VALUES (100, 'Test Controller', '192.168.1.100', '{"old":"config"}')
    `)
	if err != nil {
		t.Fatalf("failed to insert controller: %v", err)
	}

	// Insert a bin linked to that controller
	_, err = conn.Exec(`
        INSERT INTO bins (name, controller_id, led_index, grid_x, grid_y)
        VALUES ('Bin A1', 100, 10, 1, 1)
    `)
	if err != nil {
		t.Fatalf("failed to insert bin: %v", err)
	}

	// DEBUG: Check controller count
	var count int
	err = conn.QueryRow("SELECT COUNT(*) FROM controllers").Scan(&count)
	if err != nil {
		t.Fatalf("failed to count controllers: %v", err)
	}
	t.Logf("Controllers before migration: %d", count)

	// Run Migration to 004 (Latest)
	// This should execute 004_multi_container_hierarchy.sql
	if err := goose.Up(conn, "."); err != nil {
		t.Fatalf("failed to migrate to latest: %v", err)
	}

	// DEBUG: Check container count
	err = conn.QueryRow("SELECT COUNT(*) FROM containers").Scan(&count)
	if err != nil {
		t.Logf("failed to count containers (maybe table missing): %v", err)
	} else {
		t.Logf("Containers after migration: %d", count)
	}

	// Assertions
	// Check if container was created
	var containerName string
	var containerConfig string
	err = conn.QueryRow("SELECT name, config_json FROM containers WHERE controller_id = 100").Scan(&containerName, &containerConfig)
	if err != nil {
		// If 004 didn't run, table 'containers' won't exist
		t.Fatalf("failed to query new container: %v", err)
	}

	expectedName := "Test Controller (Main)"
	if containerName != expectedName {
		t.Errorf("expected container name %q, got %q", expectedName, containerName)
	}
	if containerConfig != `{"old":"config"}` {
		t.Errorf("expected container config to be copied, got %q", containerConfig)
	}

	// Check if bin was migrated
	var binContainerID int64
	err = conn.QueryRow("SELECT container_id FROM bins WHERE name = 'Bin A1'").Scan(&binContainerID)
	if err != nil {
		t.Fatalf("failed to query migrated bin: %v", err)
	}

	// Get the container ID from the container we found
	var containerID int64
	err = conn.QueryRow("SELECT id FROM containers WHERE controller_id = 100").Scan(&containerID)

	if binContainerID != containerID {
		t.Errorf("expected bin to be linked to container %d, got %d", containerID, binContainerID)
	}

	// Check that controllers table no longer has config_json
	_, err = conn.Query("SELECT config_json FROM controllers")
	if err == nil {
		t.Error("expected error selecting config_json from controllers, but got nil")
	}
}
