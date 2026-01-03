-- +goose Up
ALTER TABLE containers ADD COLUMN position_index INTEGER NOT NULL DEFAULT 0;

-- Initialize position_index with current id to maintain existing order
UPDATE containers SET position_index = id;

-- +goose Down
