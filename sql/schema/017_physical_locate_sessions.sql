-- +goose Up
-- Runtime ownership record for application-originated pixel overrides. These
-- rows are deliberately independent of inventory/layout FKs so a stale mapping
-- cannot erase the cleanup evidence needed after a restart or restore.
CREATE TABLE physical_locate_sessions (
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
    ON physical_locate_sessions((1))
    WHERE state IN ('activating', 'active', 'partial', 'stopping');

-- +goose Down
-- Do not discard the only recovery record while pixels may still be overridden.
CREATE TEMP TABLE _physical_locate_rollback_guard (ok INTEGER NOT NULL CHECK (ok = 0));
INSERT INTO _physical_locate_rollback_guard (ok)
SELECT 1 WHERE EXISTS (
    SELECT 1 FROM physical_locate_sessions
    WHERE state IN ('activating', 'active', 'partial', 'stopping')
);
DROP TABLE _physical_locate_rollback_guard;
DROP INDEX physical_locate_single_active_session;
DROP TABLE physical_locate_sessions;
