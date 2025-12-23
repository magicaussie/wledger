-- name: GetPart :one
SELECT * FROM parts WHERE id = ?;

-- name: ListParts :many
SELECT p.*, 
    CAST(COALESCE(SUM(pa.quantity), 0) AS INTEGER) as total_stock,
    (SELECT pa2.bin_id 
     FROM part_assignments pa2 
     WHERE pa2.part_id = p.id AND pa2.quantity > 0 
     ORDER BY pa2.quantity DESC, pa2.id ASC 
     LIMIT 1) as locate_bin_id,
    (SELECT b2.controller_id 
     FROM part_assignments pa2 
     JOIN bins b2 ON pa2.bin_id = b2.id
     WHERE pa2.part_id = p.id AND pa2.quantity > 0 
     ORDER BY pa2.quantity DESC, pa2.id ASC 
     LIMIT 1) as locate_controller_id
FROM parts p
LEFT JOIN part_assignments pa ON p.id = pa.part_id
GROUP BY p.id
ORDER BY p.name
LIMIT ? OFFSET ?;

-- name: SearchParts :many
SELECT p.*, 
    CAST(COALESCE(SUM(pa.quantity), 0) AS INTEGER) as total_stock,
    (SELECT pa2.bin_id 
     FROM part_assignments pa2 
     WHERE pa2.part_id = p.id AND pa2.quantity > 0 
     ORDER BY pa2.quantity DESC, pa2.id ASC 
     LIMIT 1) as locate_bin_id,
    (SELECT b2.controller_id 
     FROM part_assignments pa2 
     JOIN bins b2 ON pa2.bin_id = b2.id
     WHERE pa2.part_id = p.id AND pa2.quantity > 0 
     ORDER BY pa2.quantity DESC, pa2.id ASC 
     LIMIT 1) as locate_controller_id
FROM parts_fts fts
JOIN parts p ON fts.rowid = p.id
LEFT JOIN part_assignments pa ON p.id = pa.part_id
WHERE parts_fts MATCH ?
GROUP BY p.id, fts.rank
ORDER BY fts.rank
LIMIT ? OFFSET ?;

-- name: GetAllParts :many
SELECT * FROM parts ORDER BY id;

-- name: GetAllPartLinks :many
SELECT * FROM part_links ORDER BY id;

-- name: GetAllPartDocs :many
SELECT * FROM part_docs ORDER BY id;

-- name: GetAllPartAiPrompts :many
SELECT * FROM part_ai_prompts ORDER BY id;

-- name: RestorePart :exec
INSERT INTO parts (
    id, name, description, part_number, manufacturer, supplier, 
    unit_cost, reorder_level, min_stock_threshold, 
    image_path, barcode_data, is_favorite, tags, created_at, updated_at
) VALUES (
    ?, ?, ?, ?, ?, ?, 
    ?, ?, ?, 
    ?, ?, ?, ?, ?, ?
);

-- name: RestorePartLink :exec
INSERT INTO part_links (id, part_id, url, label) VALUES (?, ?, ?, ?);

-- name: RestorePartDoc :exec
INSERT INTO part_docs (id, part_id, file_path, file_name) VALUES (?, ?, ?, ?);

-- name: RestorePartAssignment :exec
INSERT INTO part_assignments (id, part_id, bin_id, quantity) VALUES (?, ?, ?, ?);

-- name: RestorePartAiPrompt :exec
INSERT INTO part_ai_prompts (id, part_id, prompt_text, ai_response, model_used, created_at) VALUES (?, ?, ?, ?, ?, ?);

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

-- name: UpdatePartLink :exec
UPDATE part_links SET url = ?, label = ? WHERE id = ?;

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
-- name: GetAllPartAssignments :many
SELECT * FROM part_assignments ORDER BY id;

-- name: GetPartAssignments :many
SELECT 
    pa.id, 
    pa.quantity, 
    b.id as bin_id, 
    b.name as bin_name,
    b.led_index,
    b.width,
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