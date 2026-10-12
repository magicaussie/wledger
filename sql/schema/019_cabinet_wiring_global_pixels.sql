-- +goose NO TRANSACTION
-- +goose Up
-- Cabinet wiring is now an ordered physical graph and all drawer allocations use
-- one cabinet-wide 1-based pixel namespace. The old controller-local ranges are
-- translated by saved controller slot and configured controller capacity.
-- +goose StatementBegin
-- Abort before changing persistent schema/data or toggling connection state if
-- memberships, capacities or controller-local ranges cannot define safe cabinet
-- offsets. Zero-capacity members are allowed, but cannot own an allocation.
DROP TABLE IF EXISTS temp._cabinet_pixel_migration_guard;
CREATE TEMP TABLE _cabinet_pixel_migration_guard (ok INTEGER NOT NULL CHECK (ok = 0));
INSERT INTO _cabinet_pixel_migration_guard (ok)
SELECT 1
WHERE EXISTS (
    SELECT 1
    FROM cabinet_controllers cc
    LEFT JOIN cabinets cabinet ON cabinet.id = cc.cabinet_id
    LEFT JOIN controllers c ON c.id = cc.controller_id
    WHERE cabinet.id IS NULL
       OR c.id IS NULL
       OR c.led_count < 0
       OR cc.slot < 0
       OR cc.slot > 4
)
   OR EXISTS (
    SELECT 1
    FROM drawer_led_allocations a
    LEFT JOIN cabinet_controllers cc
      ON cc.cabinet_id = a.cabinet_id AND cc.controller_id = a.controller_id
    LEFT JOIN controllers c ON c.id = a.controller_id
    WHERE cc.controller_id IS NULL
       OR c.id IS NULL
       OR c.led_count < 1
       OR a.start_pixel < 1
       OR a.pixel_count < 1
       OR a.pixel_count > c.led_count
       OR a.start_pixel > c.led_count - a.pixel_count + 1
       OR a.pixel_count > 9223372036854775807 - a.start_pixel + 1
);

-- SQLite SUM raises integer overflow rather than returning a wrapped integer.
-- Materialize every cabinet total in TEMP before using offsets; with the
-- non-negative-capacity check above, any overflowing total is unsafe, and the
-- error occurs before any persistent DDL or data change.
DROP TABLE IF EXISTS temp._cabinet_pixel_capacity_guard;
CREATE TEMP TABLE _cabinet_pixel_capacity_guard (
    cabinet_id INTEGER PRIMARY KEY,
    total_capacity INTEGER NOT NULL CHECK (total_capacity >= 0)
);
INSERT INTO _cabinet_pixel_capacity_guard (cabinet_id, total_capacity)
SELECT cc.cabinet_id, SUM(c.led_count)
FROM cabinet_controllers cc
JOIN controllers c ON c.id = cc.controller_id
GROUP BY cc.cabinet_id;
DROP TABLE _cabinet_pixel_capacity_guard;

-- Compare the converted intervals, not just the old controller-local values.
-- The preceding checks guarantee each offset and endpoint is representable.
WITH translated AS (
    SELECT a.id, a.cabinet_id,
           a.start_pixel + COALESCE((
               SELECT SUM(prior_controller.led_count)
               FROM cabinet_controllers prior
               JOIN controllers prior_controller ON prior_controller.id = prior.controller_id
               WHERE prior.cabinet_id = current.cabinet_id AND prior.slot < current.slot
           ), 0) AS start_pixel,
           a.pixel_count
    FROM drawer_led_allocations a
    JOIN cabinet_controllers current
      ON current.cabinet_id = a.cabinet_id AND current.controller_id = a.controller_id
)
INSERT INTO _cabinet_pixel_migration_guard (ok)
SELECT 1
FROM translated a
JOIN translated b
  ON b.cabinet_id = a.cabinet_id AND b.id > a.id
WHERE a.start_pixel <= b.start_pixel + b.pixel_count - 1
  AND b.start_pixel <= a.start_pixel + a.pixel_count - 1
LIMIT 1;
DROP TABLE _cabinet_pixel_migration_guard;

PRAGMA foreign_keys = OFF;
BEGIN;

