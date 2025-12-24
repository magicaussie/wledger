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

-- name: ListAuditLogs :many

SELECT 
    al.id, 
    al.user_id, 
    al.action_type, 
    al.entity_type, 
    al.entity_id, 
    al.details, 
    CAST(COALESCE(al.old_value, '{}') AS BLOB) AS old_value, 
    CAST(COALESCE(al.new_value, '{}') AS BLOB) AS new_value, 
    al.created_at,
    u.email as user_email
FROM audit_logs al
LEFT JOIN users u ON al.user_id = u.id
WHERE 
    (al.action_type = sqlc.narg('action_type') OR sqlc.narg('action_type') IS NULL) AND
    (al.entity_type = sqlc.narg('entity_type') OR sqlc.narg('entity_type') IS NULL) AND
    (al.user_id = sqlc.narg('user_id') OR sqlc.narg('user_id') IS NULL) AND
    (al.details LIKE '%' || sqlc.narg('search') || '%' OR sqlc.narg('search') IS NULL) AND
    (al.created_at >= sqlc.narg('start_date') OR sqlc.narg('start_date') IS NULL) AND
    (al.created_at <= sqlc.narg('end_date') OR sqlc.narg('end_date') IS NULL)
ORDER BY al.created_at DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountAuditLogs :one

SELECT COUNT(*) FROM audit_logs al
WHERE 
    (al.action_type = sqlc.narg('action_type') OR sqlc.narg('action_type') IS NULL) AND
    (al.entity_type = sqlc.narg('entity_type') OR sqlc.narg('entity_type') IS NULL) AND
    (al.user_id = sqlc.narg('user_id') OR sqlc.narg('user_id') IS NULL) AND
    (al.details LIKE '%' || sqlc.narg('search') || '%' OR sqlc.narg('search') IS NULL) AND
    (al.created_at >= sqlc.narg('start_date') OR sqlc.narg('start_date') IS NULL) AND
    (al.created_at <= sqlc.narg('end_date') OR sqlc.narg('end_date') IS NULL);