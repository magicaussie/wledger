-- +goose Up

-- Tracks price snapshots over time for each part+supplier combination
CREATE TABLE IF NOT EXISTS price_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id INTEGER NOT NULL,
    supplier_ref_id INTEGER NOT NULL,
    min_quantity INTEGER NOT NULL DEFAULT 1,
    price REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'USD',
    includes_tax BOOLEAN DEFAULT 0,
    recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(part_id) REFERENCES parts(id) ON DELETE CASCADE,
    FOREIGN KEY(supplier_ref_id) REFERENCES supplier_references(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_price_history_part_id ON price_history(part_id);
CREATE INDEX IF NOT EXISTS idx_price_history_supplier_ref ON price_history(supplier_ref_id);
CREATE INDEX IF NOT EXISTS idx_price_history_recorded ON price_history(recorded_at);

-- +goose Down
DROP TABLE IF EXISTS price_history;