-- Allow slot values above the user-facing 0..4 range only as internal temporary
-- values during an atomic reorder. The five-membership limit remains enforced.
CREATE TABLE cabinet_controllers_rebuild (
    cabinet_id INTEGER NOT NULL,
    controller_id INTEGER NOT NULL,
    slot INTEGER NOT NULL CHECK (slot >= 0),
    strings_configured INTEGER NOT NULL DEFAULT 0 CHECK (strings_configured IN (0, 1)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (cabinet_id, controller_id),
    UNIQUE (controller_id),
    UNIQUE (cabinet_id, slot),
    FOREIGN KEY (cabinet_id) REFERENCES cabinets(id) ON DELETE RESTRICT,
    FOREIGN KEY (controller_id) REFERENCES controllers(id) ON DELETE RESTRICT
);
INSERT INTO cabinet_controllers_rebuild (cabinet_id, controller_id, slot, strings_configured, created_at, updated_at)
SELECT cabinet_id, controller_id, slot, 0, created_at, updated_at
FROM cabinet_controllers;
DROP TABLE cabinet_controllers;
ALTER TABLE cabinet_controllers_rebuild RENAME TO cabinet_controllers;

CREATE TRIGGER cabinet_controllers_max_five_insert
BEFORE INSERT ON cabinet_controllers
WHEN (SELECT COUNT(*) FROM cabinet_controllers WHERE cabinet_id = NEW.cabinet_id) >= 5
BEGIN
    SELECT RAISE(ABORT, 'a cabinet may have at most five controllers');
END;
CREATE TRIGGER cabinet_controllers_max_five_update
BEFORE UPDATE OF cabinet_id ON cabinet_controllers
WHEN NEW.cabinet_id <> OLD.cabinet_id
 AND (SELECT COUNT(*) FROM cabinet_controllers WHERE cabinet_id = NEW.cabinet_id) >= 5
BEGIN
    SELECT RAISE(ABORT, 'a cabinet may have at most five controllers');
END;

-- Temporary high slot values are accepted only while the owning cabinet has an
-- in-transaction reorder guard; committed controller order remains 0..4.
CREATE TABLE cabinet_controller_reorder_guard (
    cabinet_id INTEGER PRIMARY KEY,
    FOREIGN KEY (cabinet_id) REFERENCES cabinets(id) ON DELETE CASCADE
);
CREATE TRIGGER cabinet_controllers_order_guard_insert
BEFORE INSERT ON cabinet_controllers
WHEN NEW.slot >= 5
 AND NOT EXISTS (SELECT 1 FROM cabinet_controller_reorder_guard WHERE cabinet_id = NEW.cabinet_id)
BEGIN
    SELECT RAISE(ABORT, 'controller order slot outside 0..4 requires reorder transaction guard');
END;
CREATE TRIGGER cabinet_controllers_order_guard_update
BEFORE UPDATE OF slot ON cabinet_controllers
WHEN NEW.slot >= 5
 AND NOT EXISTS (SELECT 1 FROM cabinet_controller_reorder_guard WHERE cabinet_id = NEW.cabinet_id)
BEGIN
    SELECT RAISE(ABORT, 'controller order slot outside 0..4 requires reorder transaction guard');
END;

CREATE TABLE cabinet_led_strings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cabinet_id INTEGER NOT NULL,
    controller_id INTEGER NOT NULL,
    name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 200),
    display_order INTEGER NOT NULL CHECK (display_order >= 0),
    pixel_count INTEGER NOT NULL CHECK (pixel_count >= 1),
    layout_complete INTEGER NOT NULL DEFAULT 0 CHECK (layout_complete IN (0, 1)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (cabinet_id, controller_id, display_order),
    UNIQUE (id, cabinet_id, controller_id),
    FOREIGN KEY (cabinet_id, controller_id)
        REFERENCES cabinet_controllers(cabinet_id, controller_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);
CREATE INDEX idx_cabinet_led_strings_controller_order
    ON cabinet_led_strings(cabinet_id, controller_id, display_order, id);

CREATE TABLE cabinet_led_path_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    string_id INTEGER NOT NULL,
    display_order INTEGER NOT NULL CHECK (display_order >= 0),
    pixel_count INTEGER NOT NULL CHECK (pixel_count >= 1),
    points_json TEXT NOT NULL DEFAULT '[]',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (string_id, display_order),
    FOREIGN KEY (string_id) REFERENCES cabinet_led_strings(id) ON DELETE RESTRICT
);
CREATE INDEX idx_cabinet_led_path_sections_string_order
    ON cabinet_led_path_sections(string_id, display_order, id);

