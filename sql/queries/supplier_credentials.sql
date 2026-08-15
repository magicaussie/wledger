-- name: GetSupplierCredential :one
SELECT * FROM supplier_credentials WHERE provider_key = ?;

-- name: GetAllSupplierCredentials :many
SELECT * FROM supplier_credentials ORDER BY provider_key;

-- name: UpsertSupplierCredential :exec
INSERT INTO supplier_credentials (provider_key, api_key, api_secret, access_token, refresh_token, token_expires_at, is_active)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(provider_key) DO UPDATE SET
    api_key = COALESCE(excluded.api_key, api_key),
    api_secret = COALESCE(excluded.api_secret, api_secret),
    access_token = COALESCE(excluded.access_token, access_token),
    refresh_token = COALESCE(excluded.refresh_token, refresh_token),
    token_expires_at = COALESCE(excluded.token_expires_at, token_expires_at),
    is_active = excluded.is_active,
    updated_at = CURRENT_TIMESTAMP;

-- name: UpdateSupplierCredentialToken :exec
UPDATE supplier_credentials
SET access_token = ?, refresh_token = ?, token_expires_at = ?, updated_at = CURRENT_TIMESTAMP
WHERE provider_key = ?;

-- name: DeleteSupplierCredential :exec
DELETE FROM supplier_credentials WHERE provider_key = ?;

-- name: ClearSupplierCredentials :exec
DELETE FROM supplier_credentials;

-- name: RestoreSupplierCredential :exec
INSERT INTO supplier_credentials (id, provider_key, api_key, api_secret, access_token, refresh_token, token_expires_at, is_active, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
