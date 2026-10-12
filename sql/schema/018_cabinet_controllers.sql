-- +goose NO TRANSACTION
-- +goose Up
-- M6 (multiple controllers per cabinet, schema step).
--
-- `cabinets.controller_id` stays exactly as it is: it remains the legacy /
-- primary controller pointer and is neither dropped, renamed nor backfilled.
-- This migration adds a membership table so a cabinet can own up to five
-- controllers and rebuilds `drawer_led_allocations` so its
-- (cabinet_id, controller_id) key references that membership table instead of
-- cabinets(id, controller_id).
--
-- No drawer, bin, container, part, assignment or inventory row is read, moved or
-- rewritten other than the allocation rebuild below, which copies every column
-- verbatim.
--
-- SQLite cannot alter a foreign key in place, so `drawer_led_allocations` uses
-- the documented create/copy/drop/rename rebuild. Foreign key enforcement MUST
-- be off around that rebuild (ALTER TABLE ... RENAME would otherwise rewrite
-- referencing clauses and DROP TABLE would perform an implicit delete). PRAGMA
-- foreign_keys is a no-op inside a transaction, so the rebuild opts out of
-- Goose's transaction and runs inside its own explicit one. StatementBegin keeps
-- the whole body in a single statement, so the pragma, the transaction and the
-- rebuild can never be split across pooled connections. As in 012, no global
-- foreign_key_check runs: the legacy database may carry pre-existing dangling
-- references and invalid data is reported, never repaired or dropped.
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;

BEGIN;

-- Membership identity is (cabinet_id, controller_id); it is also the parent key
-- the rebuilt allocation table references. slot gives deterministic
-- per-cabinet ordering and is bounded to 0..4, so a cabinet holds at most five
-- memberships. UNIQUE (controller_id) keeps every controller owned by at most
-- one cabinet.
CREATE TABLE cabinet_controllers (
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

-- Backfill exactly one membership for each existing non-NULL
-- cabinets.controller_id: slot 0 mirrors the legacy primary pointer. Cabinets
-- with a NULL controller_id deliberately receive no membership.
INSERT INTO cabinet_controllers (cabinet_id, controller_id, slot)
SELECT id, controller_id, 0
FROM cabinets
WHERE controller_id IS NOT NULL
ORDER BY id;

-- Rebuild drawer_led_allocations: identical columns, checks and indexes, but the
-- (cabinet_id, controller_id) foreign key now targets the membership table.
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

-- Copy every allocation verbatim, including row id and both timestamps.
INSERT INTO drawer_led_allocations_rebuild
    (id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at)
SELECT id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at
FROM drawer_led_allocations;

-- Carry the AUTOINCREMENT high-water mark onto the rebuilt table so an id
-- belonging to an already deleted allocation is never handed out again.
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

-- A physical drawer is mapped to exactly one controller, even when it has
-- multiple disjoint ranges. Overlap is checked in controller-wide coordinates,
-- so identical pixel numbers on separate controllers remain valid.
CREATE TRIGGER drawer_led_allocations_one_controller_insert
BEFORE INSERT ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.drawer_id = NEW.drawer_id
      AND existing.controller_id <> NEW.controller_id
)
BEGIN
    SELECT RAISE(ABORT, 'drawer LED ranges must use one controller');
END;

CREATE TRIGGER drawer_led_allocations_one_controller_update
BEFORE UPDATE OF drawer_id, controller_id ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.drawer_id = NEW.drawer_id
      AND existing.controller_id <> NEW.controller_id
      AND existing.id <> OLD.id
)
BEGIN
    SELECT RAISE(ABORT, 'drawer LED ranges must use one controller');
END;

CREATE TRIGGER drawer_led_allocations_no_overlap_insert
BEFORE INSERT ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.controller_id = NEW.controller_id
      AND NEW.start_pixel <= existing.start_pixel + existing.pixel_count - 1
      AND existing.start_pixel <= NEW.start_pixel + NEW.pixel_count - 1
)
BEGIN
    SELECT RAISE(ABORT, 'drawer LED ranges overlap on controller');
END;

CREATE TRIGGER drawer_led_allocations_no_overlap_update
BEFORE UPDATE OF controller_id, start_pixel, pixel_count ON drawer_led_allocations
WHEN EXISTS (
    SELECT 1 FROM drawer_led_allocations existing
    WHERE existing.controller_id = NEW.controller_id
      AND existing.id <> OLD.id
      AND NEW.start_pixel <= existing.start_pixel + existing.pixel_count - 1
      AND existing.start_pixel <= NEW.start_pixel + NEW.pixel_count - 1
)
BEGIN
    SELECT RAISE(ABORT, 'drawer LED ranges overlap on controller');
END;

COMMIT;

PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- Reverse M6. The rollback is refused as a whole (inside the shared Goose
-- statement) while either
--   * a membership exists that is not the legacy primary pointer
--     (cabinet_id, cabinets.controller_id), i.e. a secondary controller
--     assignment would be lost by dropping the table; or
--   * any drawer_led_allocations row exists, because reverting the allocation
--     foreign key cannot be proven lossless and would discard configured
--     electrical ranges.
-- The guard trips before any pragma or transaction starts, so a refusal leaves
-- the database untouched (its only residue is an empty, connection-local TEMP
-- table). The cleanup is qualified to TEMP so it cannot remove a main database
-- table with the same name. When neither condition holds,
-- drawer_led_allocations is rebuilt back onto cabinets(id, controller_id) with
-- its high-water mark and the additive membership table is dropped.
-- +goose StatementBegin
DROP TABLE IF EXISTS temp._cabinet_controllers_rollback_guard;
CREATE TEMP TABLE _cabinet_controllers_rollback_guard (
    ok INTEGER NOT NULL CHECK (ok = 0)
);
INSERT INTO _cabinet_controllers_rollback_guard (ok)
SELECT 1
WHERE EXISTS (
    SELECT 1 FROM cabinet_controllers cc
    WHERE NOT EXISTS (
        SELECT 1 FROM cabinets c
        WHERE c.id = cc.cabinet_id
          AND c.controller_id = cc.controller_id
    )
)
   OR EXISTS (SELECT 1 FROM drawer_led_allocations);
DROP TABLE _cabinet_controllers_rollback_guard;

PRAGMA foreign_keys = OFF;

BEGIN;

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
    FOREIGN KEY (cabinet_id, controller_id) REFERENCES cabinets(id, controller_id) ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (controller_id) REFERENCES controllers(id) ON DELETE RESTRICT
);

INSERT INTO drawer_led_allocations_rebuild
    (id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at)
SELECT id, cabinet_id, drawer_id, controller_id, start_pixel, pixel_count, display_order, created_at, updated_at
FROM drawer_led_allocations;

-- Same high-water mark preservation as the Up migration.
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

DROP TABLE cabinet_controllers;

COMMIT;

PRAGMA foreign_keys = ON;
-- +goose StatementEnd
