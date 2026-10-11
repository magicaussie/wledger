-- name: GetFlag :one
SELECT value FROM system_flags WHERE key = ?;

-- name: SetFlag :exec
INSERT INTO system_flags (key, value, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(key) DO UPDATE SET
    value = excluded.value,
    updated_at = excluded.updated_at;

-- GetAllSystemFlags captures every flag, with no key filter, so a backup is
-- complete and a restore can reproduce the exact flag set (including the
-- absence of a key).
-- name: GetAllSystemFlags :many
SELECT key, value, updated_at FROM system_flags
ORDER BY key ASC;

-- DeleteAllSystemFlags removes every flag so a current-format restore can
-- replace the flag set exactly instead of upserting onto the old one.
-- name: DeleteAllSystemFlags :exec
DELETE FROM system_flags;

-- RestoreSystemFlag restores a flag with its captured timestamp so the flag set
-- round-trips exactly.
-- name: RestoreSystemFlag :exec
INSERT INTO system_flags (key, value, updated_at)
VALUES (?, ?, ?);
