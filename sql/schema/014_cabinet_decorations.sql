-- +goose Up
-- Non-inventory Paint objects are stored separately from drawers, bin
-- placements and electrical mappings. Existing cabinets receive an empty list.
ALTER TABLE cabinets ADD COLUMN layout_decorations_json TEXT NOT NULL DEFAULT '[]';

-- +goose Down
-- Deliberately retain additive editor metadata on SQLite versions without
-- portable DROP COLUMN support. No inventory or physical placement is touched.
