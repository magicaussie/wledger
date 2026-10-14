-- name: GetAllPartDetails :many
SELECT * FROM part_details ORDER BY part_id;

-- name: CreatePartDetails :exec
INSERT INTO part_details (
    part_id, sku, brand, model, colour, material, finish, condition, notes,
    length_mm, width_mm, height_mm, depth_mm, dimension_unit, weight_g, weight_unit, purchased_from,
    supplier_product_code, purchase_date, purchase_price, currency, product_url, purchase_notes
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ClearPartDetails :exec
DELETE FROM part_details;

-- name: RestorePartDetails :exec
INSERT INTO part_details (
    part_id, sku, brand, model, colour, material, finish, condition, notes,
    length_mm, width_mm, height_mm, depth_mm, dimension_unit, weight_g, weight_unit, purchased_from,
    supplier_product_code, purchase_date, purchase_price, currency, product_url,
    purchase_notes, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
