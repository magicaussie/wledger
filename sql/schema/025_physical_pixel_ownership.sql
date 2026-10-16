-- +goose NO TRANSACTION
-- +goose Up
-- Rebuild the parent without altering or cascading its recovery targets.
-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE physical_locate_sessions_new (
 controller_id INTEGER NOT NULL,
 controller_address TEXT NOT NULL,
 controller_port INTEGER NOT NULL,
 session_id TEXT PRIMARY KEY NOT NULL,
 drawer_id INTEGER NOT NULL,
 bin_id INTEGER NOT NULL,
 owner_id INTEGER NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('activating','active','partial','stopping','stopped','failed')),
 ranges_json TEXT NOT NULL, commands_json TEXT NOT NULL, fingerprint TEXT NOT NULL,
 color TEXT NOT NULL, brightness INTEGER NOT NULL,
 last_operation TEXT NOT NULL, last_operation_at DATETIME NOT NULL,
 last_successful_operation TEXT NOT NULL DEFAULT '', last_successful_at TEXT NOT NULL DEFAULT '',
 last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO physical_locate_sessions_new SELECT * FROM physical_locate_sessions;
DROP TABLE physical_locate_sessions;
ALTER TABLE physical_locate_sessions_new RENAME TO physical_locate_sessions;
CREATE INDEX physical_locate_controller_sessions ON physical_locate_sessions(controller_id);
CREATE TABLE physical_emergency_state (id INTEGER PRIMARY KEY CHECK(id=1), active INTEGER NOT NULL CHECK(active IN (0,1)));
INSERT INTO physical_emergency_state VALUES(1,0);
CREATE TABLE physical_operation_metadata (
 session_id TEXT PRIMARY KEY REFERENCES physical_locate_sessions(session_id),
 function TEXT NOT NULL, duration_ns INTEGER NOT NULL, expires_at DATETIME NOT NULL
);
CREATE TABLE physical_pixel_ownership (
 session_id TEXT NOT NULL REFERENCES physical_locate_sessions(session_id),
 controller_id INTEGER NOT NULL,
 mac TEXT NOT NULL, output TEXT NOT NULL,
 start_pixel INTEGER NOT NULL, stop_pixel INTEGER NOT NULL,
 CHECK (start_pixel >= 0 AND stop_pixel > start_pixel),
 PRIMARY KEY(session_id, controller_id, mac, output, start_pixel, stop_pixel)
);
CREATE INDEX physical_pixel_identity ON physical_pixel_ownership(mac, output, start_pixel, stop_pixel);
COMMIT;
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- Refuse to lose new operation metadata or collapse concurrent history.
-- +goose StatementBegin
DROP TABLE IF EXISTS temp._physical_ownership_down_guard;
CREATE TEMP TABLE _physical_ownership_down_guard (ok INTEGER CHECK(ok=0));
INSERT INTO _physical_ownership_down_guard
SELECT 1 WHERE EXISTS (SELECT 1 FROM physical_emergency_state WHERE active=1)
 OR EXISTS (SELECT 1 FROM physical_operation_metadata)
 OR EXISTS (SELECT 1 FROM physical_pixel_ownership)
 OR EXISTS (SELECT controller_id FROM physical_locate_sessions GROUP BY controller_id HAVING COUNT(*)>1);
DROP TABLE _physical_ownership_down_guard;
PRAGMA foreign_keys=OFF;
BEGIN;
DROP TABLE physical_emergency_state;
DROP TABLE physical_pixel_ownership;
DROP TABLE physical_operation_metadata;
CREATE TABLE physical_locate_sessions_old (
    controller_id INTEGER PRIMARY KEY,
    controller_address TEXT NOT NULL,
    controller_port INTEGER NOT NULL,
    session_id TEXT NOT NULL UNIQUE,
    drawer_id INTEGER NOT NULL,
    bin_id INTEGER NOT NULL,
    owner_id INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('activating', 'active', 'partial', 'stopping', 'stopped', 'failed')),
    ranges_json TEXT NOT NULL,
    commands_json TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    color TEXT NOT NULL,
    brightness INTEGER NOT NULL,
    last_operation TEXT NOT NULL,
    last_operation_at DATETIME NOT NULL,
    last_successful_operation TEXT NOT NULL DEFAULT '',
    last_successful_at TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX physical_locate_single_active_session
    ON physical_locate_sessions_old((1))
    WHERE state IN ('activating', 'active', 'partial', 'stopping');

INSERT INTO physical_locate_sessions_old SELECT * FROM physical_locate_sessions;
DROP TABLE physical_locate_sessions;
ALTER TABLE physical_locate_sessions_old RENAME TO physical_locate_sessions;
COMMIT;
PRAGMA foreign_keys=ON;
-- +goose StatementEnd
