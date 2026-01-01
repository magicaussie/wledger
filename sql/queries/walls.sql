-- name: CreateWall :one
INSERT INTO walls (name, description) VALUES (?, ?) RETURNING id;

-- name: GetWalls :many
SELECT * FROM walls ORDER BY name;

-- name: GetWall :one
SELECT * FROM walls WHERE id = ?;

-- name: UpdateWall :exec
UPDATE walls SET name = ?, description = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?;

-- name: DeleteWall :exec
DELETE FROM walls WHERE id = ?;

-- name: AddContainerToWall :exec
INSERT INTO wall_cards (wall_id, container_id, position_index, config_json) VALUES (?, ?, ?, ?);

-- name: GetWallCards :many
SELECT wc.*, c.name as container_name
FROM wall_cards wc
JOIN containers c ON wc.container_id = c.id
WHERE wc.wall_id = ?
ORDER BY wc.position_index;

-- name: RemoveContainerFromWall :exec
DELETE FROM wall_cards WHERE wall_id = ? AND container_id = ?;

-- name: UpdateWallCardPosition :exec
UPDATE wall_cards SET position_index = ? WHERE id = ?;

-- name: DeleteWallCardsByWallID :exec
DELETE FROM wall_cards WHERE wall_id = ?;
