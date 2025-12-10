-- name: CreateBin :one
INSERT INTO bins (name, controller_id, led_index, width, grid_x, grid_y)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetBinsByController :many
SELECT * FROM bins WHERE controller_id = ?;

-- name: DeleteBinsByController :exec
DELETE FROM bins WHERE controller_id = ?;