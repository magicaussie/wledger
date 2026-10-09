-- +goose Up
-- Drawer-owned physical LED allocation (segment-relative).
-- led_start: first LED of the drawer within its WLED segment (0-based).
-- led_count: number of LEDs allocated to the drawer (0 = unallocated).
-- These are additive columns; existing bins.led_index semantics are unchanged.
ALTER TABLE containers ADD COLUMN led_start INTEGER NOT NULL DEFAULT 0;
ALTER TABLE containers ADD COLUMN led_count INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- Additive columns are intentionally left in place on rollback; dropping them
-- is not portable across SQLite versions and they are harmless if unused.
