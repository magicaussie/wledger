-- name: GetAllCategories :many
SELECT * FROM categories ORDER BY id;

-- name: GetAllPartCategories :many
SELECT part_id, category_id FROM part_categories ORDER BY part_id;

-- name: GetAllCategoryAttributes :many
SELECT * FROM category_attributes ORDER BY id;

-- name: GetAllPartAttributeValues :many
SELECT * FROM part_attribute_values ORDER BY part_id, attribute_id;

-- name: ClearPartAttributeValues :exec
DELETE FROM part_attribute_values;

-- name: ClearPartCategories :exec
DELETE FROM part_categories;

-- name: ClearCategoryAttributes :exec
DELETE FROM category_attributes;

-- name: ClearCategories :exec
DELETE FROM categories;

-- name: RestoreCategory :exec
INSERT INTO categories (id, name, description, image_path, parent_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: RestoreCategoryParent :exec
UPDATE categories SET parent_id = ? WHERE id = ?;

-- name: RestorePartCategory :exec
INSERT INTO part_categories (part_id, category_id) VALUES (?, ?);

-- name: RestoreCategoryAttribute :exec
INSERT INTO category_attributes (id, category_id, name, data_type, unit, options, min_value, max_value, is_required, display_order, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: RestorePartAttributeValue :exec
INSERT INTO part_attribute_values (part_id, attribute_id, value_text, value_number, value_boolean, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: CreatePartCategory :exec
INSERT INTO part_categories (part_id, category_id) VALUES (?, ?);

-- name: CreatePartAttributeValue :exec
INSERT INTO part_attribute_values (part_id, attribute_id, value_text, value_number, value_boolean)
VALUES (?, ?, ?, ?, ?);
