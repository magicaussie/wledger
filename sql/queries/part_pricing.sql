-- name: CreatePartPricing :one
INSERT INTO part_pricing (part_id, supplier_ref_id, min_quantity, price, currency, includes_tax)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetPartPricing :many
SELECT pp.*, sr.provider_key, sr.provider_id
FROM part_pricing pp
JOIN supplier_references sr ON pp.supplier_ref_id = sr.id
WHERE pp.part_id = ?
ORDER BY pp.min_quantity ASC;

-- name: GetPartPricingBySupplierRef :many
SELECT * FROM part_pricing WHERE supplier_ref_id = ? ORDER BY min_quantity ASC;

-- name: GetAllPartPricing :many
SELECT pp.*, sr.provider_key, sr.provider_id
FROM part_pricing pp
JOIN supplier_references sr ON pp.supplier_ref_id = sr.id
ORDER BY pp.part_id, pp.min_quantity;

-- name: DeletePartPricingByPart :exec
DELETE FROM part_pricing WHERE part_id = ?;

-- name: DeletePartPricingBySupplierRef :exec
DELETE FROM part_pricing WHERE supplier_ref_id = ?;

-- name: DeletePartPricing :exec
DELETE FROM part_pricing WHERE id = ?;

-- name: ClearPartPricing :exec
DELETE FROM part_pricing;

-- name: RestorePartPricing :exec
INSERT INTO part_pricing (id, part_id, supplier_ref_id, min_quantity, price, currency, includes_tax)
VALUES (?, ?, ?, ?, ?, ?, ?);
