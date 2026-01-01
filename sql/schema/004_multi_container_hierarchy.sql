-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

-- Create Containers
CREATE TABLE containers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    controller_id INTEGER NOT NULL,
    segment_id INTEGER NOT NULL DEFAULT 0,
    config_json TEXT DEFAULT '{"type":"grid","rows":8,"cols":8}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(controller_id) REFERENCES controllers(id) ON DELETE CASCADE
);
CREATE INDEX idx_containers_controller_id ON containers(controller_id);

-- Create Walls
CREATE TABLE walls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Create Wall Cards
CREATE TABLE wall_cards (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    wall_id INTEGER NOT NULL,
    container_id INTEGER NOT NULL,
    position_index INTEGER NOT NULL DEFAULT 0,
    config_json TEXT,
    FOREIGN KEY(wall_id) REFERENCES walls(id) ON DELETE CASCADE,
    FOREIGN KEY(container_id) REFERENCES containers(id) ON DELETE CASCADE
);
CREATE INDEX idx_wall_cards_wall_id ON wall_cards(wall_id);

-- Data Migration: Create Default Containers
-- Create a container for each controller, inheriting its config
INSERT INTO containers (name, controller_id, config_json)
SELECT name || ' (Main)', id, config_json FROM controllers;

-- Recreate Bins
CREATE TABLE bins_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    container_id INTEGER NOT NULL,
    led_index INTEGER,
    width INTEGER DEFAULT 1,
    grid_x INTEGER,
    grid_y INTEGER,
    FOREIGN KEY(container_id) REFERENCES containers(id) ON DELETE CASCADE,
    UNIQUE(container_id, led_index)
);

-- Copy data, mapping controller_id to the new container_id
-- Join existing bins to the newly created containers via controller_id.
-- Since exactly 1 container per controller is created above, this mapping is 1-to-1.
INSERT INTO bins_new (id, name, container_id, led_index, width, grid_x, grid_y)
SELECT b.id, b.name, c.id, b.led_index, b.width, b.grid_x, b.grid_y
FROM bins b
JOIN containers c ON b.controller_id = c.controller_id;

DROP TABLE bins;
ALTER TABLE bins_new RENAME TO bins;
CREATE INDEX idx_bins_container_id ON bins(container_id);

-- Recreate Controllers (Remove config_json)
CREATE TABLE controllers_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    ip_address TEXT NOT NULL,
    port INTEGER DEFAULT 80,
    mac_address TEXT,
    is_online BOOLEAN DEFAULT 0,
    led_count INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO controllers_new (id, name, ip_address, port, mac_address, is_online, led_count, created_at)
SELECT id, name, ip_address, port, mac_address, is_online, led_count, created_at FROM controllers;

DROP TABLE controllers;
ALTER TABLE controllers_new RENAME TO controllers;

PRAGMA foreign_keys = ON;
