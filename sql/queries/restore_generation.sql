-- name: GetRestoreGeneration :one
SELECT generation_id FROM restore_generation WHERE id = 1;

-- name: SetRestoreGeneration :exec
UPDATE restore_generation
SET generation_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = 1;
