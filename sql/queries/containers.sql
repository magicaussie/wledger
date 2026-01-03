-- name: RestoreContainer :exec
INSERT INTO containers (id, name, controller_id, segment_id, config_json, position_index, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: CreateContainer :one
INSERT INTO containers (name, controller_id, segment_id, config_json, position_index)
VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: GetContainer :one
SELECT * FROM containers WHERE id = ?;

-- name: GetContainersByController :many
SELECT * FROM containers WHERE controller_id = ? ORDER BY position_index ASC, id ASC;

-- name: UpdateContainerConfig :exec
UPDATE containers
SET name = ?, config_json = ?, segment_id = ?, position_index = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteContainer :exec
DELETE FROM containers WHERE id = ?;

-- name: GetAllContainers :many
SELECT * FROM containers ORDER BY name;
