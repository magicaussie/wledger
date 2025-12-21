-- name: GetAllInspirationTemplates :many
SELECT * FROM inspiration_templates ORDER BY id;

-- name: GetInspirationTemplate :one
SELECT * FROM inspiration_templates WHERE id = ?;

-- name: CreateInspirationTemplate :one
INSERT INTO inspiration_templates (title, template_content) VALUES (?, ?) RETURNING *;

-- name: UpdateInspirationTemplate :exec
UPDATE inspiration_templates SET title = ?, template_content = ? WHERE id = ?;

-- name: DeleteInspirationTemplate :exec
DELETE FROM inspiration_templates WHERE id = ?;

-- name: GetPartsForInspirationAll :many
SELECT 
    p.name,
    p.part_number,
    CAST(COALESCE(SUM(pa.quantity), 0) AS INTEGER) as total_quantity
FROM parts p
LEFT JOIN part_assignments pa ON p.id = pa.part_id
GROUP BY p.id
HAVING total_quantity > 0
ORDER BY p.name;

-- name: GetPartsForInspirationFiltered :many
SELECT DISTINCT
    p.name,
    p.part_number,
    CAST(COALESCE(SUM(pa.quantity), 0) AS INTEGER) as total_quantity
FROM parts p
LEFT JOIN part_assignments pa ON p.id = pa.part_id
JOIN part_tags pt ON p.id = pt.part_id
JOIN tags t ON pt.tag_id = t.id
WHERE t.name IN (sqlc.slice('tags'))
GROUP BY p.id
HAVING total_quantity > 0
ORDER BY p.name;