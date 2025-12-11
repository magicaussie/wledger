-- name: CreatePart :one
INSERT INTO parts (
    name, description, part_number, manufacturer, supplier, 
    unit_cost, reorder_level, min_stock_threshold, 
    barcode_data, image_path
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
)
RETURNING id;

-- name: UpdatePart :exec
UPDATE parts SET 
    name = ?, description = ?, part_number = ?, manufacturer = ?, 
    supplier = ?, unit_cost = ?, reorder_level = ?, 
    min_stock_threshold = ?, barcode_data = ?, image_path = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: GetPart :one
SELECT * FROM parts WHERE id = ?;

-- name: DeletePart :exec
DELETE FROM parts WHERE id = ?;

-- name: ListParts :many
SELECT p.*, 
    (SELECT COALESCE(SUM(quantity), 0) FROM part_assignments pa WHERE pa.part_id = p.id) AS total_stock
FROM parts p
ORDER BY p.name ASC 
LIMIT ? OFFSET ?;

-- name: CountParts :one
SELECT COUNT(*) FROM parts;

-- name: SearchParts :many
SELECT p.*,
    (SELECT COALESCE(SUM(quantity), 0) FROM part_assignments pa WHERE pa.part_id = p.id) AS total_stock
FROM parts p
JOIN parts_fts ON p.id = parts_fts.rowid
WHERE parts_fts MATCH ? 
ORDER BY rank
LIMIT 50;

-- name: CreatePartAssignment :exec
INSERT INTO part_assignments (part_id, bin_id, quantity)
VALUES (?, ?, ?);

-- name: UpdatePartAssignmentQuantity :exec
UPDATE part_assignments 
SET quantity = quantity + ? 
WHERE part_id = ? AND bin_id = ?;

-- name: GetAssignmentID :one
SELECT id FROM part_assignments 
WHERE part_id = ? AND bin_id = ?;

-- name: UpdateBinQuantity :exec
UPDATE part_assignments 
SET quantity = ? 
WHERE id = ?;

-- name: GetPartAssignments :many
SELECT 
    pa.id, pa.quantity, pa.bin_id,
    b.name as bin_name,
    c.name as controller_name,
    c.id as controller_id,
    c.ip_address as controller_ip
FROM part_assignments pa
JOIN bins b ON pa.bin_id = b.id
LEFT JOIN controllers c ON b.controller_id = c.id
WHERE pa.part_id = ?
ORDER BY c.name, b.name;

-- name: GetTotalStock :one
SELECT COALESCE(SUM(quantity), 0) 
FROM part_assignments 
WHERE part_id = ?;

-- name: DeletePartAssignment :exec
DELETE FROM part_assignments 
WHERE part_id = ? AND bin_id = ?;