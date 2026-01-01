-- name: RestoreController :exec
INSERT INTO controllers (id, name, ip_address, port, mac_address, is_online, led_count, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateController :one
INSERT INTO controllers (name, ip_address, port) 
VALUES (?, ?, ?)
RETURNING id, name, ip_address, is_online, created_at;

-- name: GetControllers :many
SELECT * FROM controllers ORDER BY name;

-- name: GetController :one
SELECT * FROM controllers WHERE id = ?;

-- name: DeleteController :exec
DELETE FROM controllers WHERE id = ?;

-- name: UpdateControllerStatus :exec
UPDATE controllers 
SET is_online = ?, led_count = ? 
WHERE id = ?;
