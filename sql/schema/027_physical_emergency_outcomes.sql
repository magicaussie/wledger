-- +goose Up
-- Independent of controllers and sessions: deleting configuration or completing
-- an operation must not erase an unacknowledged emergency delivery.
ALTER TABLE physical_emergency_state ADD COLUMN emergency_id TEXT NOT NULL DEFAULT '';
CREATE TABLE physical_emergency_outcomes (
    controller_id INTEGER NOT NULL,
    controller_address TEXT NOT NULL,
    controller_port INTEGER NOT NULL,
    emergency_id TEXT NOT NULL,
    attempted INTEGER NOT NULL CHECK (attempted IN (0,1)),
    acknowledged INTEGER NOT NULL CHECK (acknowledged IN (0,1)),
    error TEXT NOT NULL DEFAULT '',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (acknowledged = 0 OR attempted = 1),
    PRIMARY KEY (controller_id, controller_address, controller_port)
);

-- +goose Down
-- Do not roll back the evidence for an interrupted or failed emergency.
-- +goose StatementBegin
DROP TABLE IF EXISTS temp._physical_emergency_down_guard;
CREATE TEMP TABLE _physical_emergency_down_guard (ok INTEGER CHECK(ok=0));
INSERT INTO _physical_emergency_down_guard
SELECT 1 WHERE EXISTS (SELECT 1 FROM physical_emergency_state WHERE active=1)
 OR EXISTS (SELECT 1 FROM physical_emergency_outcomes);
DROP TABLE _physical_emergency_down_guard;
DROP TABLE physical_emergency_outcomes;
ALTER TABLE physical_emergency_state DROP COLUMN emergency_id;
-- +goose StatementEnd