-- A range now remains one logical cabinet-wide range even when it crosses one
-- or more controller or string boundaries. Controller-local spans are derived at
-- Locate time; they are not stored as a second source of truth.
CREATE TABLE drawer_led_allocations_rebuild (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cabinet_id INTEGER NOT NULL,
    drawer_id INTEGER NOT NULL,
    start_pixel INTEGER NOT NULL CHECK (start_pixel >= 1),
    pixel_count INTEGER NOT NULL CHECK (pixel_count >= 1),
    display_order INTEGER NOT NULL DEFAULT 0 CHECK (display_order >= 0),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (start_pixel <= 9223372036854775807 - pixel_count + 1),
    FOREIGN KEY (drawer_id, cabinet_id) REFERENCES drawers(id, cabinet_id) ON DELETE RESTRICT
);
INSERT INTO drawer_led_allocations_rebuild
    (id, cabinet_id, drawer_id, start_pixel, pixel_count, display_order, created_at, updated_at)
SELECT a.id, a.cabinet_id, a.drawer_id,
       a.start_pixel + COALESCE((
           SELECT SUM(prior_controller.led_count)
           FROM cabinet_controllers prior
           JOIN controllers prior_controller ON prior_controller.id = prior.controller_id
           WHERE prior.cabinet_id = a.cabinet_id AND prior.slot < current.slot
       ), 0),
       a.pixel_count, a.display_order, a.created_at, a.updated_at
FROM drawer_led_allocations a
JOIN cabinet_controllers current
  ON current.cabinet_id = a.cabinet_id AND current.controller_id = a.controller_id;

DELETE FROM sqlite_sequence
 WHERE name = 'drawer_led_allocations_rebuild'
   AND (SELECT seq FROM sqlite_sequence WHERE name = 'drawer_led_allocations') > seq;
INSERT INTO sqlite_sequence (name, seq)
SELECT 'drawer_led_allocations_rebuild', (SELECT seq FROM sqlite_sequence WHERE name = 'drawer_led_allocations')
 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'drawer_led_allocations_rebuild')
   AND (SELECT seq FROM sqlite_sequence WHERE name = 'drawer_led_allocations') IS NOT NULL;
DROP TABLE drawer_led_allocations;
ALTER TABLE drawer_led_allocations_rebuild RENAME TO drawer_led_allocations;
CREATE INDEX idx_drawer_led_allocations_controller_range
    ON drawer_led_allocations(cabinet_id, start_pixel, pixel_count);
CREATE INDEX idx_drawer_led_allocations_drawer_order
    ON drawer_led_allocations(drawer_id, display_order, id);

CREATE TRIGGER drawer_led_allocations_no_overlap_insert
BEFORE INSERT ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.cabinet_id = NEW.cabinet_id
      AND NEW.start_pixel <= existing.start_pixel + existing.pixel_count - 1
      AND existing.start_pixel <= NEW.start_pixel + NEW.pixel_count - 1
)
BEGIN
    SELECT RAISE(ABORT, 'drawer LED ranges overlap in cabinet pixel space');
END;
CREATE TRIGGER drawer_led_allocations_no_overlap_update
BEFORE UPDATE OF cabinet_id, start_pixel, pixel_count ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.cabinet_id = NEW.cabinet_id
      AND existing.id <> OLD.id
      AND NEW.start_pixel <= existing.start_pixel + existing.pixel_count - 1
      AND existing.start_pixel <= NEW.start_pixel + NEW.pixel_count - 1
)
BEGIN
    SELECT RAISE(ABORT, 'drawer LED ranges overlap in cabinet pixel space');
END;

