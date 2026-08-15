-- name: CreateSupplierReference :one
INSERT INTO supplier_references (part_id, provider_key, provider_id, provider_url)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: GetSupplierReference :one
SELECT * FROM supplier_references WHERE id = ?;

-- name: GetSupplierReferencesByPart :many
SELECT * FROM supplier_references WHERE part_id = ? ORDER BY created_at DESC;

-- name: GetSupplierReferenceByProvider :one
SELECT * FROM supplier_references WHERE provider_key = ? AND provider_id = ?;

-- name: FindExistingPartByProviderRef :one
SELECT p.* FROM parts p
JOIN supplier_references sr ON p.id = sr.part_id
WHERE sr.provider_key = ? AND sr.provider_id = ?;

-- name: DeleteSupplierReference :exec
DELETE FROM supplier_references WHERE id = ?;

-- name: DeleteSupplierReferencesByPart :exec
DELETE FROM supplier_references WHERE part_id = ?;

-- name: GetAllSupplierReferences :many
SELECT * FROM supplier_references ORDER BY id;

-- name: ClearSupplierReferences :exec
DELETE FROM supplier_references;

-- name: RestoreSupplierReference :exec
INSERT INTO supplier_references (id, part_id, provider_key, provider_id, provider_url, created_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: GetRecentlyImportedParts :many
SELECT DISTINCT p.*, sr.provider_key, sr.created_at AS imported_at
FROM parts p
JOIN supplier_references sr ON p.id = sr.part_id
ORDER BY sr.created_at DESC
LIMIT ?;

-- name: GetPriceComparisonByPart :many
SELECT sr.provider_key, pp.min_quantity, pp.price, pp.currency, pp.includes_tax
FROM part_pricing pp
JOIN supplier_references sr ON pp.supplier_ref_id = sr.id
WHERE sr.part_id = ?
ORDER BY pp.min_quantity ASC, sr.provider_key;
