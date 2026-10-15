-- name: GetControllerOutputs :many
SELECT * FROM controller_outputs WHERE controller_id = ? ORDER BY display_order, id;

-- name: GetAllControllerOutputs :many
SELECT * FROM controller_outputs ORDER BY controller_id, display_order, id;

-- name: CreateControllerOutput :one
INSERT INTO controller_outputs (controller_id, display_order, name, pixel_count, direction, pattern, rows, columns, custom_order_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateControllerOutput :execrows
UPDATE controller_outputs
SET display_order = ?, name = ?, pixel_count = ?, direction = ?, pattern = ?, rows = ?, columns = ?, custom_order_json = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND controller_id = ?;

-- name: DeleteControllerOutput :execrows
DELETE FROM controller_outputs WHERE id = ? AND controller_id = ?;

-- name: RestoreControllerOutput :exec
INSERT INTO controller_outputs (id, controller_id, display_order, name, pixel_count, direction, pattern, rows, columns, custom_order_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ClearControllerOutputs :exec
DELETE FROM controller_outputs;
