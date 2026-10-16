-- +goose Up
CREATE TABLE physical_bin_pixels (
    bin_id INTEGER PRIMARY KEY REFERENCES bins(id) ON DELETE CASCADE,
    drawer_id INTEGER NOT NULL REFERENCES drawers(id) ON DELETE CASCADE,
    allocation_id INTEGER NOT NULL REFERENCES drawer_led_allocations(id) ON DELETE RESTRICT,
    pixel_offset INTEGER NOT NULL CHECK (pixel_offset >= 0),
    controller_id INTEGER NOT NULL REFERENCES controllers(id) ON DELETE RESTRICT,
    pixel INTEGER NOT NULL CHECK (pixel >= 0),
    topology_fingerprint TEXT NOT NULL CHECK (length(topology_fingerprint) = 64),
    UNIQUE (allocation_id, pixel_offset),
    UNIQUE (controller_id, pixel)
);
-- +goose StatementBegin
CREATE TRIGGER physical_bin_pixels_insert_guard BEFORE INSERT ON physical_bin_pixels
BEGIN
 SELECT RAISE(ABORT, 'physical bin pixel must reference its drawer allocation and an in-range offset')
 WHERE NOT EXISTS (SELECT 1 FROM drawer_led_allocations a WHERE a.id=NEW.allocation_id AND a.drawer_id=NEW.drawer_id AND NEW.pixel_offset<a.pixel_count);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER physical_bin_pixels_update_guard BEFORE UPDATE ON physical_bin_pixels
BEGIN
 SELECT RAISE(ABORT, 'physical bin pixel must reference its drawer allocation and an in-range offset')
 WHERE NOT EXISTS (SELECT 1 FROM drawer_led_allocations a WHERE a.id=NEW.allocation_id AND a.drawer_id=NEW.drawer_id AND NEW.pixel_offset<a.pixel_count);
END;
-- +goose StatementEnd
-- Preserve allocation identity; existing replace-all editors must not delete or
-- retarget a range that has explicit bin assignments.
-- +goose StatementBegin
CREATE TRIGGER physical_bin_pixels_allocation_guard BEFORE UPDATE OF cabinet_id, drawer_id, start_pixel, pixel_count ON drawer_led_allocations
WHEN EXISTS (SELECT 1 FROM physical_bin_pixels p WHERE p.allocation_id=OLD.id)
 AND (NEW.cabinet_id<>OLD.cabinet_id OR NEW.drawer_id<>OLD.drawer_id OR NEW.start_pixel<>OLD.start_pixel OR NEW.pixel_count<>OLD.pixel_count)
BEGIN
 SELECT RAISE(ABORT, 'clear physical bin pixel overrides before changing this allocation');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER physical_bin_pixels_allocation_delete_guard BEFORE DELETE ON drawer_led_allocations
WHEN EXISTS (SELECT 1 FROM physical_bin_pixels p WHERE p.allocation_id=OLD.id)
BEGIN
 SELECT RAISE(ABORT, 'clear physical bin pixel overrides before replacing or deleting this allocation');
END;
-- +goose StatementEnd

-- +goose Down
-- Refuse rollback before touching guards or data if any overrides would be lost.
CREATE TEMP TABLE physical_bin_pixels_rollback_guard (
    override_count INTEGER NOT NULL
    CONSTRAINT clear_physical_bin_pixel_overrides_before_rollback CHECK (override_count = 0)
);
INSERT INTO physical_bin_pixels_rollback_guard SELECT COUNT(*) FROM physical_bin_pixels;
DROP TABLE physical_bin_pixels_rollback_guard;
DROP TRIGGER physical_bin_pixels_allocation_delete_guard;
DROP TRIGGER physical_bin_pixels_allocation_guard;
DROP TABLE physical_bin_pixels;
