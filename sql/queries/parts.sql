-- name: GetPart :one
SELECT * FROM parts WHERE id = ?;

-- name: ListParts :many
SELECT *, 
    (SELECT COALESCE(SUM(quantity), 0) FROM part_assignments WHERE part_id = parts.id) as total_stock 
FROM parts 
ORDER BY name 
LIMIT ? OFFSET ?;

-- name: SearchParts :many
SELECT p.*, 
    (SELECT COALESCE(SUM(quantity), 0) FROM part_assignments WHERE part_id = p.id) as total_stock 
FROM parts_fts 
JOIN parts p ON parts_fts.rowid = p.id 
WHERE parts_fts MATCH ? 
ORDER BY parts_fts.rank
LIMIT ? OFFSET ?;

-- name: CreatePart :one
INSERT INTO parts (
    name, description, part_number, manufacturer, supplier, 
    unit_cost, reorder_level, min_stock_threshold, 
    image_path, barcode_data
) VALUES (
    ?, ?, ?, ?, ?, 
    ?, ?, ?, 
    ?, ?
) RETURNING id;

-- name: UpdatePart :exec
UPDATE parts SET 
    name = ?, 
    description = ?, 
    part_number = ?, 
    manufacturer = ?, 
    supplier = ?, 
    unit_cost = ?, 
    reorder_level = ?, 
    min_stock_threshold = ?, 
    barcode_data = ?,
    image_path = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeletePart :exec
DELETE FROM parts WHERE id = ?;

-- LINKS
-- name: CreatePartLink :exec
INSERT INTO part_links (part_id, url, label) VALUES (?, ?, ?);

-- name: GetPartLinks :many
SELECT * FROM part_links WHERE part_id = ?;

-- name: DeletePartLink :exec
DELETE FROM part_links WHERE id = ?;

-- DOCUMENTS
-- name: CreatePartDoc :exec
INSERT INTO part_docs (part_id, file_path, file_name) VALUES (?, ?, ?);

-- name: GetPartDocs :many
SELECT * FROM part_docs WHERE part_id = ?;

-- name: GetPartDoc :one
SELECT * FROM part_docs WHERE id = ?;

-- name: DeletePartDoc :exec
DELETE FROM part_docs WHERE id = ?;

-- STOCK ASSIGNMENTS
-- name: GetPartAssignments :many
SELECT 
    pa.id, 
    pa.quantity, 
    b.id as bin_id, 
    b.name as bin_name,
    c.name as controller_name,
    c.id as controller_id,
    c.ip_address as controller_ip
FROM part_assignments pa
LEFT JOIN bins b ON pa.bin_id = b.id
LEFT JOIN controllers c ON b.controller_id = c.id
WHERE pa.part_id = ?;

-- name: GetAssignment :one
SELECT * FROM part_assignments WHERE id = ?;

-- name: GetAssignmentID :one
SELECT id FROM part_assignments WHERE part_id = ? AND bin_id = ?;

-- name: CreatePartAssignment :exec
INSERT INTO part_assignments (part_id, bin_id, quantity) VALUES (?, ?, ?);

-- name: ReassignPartAssignment :exec
UPDATE part_assignments 
SET bin_id = ? 
WHERE id = ?;

-- name: UpdatePartAssignmentQuantity :exec
UPDATE part_assignments SET quantity = ? WHERE part_id = ? AND bin_id = ?;

-- name: DeletePartAssignment :exec
DELETE FROM part_assignments WHERE part_id = ? AND bin_id = ?;

-- name: DeleteAssignment :exec
DELETE FROM part_assignments WHERE id = ?;