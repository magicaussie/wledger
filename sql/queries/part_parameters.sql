-- name: CreatePartParameter :one
INSERT INTO part_parameters (part_id, name, value_text, value_typ, value_min, value_max, unit, symbol, param_group)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetPartParameters :many
SELECT * FROM part_parameters WHERE part_id = ? ORDER BY param_group, name;

-- name: GetAllPartParameters :many
SELECT * FROM part_parameters ORDER BY id;

-- name: DeletePartParametersByPart :exec
DELETE FROM part_parameters WHERE part_id = ?;

-- name: DeletePartParameter :exec
DELETE FROM part_parameters WHERE id = ?;

-- name: ClearPartParameters :exec
DELETE FROM part_parameters;

-- name: RestorePartParameter :exec
INSERT INTO part_parameters (id, part_id, name, value_text, value_typ, value_min, value_max, unit, symbol, param_group)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
