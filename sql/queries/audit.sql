-- name: GetAllAuditLogs :many
SELECT * FROM audit_logs ORDER BY id;

-- name: RestoreAuditLog :exec
INSERT INTO audit_logs (
    id, user_id, action_type, entity_type, entity_id, details, old_value, new_value, created_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?
);

-- name: CreateAuditLog :exec
INSERT INTO audit_logs (
    user_id, action_type, entity_type, entity_id, details, old_value, new_value
) VALUES (
    ?, ?, ?, ?, ?, ?, ?
);