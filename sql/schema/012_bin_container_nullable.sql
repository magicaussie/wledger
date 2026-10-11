-- +goose NO TRANSACTION
-- +goose Up
-- M5 (stable bin identity, schema step): bins.container_id becomes optional.
--
-- A bin may now exist without an electrical container while every existing
-- column, row id, foreign key, unique constraint and index is preserved. No
-- physical drawer attachment is inferred or backfilled and no electrical
-- mapping is touched: part_assignments and bin_drawer_attachments keep
-- referencing the same bin ids, and containers keep their segments and LED
-- allocation.
--
-- SQLite cannot relax a NOT NULL constraint in place, so the documented
-- create/copy/drop/rename rebuild is used. Foreign key enforcement MUST be off
-- around that rebuild: with it on, DROP TABLE bins performs an implicit DELETE
-- that would silently null out part_assignments.bin_id (ON DELETE SET NULL) and
-- would be refused outright by bin_drawer_attachments (ON DELETE RESTRICT).
-- PRAGMA foreign_keys is a no-op inside a transaction, so this migration opts
-- out of Goose's transaction and runs the rebuild inside its own explicit one.
-- StatementBegin keeps the whole body in a single statement, so the pragma, the
-- transaction and the rebuild can never be split across pooled connections
-- (Goose executes each statement of a NO TRANSACTION migration separately).
--
-- The rebuild deliberately runs no global foreign_key_check: the legacy
-- database is known to carry pre-existing dangling references, and M5 relaxes
-- NOT NULL only. Invalid data is reported, never repaired or dropped.
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE bins_rebuild (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    container_id INTEGER,
    led_index INTEGER,
    width INTEGER DEFAULT 1,
    grid_x INTEGER,
    grid_y INTEGER,
    FOREIGN KEY(container_id) REFERENCES containers(id) ON DELETE CASCADE,
    UNIQUE(container_id, led_index)
);

INSERT INTO bins_rebuild (id, name, container_id, led_index, width, grid_x, grid_y)
SELECT id, name, container_id, led_index, width, grid_x, grid_y
FROM bins;

-- Carry the old AUTOINCREMENT high-water mark onto the rebuilt table so that an
-- id belonging to an already deleted bin is never handed out again: printed bin
-- labels keep their identity and can never resolve to a different bin.
DELETE FROM sqlite_sequence
 WHERE name = 'bins_rebuild'
   AND (SELECT seq FROM sqlite_sequence WHERE name = 'bins') > seq;
INSERT INTO sqlite_sequence (name, seq)
SELECT 'bins_rebuild', (SELECT seq FROM sqlite_sequence WHERE name = 'bins')
 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'bins_rebuild')
   AND (SELECT seq FROM sqlite_sequence WHERE name = 'bins') IS NOT NULL;

DROP TABLE bins;

ALTER TABLE bins_rebuild RENAME TO bins;

CREATE INDEX idx_bins_container_id ON bins(container_id);

COMMIT;

PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- Restore the pre-M5 NOT NULL schema. Rows are copied verbatim, so the rollback
-- refuses as a whole while any bin still has a NULL container_id: the old
-- schema cannot represent those rows and a partial rewrite would lose them. The
-- guard trips before any pragma or transaction starts, so a refusal leaves the
-- database untouched (its only residue is an empty, connection-local TEMP
-- table). The guard cleanup is qualified to TEMP so it cannot remove a main
-- database table with the same name. Rows, ids, quantities, attachments and
-- flags are preserved when the rollback is possible.
-- +goose StatementBegin
DROP TABLE IF EXISTS temp._bin_decoupling_rollback_guard;
CREATE TEMP TABLE _bin_decoupling_rollback_guard (
    ok INTEGER NOT NULL CHECK (ok = 0)
);
INSERT INTO _bin_decoupling_rollback_guard (ok)
SELECT 1 WHERE EXISTS (SELECT 1 FROM bins WHERE container_id IS NULL);
DROP TABLE _bin_decoupling_rollback_guard;

PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE bins_rebuild (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    container_id INTEGER NOT NULL,
    led_index INTEGER,
    width INTEGER DEFAULT 1,
    grid_x INTEGER,
    grid_y INTEGER,
    FOREIGN KEY(container_id) REFERENCES containers(id) ON DELETE CASCADE,
    UNIQUE(container_id, led_index)
);

INSERT INTO bins_rebuild (id, name, container_id, led_index, width, grid_x, grid_y)
SELECT id, name, container_id, led_index, width, grid_x, grid_y
FROM bins;

-- Same high-water mark preservation as the Up migration.
DELETE FROM sqlite_sequence
 WHERE name = 'bins_rebuild'
   AND (SELECT seq FROM sqlite_sequence WHERE name = 'bins') > seq;
INSERT INTO sqlite_sequence (name, seq)
SELECT 'bins_rebuild', (SELECT seq FROM sqlite_sequence WHERE name = 'bins')
 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'bins_rebuild')
   AND (SELECT seq FROM sqlite_sequence WHERE name = 'bins') IS NOT NULL;

DROP TABLE bins;

ALTER TABLE bins_rebuild RENAME TO bins;

CREATE INDEX idx_bins_container_id ON bins(container_id);

COMMIT;

PRAGMA foreign_keys = ON;
-- +goose StatementEnd
