-- +goose Up
-- Add explicit physical canvas bounds for drawer placement. Existing cabinets
-- retain a neutral 100×100 grid canvas; no inventory, bin, controller or LED
-- mapping is changed.
ALTER TABLE cabinets ADD COLUMN layout_width REAL NOT NULL DEFAULT 100 CHECK (layout_width > 0);
ALTER TABLE cabinets ADD COLUMN layout_height REAL NOT NULL DEFAULT 100 CHECK (layout_height > 0);

-- +goose Down
-- SQLite cannot drop columns on the project's supported compatibility range.
-- This additive metadata is intentionally retained on rollback; no physical
-- identities, stock, or mappings are destroyed.
