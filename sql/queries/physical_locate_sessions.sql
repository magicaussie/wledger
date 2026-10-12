-- name: SavePhysicalLocateSession :execrows
INSERT INTO physical_locate_sessions (
    controller_id, controller_address, controller_port, session_id, drawer_id, bin_id, owner_id, state,
    ranges_json, commands_json, fingerprint, color, brightness,
    last_operation, last_operation_at, last_successful_operation, last_successful_at, last_error
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(controller_id) DO UPDATE SET
    controller_address = excluded.controller_address,
    controller_port = excluded.controller_port,
    session_id = excluded.session_id,
    drawer_id = excluded.drawer_id,
    bin_id = excluded.bin_id,
    owner_id = excluded.owner_id,
    state = excluded.state,
    ranges_json = excluded.ranges_json,
    commands_json = excluded.commands_json,
    fingerprint = excluded.fingerprint,
    color = excluded.color,
    brightness = excluded.brightness,
    last_operation = excluded.last_operation,
    last_operation_at = excluded.last_operation_at,
    last_successful_operation = excluded.last_successful_operation,
    last_successful_at = excluded.last_successful_at,
    last_error = excluded.last_error
WHERE physical_locate_sessions.session_id = excluded.session_id
   OR (physical_locate_sessions.state IN ('stopped', 'failed') AND excluded.state = 'activating');

-- name: ListPhysicalLocateSessions :many
SELECT * FROM physical_locate_sessions ORDER BY controller_id;
