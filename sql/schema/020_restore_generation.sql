-- +goose Up
CREATE TABLE restore_generation (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    generation_id TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO restore_generation (id, generation_id) VALUES (1, '');

-- +goose Down
DROP TABLE restore_generation;
