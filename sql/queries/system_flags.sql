-- name: GetFlag :one
SELECT value FROM system_flags WHERE key = ?;

-- name: SetFlag :exec
INSERT INTO system_flags (key, value, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(key) DO UPDATE SET
    value = excluded.value,
    updated_at = excluded.updated_at;