-- Keep the pre-existing logical session row as the durable session owner and
-- add one durable row per controller target. Existing sessions migrate as a
-- single-target session without changing their cleanup evidence. Controller IDs
-- deliberately have no FK here: backup restore/controller deletion must not erase
-- the endpoint and command snapshots needed to detect or retry stale cleanup.
CREATE TABLE physical_locate_session_targets (
    session_id TEXT NOT NULL,
    controller_id INTEGER NOT NULL,
    controller_address TEXT NOT NULL,
    controller_port INTEGER NOT NULL,
    ranges_json TEXT NOT NULL,
    commands_json TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    may_have_received INTEGER NOT NULL DEFAULT 0 CHECK (may_have_received IN (0, 1)),
    last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, controller_id),
    FOREIGN KEY (session_id) REFERENCES physical_locate_sessions(session_id) ON DELETE CASCADE
);
INSERT INTO physical_locate_session_targets
    (session_id, controller_id, controller_address, controller_port, ranges_json, commands_json, fingerprint, may_have_received, last_error)
SELECT session_id, controller_id, controller_address, controller_port, ranges_json, commands_json, fingerprint,
       CASE WHEN state IN ('activating', 'active', 'partial', 'stopping') THEN 1 ELSE 0 END,
       last_error
FROM physical_locate_sessions;

COMMIT;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose NO TRANSACTION
-- +goose Down
-- A downgrade is permitted only while the new wiring graph is empty, every
-- saved global range maps wholly to one legacy controller span, and each durable
-- Locate session still has exactly one target. Otherwise refuse without mutation.
-- +goose StatementBegin
CREATE TEMP TABLE _cabinet_wiring_rollback_guard (ok INTEGER NOT NULL CHECK (ok = 0));
INSERT INTO _cabinet_wiring_rollback_guard (ok)
SELECT 1
WHERE EXISTS (SELECT 1 FROM cabinet_led_strings)
   OR EXISTS (SELECT 1 FROM cabinet_controllers WHERE strings_configured = 1)
   OR EXISTS (
       SELECT 1 FROM physical_locate_session_targets target
       GROUP BY target.session_id
       HAVING COUNT(*) <> 1
   )
   OR EXISTS (
       SELECT 1 FROM cabinet_controllers WHERE slot >= 5
   )
   OR EXISTS (
       SELECT 1 FROM drawer_led_allocations a
       WHERE (
           SELECT COUNT(*)
           FROM cabinet_controllers cc
           JOIN controllers c ON c.id = cc.controller_id
           WHERE cc.cabinet_id = a.cabinet_id
             AND a.start_pixel > COALESCE((
                 SELECT SUM(prior_controller.led_count)
                 FROM cabinet_controllers prior
                 JOIN controllers prior_controller ON prior_controller.id = prior.controller_id
                 WHERE prior.cabinet_id = cc.cabinet_id AND prior.slot < cc.slot
             ), 0)
             AND a.start_pixel + a.pixel_count - 1 <= COALESCE((
                 SELECT SUM(prior_controller.led_count)
                 FROM cabinet_controllers prior
                 JOIN controllers prior_controller ON prior_controller.id = prior.controller_id
                 WHERE prior.cabinet_id = cc.cabinet_id AND prior.slot < cc.slot
             ), 0) + c.led_count
       ) <> 1
   );
DROP TABLE _cabinet_wiring_rollback_guard;

PRAGMA foreign_keys = OFF;
BEGIN;

CREATE TABLE cabinet_controllers_rebuild (
    cabinet_id INTEGER NOT NULL,
    controller_id INTEGER NOT NULL,
    slot INTEGER NOT NULL CHECK (slot >= 0 AND slot < 5),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (cabinet_id, controller_id),
    UNIQUE (controller_id),
    UNIQUE (cabinet_id, slot),
    FOREIGN KEY (cabinet_id) REFERENCES cabinets(id) ON DELETE RESTRICT,
    FOREIGN KEY (controller_id) REFERENCES controllers(id) ON DELETE RESTRICT
);
INSERT INTO cabinet_controllers_rebuild (cabinet_id, controller_id, slot, created_at, updated_at)
SELECT cabinet_id, controller_id, slot, created_at, updated_at FROM cabinet_controllers;
DROP TABLE cabinet_controllers;
ALTER TABLE cabinet_controllers_rebuild RENAME TO cabinet_controllers;

