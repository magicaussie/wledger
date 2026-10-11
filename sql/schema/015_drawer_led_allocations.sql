-- +goose Up
PRAGMA foreign_keys = ON;

-- Persistent physical-drawer assignments use controller-wide, user-facing
-- 1-based pixel coordinates. These are deliberately separate from legacy
-- containers (WLED segments) and bins.led_index. Physical strings and WLED
-- segments are not assumed to be interchangeable by this table.
CREATE TABLE drawer_led_allocations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cabinet_id INTEGER NOT NULL,
    drawer_id INTEGER NOT NULL,
    controller_id INTEGER NOT NULL,
    start_pixel INTEGER NOT NULL CHECK (start_pixel >= 1),
    pixel_count INTEGER NOT NULL CHECK (pixel_count >= 1),
    display_order INTEGER NOT NULL DEFAULT 0 CHECK (display_order >= 0),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (start_pixel <= 9223372036854775807 - pixel_count + 1),
    FOREIGN KEY (drawer_id, cabinet_id) REFERENCES drawers(id, cabinet_id) ON DELETE RESTRICT,
    FOREIGN KEY (cabinet_id, controller_id) REFERENCES cabinets(id, controller_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (controller_id) REFERENCES controllers(id) ON DELETE RESTRICT
);
CREATE INDEX idx_drawer_led_allocations_controller_range
    ON drawer_led_allocations(controller_id, start_pixel, pixel_count);
CREATE INDEX idx_drawer_led_allocations_drawer_order
    ON drawer_led_allocations(drawer_id, display_order, id);

-- +goose Down
-- Preserve configured electrical data: rollback is refused while any allocation
-- remains, then only the additive table/indexes are removed.
CREATE TEMP TABLE _drawer_led_rollback_guard (ok INTEGER NOT NULL CHECK (ok = 0));
INSERT INTO _drawer_led_rollback_guard (ok)
SELECT 1 WHERE EXISTS (SELECT 1 FROM drawer_led_allocations);
DROP TABLE _drawer_led_rollback_guard;
DROP TABLE drawer_led_allocations;
