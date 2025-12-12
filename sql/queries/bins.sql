-- name: CreateBin :one
INSERT INTO bins (name, controller_id, led_index, width, grid_x, grid_y)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetBin :one
SELECT * FROM bins WHERE id = ?;

-- name: GetBinsByController :many
SELECT * FROM bins 
WHERE controller_id = ? 
ORDER BY led_index ASC;

-- name: DeleteBinsByController :exec
DELETE FROM bins WHERE controller_id = ?;

-- name: UpsertBin :exec
INSERT INTO bins (name, controller_id, led_index, width)
VALUES (?, ?, ?, ?)
ON CONFLICT(controller_id, led_index) DO UPDATE SET
    name = excluded.name,
    width = excluded.width;

-- name: DeleteBinByLed :exec
DELETE FROM bins 
WHERE controller_id = ? AND led_index = ?;