CREATE TABLE drawer_led_allocations_rebuild (
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
    FOREIGN KEY (cabinet_id, controller_id) REFERENCES cabinet_controllers(cabinet_id, controller_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (controller_id) REFERENCES controllers(id) ON DELETE RESTRICT
);
INSERT INTO drawer_led_allocations_rebuild
    (id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at)
SELECT a.id, a.cabinet_id, a.drawer_id, cc.controller_id,
       a.start_pixel - COALESCE((
           SELECT SUM(prior_controller.led_count)
           FROM cabinet_controllers prior
           JOIN controllers prior_controller ON prior_controller.id = prior.controller_id
           WHERE prior.cabinet_id = a.cabinet_id AND prior.slot < cc.slot
       ), 0),
       a.pixel_count, a.display_order, a.created_at, a.updated_at
FROM drawer_led_allocations a
JOIN cabinet_controllers cc ON cc.cabinet_id = a.cabinet_id
JOIN controllers c ON c.id = cc.controller_id
WHERE a.start_pixel > COALESCE((
          SELECT SUM(prior_controller.led_count)
          FROM cabinet_controllers prior
          JOIN controllers prior_controller ON prior_controller.id = prior.controller_id
          WHERE prior.cabinet_id = a.cabinet_id AND prior.slot < cc.slot
      ), 0)
  AND a.start_pixel + a.pixel_count - 1 <= COALESCE((
          SELECT SUM(prior_controller.led_count)
          FROM cabinet_controllers prior
          JOIN controllers prior_controller ON prior_controller.id = prior.controller_id
          WHERE prior.cabinet_id = a.cabinet_id AND prior.slot < cc.slot
      ), 0) + c.led_count;

DELETE FROM sqlite_sequence
 WHERE name = 'drawer_led_allocations_rebuild'
   AND (SELECT seq FROM sqlite_sequence WHERE name = 'drawer_led_allocations') > seq;
INSERT INTO sqlite_sequence (name, seq)
SELECT 'drawer_led_allocations_rebuild', (SELECT seq FROM sqlite_sequence WHERE name = 'drawer_led_allocations')
 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'drawer_led_allocations_rebuild')
   AND (SELECT seq FROM sqlite_sequence WHERE name = 'drawer_led_allocations') IS NOT NULL;
DROP TABLE drawer_led_allocations;
ALTER TABLE drawer_led_allocations_rebuild RENAME TO drawer_led_allocations;
CREATE INDEX idx_drawer_led_allocations_controller_range
    ON drawer_led_allocations(controller_id, start_pixel, pixel_count);
CREATE INDEX idx_drawer_led_allocations_drawer_order
    ON drawer_led_allocations(drawer_id, display_order, id);
CREATE TRIGGER drawer_led_allocations_one_controller_insert
BEFORE INSERT ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.drawer_id = NEW.drawer_id AND existing.controller_id <> NEW.controller_id
)
BEGIN SELECT RAISE(ABORT, 'drawer LED ranges must use one controller'); END;
CREATE TRIGGER drawer_led_allocations_one_controller_update
BEFORE UPDATE OF drawer_id, controller_id ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.drawer_id = NEW.drawer_id AND existing.controller_id <> NEW.controller_id AND existing.id <> OLD.id
)
BEGIN SELECT RAISE(ABORT, 'drawer LED ranges must use one controller'); END;
CREATE TRIGGER drawer_led_allocations_no_overlap_insert
BEFORE INSERT ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.controller_id = NEW.controller_id
      AND NEW.start_pixel <= existing.start_pixel + existing.pixel_count - 1
      AND existing.start_pixel <= NEW.start_pixel + NEW.pixel_count - 1
)
BEGIN SELECT RAISE(ABORT, 'drawer LED ranges overlap on controller'); END;
CREATE TRIGGER drawer_led_allocations_no_overlap_update
BEFORE UPDATE OF controller_id, start_pixel, pixel_count ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.controller_id = NEW.controller_id AND existing.id <> OLD.id
      AND NEW.start_pixel <= existing.start_pixel + existing.pixel_count - 1
      AND existing.start_pixel <= NEW.start_pixel + NEW.pixel_count - 1
)
BEGIN SELECT RAISE(ABORT, 'drawer LED ranges overlap on controller'); END;

DROP TABLE physical_locate_session_targets;
DROP TABLE cabinet_led_path_sections;
DROP TABLE cabinet_led_strings;
DROP TABLE cabinet_controller_reorder_guard;
COMMIT;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd
