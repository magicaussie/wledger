-- name: RestoreController :exec
INSERT INTO controllers (id, name, ip_address, port, mac_address, is_online, led_count, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateController :one
INSERT INTO controllers (name, ip_address, port)
VALUES (?, ?, ?)
RETURNING id, name, ip_address, is_online, created_at;

-- name: GetControllers :many
SELECT * FROM controllers ORDER BY name;

-- name: GetControllersForCompatibility :many
SELECT id, name, ip_address, port, mac_address, is_online, led_count, created_at
FROM controllers ORDER BY name;

-- name: GetController :one
SELECT * FROM controllers WHERE id = ?;

-- name: GetControllerByIP :one
SELECT * FROM controllers WHERE ip_address = ?;

-- name: UpdateControllerSettings :execrows
UPDATE controllers
SET name = ?, ip_address = ?, port = ?
WHERE id = ?;

-- name: DeleteController :exec
DELETE FROM controllers WHERE id = ?;

-- name: UpdateControllerStatus :exec
UPDATE controllers
SET is_online = ?, led_count = ?
WHERE id = ?;

-- name: SetControllerOnlineStatus :exec
UPDATE controllers
SET is_online = ?
WHERE id = ?;

-- name: UpdateControllerLedCount :execrows
UPDATE controllers
SET led_count = ?
WHERE id = ? AND led_count = ?;

-- name: UpdateControllerLocateMappingVerification :execrows
UPDATE controllers
SET locate_mapping_verified = ?, locate_mapping_fingerprint = ?
WHERE id = ?;
