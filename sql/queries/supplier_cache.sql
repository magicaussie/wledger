-- name: GetSupplierCache :one
SELECT * FROM supplier_cache WHERE cache_key = ?;

-- name: UpsertSupplierCache :exec
INSERT INTO supplier_cache (cache_key, provider_key, data, expires_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(cache_key) DO UPDATE SET
    data = excluded.data,
    expires_at = excluded.expires_at,
    created_at = CURRENT_TIMESTAMP;

-- name: DeleteSupplierCache :exec
DELETE FROM supplier_cache WHERE cache_key = ?;

-- name: DeleteSupplierCacheByProvider :exec
DELETE FROM supplier_cache WHERE provider_key = ?;

-- name: DeleteExpiredSupplierCache :exec
DELETE FROM supplier_cache WHERE expires_at < CURRENT_TIMESTAMP;

-- name: GetAllSupplierCache :many
SELECT * FROM supplier_cache ORDER BY created_at DESC;
