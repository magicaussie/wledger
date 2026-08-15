-- name: CreatePriceHistory :one
INSERT INTO price_history (part_id, supplier_ref_id, min_quantity, price, currency, includes_tax)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetPriceHistoryByPart :many
SELECT ph.*, sr.provider_key
FROM price_history ph
JOIN supplier_references sr ON ph.supplier_ref_id = sr.id
WHERE ph.part_id = ?
ORDER BY ph.recorded_at DESC;

-- name: GetLatestPriceSnapshot :many
SELECT ph.*, sr.provider_key
FROM price_history ph
JOIN supplier_references sr ON ph.supplier_ref_id = sr.id
WHERE ph.part_id = ?
AND ph.recorded_at = (
    SELECT MAX(ph2.recorded_at)
    FROM price_history ph2
    WHERE ph2.part_id = ph.part_id
    AND ph2.supplier_ref_id = ph.supplier_ref_id
)
ORDER BY sr.provider_key, ph.min_quantity;

-- name: GetPriceHistoryByPartAndProvider :many
SELECT *
FROM price_history ph
JOIN supplier_references sr ON ph.supplier_ref_id = sr.id
WHERE ph.part_id = ?
AND sr.provider_key = ?
ORDER BY ph.recorded_at ASC;

-- name: GetAllPriceHistory :many
SELECT * FROM price_history ORDER BY id;

-- name: ClearPriceHistory :exec
DELETE FROM price_history;

-- name: RestorePriceHistory :exec
INSERT INTO price_history (id, part_id, supplier_ref_id, min_quantity, price, currency, includes_tax, recorded_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);
