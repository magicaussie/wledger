-- name: CreateAuditLog :exec
INSERT INTO audit_logs (
    user_id, action_type, entity_type, entity_id, details, old_value, new_value
) VALUES (
    ?, ?, ?, ?, ?, ?, ?
);