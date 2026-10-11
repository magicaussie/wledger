-- +goose Up
PRAGMA foreign_keys = ON;

-- Physical storage foundation (M2-M4). New empty tables only; the legacy
-- controllers/containers/bins/parts/part_assignments/flags/walls graph is not
-- rewritten, recreated or backfilled.
--
-- Attachment geometry is new physical-layout truth and never reuses the legacy
-- bins.grid_x / bins.grid_y / bins.width / bins.container_id columns, which
-- keep their historical electrical/visual meaning.
--
-- Every foreign key is ON DELETE RESTRICT: deleting a cabinet, drawer, bin or
-- controller that still has an attached row is blocked, never cascaded.

CREATE TABLE IF NOT EXISTS cabinets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    controller_id INTEGER,
    coordinate_system TEXT NOT NULL DEFAULT 'grid',
    layout_revision INTEGER NOT NULL DEFAULT 0 CHECK (layout_revision >= 0),
    electrical_revision INTEGER NOT NULL DEFAULT 0 CHECK (electrical_revision >= 0),
    display_order INTEGER NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (controller_id) REFERENCES controllers(id) ON DELETE RESTRICT,
    UNIQUE (controller_id),
    UNIQUE (id, controller_id)
);
CREATE INDEX IF NOT EXISTS idx_cabinets_controller_id ON cabinets(controller_id);

-- Drawer geometry is cabinet-local: x/y are a non-negative origin, width/height
-- strictly positive. UNIQUE (id, cabinet_id) gives later scopes a drawer key
-- that carries its owning cabinet.
CREATE TABLE IF NOT EXISTS drawers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    cabinet_id INTEGER NOT NULL,
    label TEXT NOT NULL CHECK (length(trim(label)) > 0),
    display_order INTEGER NOT NULL DEFAULT 0,
    x REAL NOT NULL CHECK (x >= 0),
    y REAL NOT NULL CHECK (y >= 0),
    width REAL NOT NULL CHECK (width > 0),
    height REAL NOT NULL CHECK (height > 0),
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0, 1)),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (cabinet_id) REFERENCES cabinets(id) ON DELETE RESTRICT,
    UNIQUE (id, cabinet_id)
);
CREATE INDEX IF NOT EXISTS idx_drawers_cabinet_id ON drawers(cabinet_id);

-- One attachment per bin (bin_id is the primary key); geometry is this table's
-- own physical placement, independent of the legacy bin columns.
CREATE TABLE IF NOT EXISTS bin_drawer_attachments (
    bin_id INTEGER PRIMARY KEY,
    drawer_id INTEGER NOT NULL,
    x REAL NOT NULL CHECK (x >= 0),
    y REAL NOT NULL CHECK (y >= 0),
    width REAL NOT NULL CHECK (width > 0),
    height REAL NOT NULL CHECK (height > 0),
    display_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (bin_id) REFERENCES bins(id) ON DELETE RESTRICT,
    FOREIGN KEY (drawer_id) REFERENCES drawers(id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS idx_bin_drawer_attachments_drawer_id ON bin_drawer_attachments(drawer_id);

-- +goose Down
-- The guard and the drops share one Goose transaction: if any new table holds
-- rows the CHECK trips and the whole Down aborts, leaving every table intact.
-- When empty, only the new tables are dropped, in reverse dependency order.
CREATE TEMP TABLE _physical_storage_rollback_guard (
    ok INTEGER NOT NULL CHECK (ok = 0)
);
INSERT INTO _physical_storage_rollback_guard (ok)
SELECT 1
WHERE EXISTS (SELECT 1 FROM cabinets)
   OR EXISTS (SELECT 1 FROM drawers)
   OR EXISTS (SELECT 1 FROM bin_drawer_attachments);
DROP TABLE _physical_storage_rollback_guard;

DROP TABLE IF EXISTS bin_drawer_attachments;
DROP TABLE IF EXISTS drawers;
DROP TABLE IF EXISTS cabinets;
