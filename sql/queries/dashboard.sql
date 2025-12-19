-- name: GetDashboardStats :one
SELECT
    COUNT(DISTINCT p.id) as total_parts,
    (SELECT COUNT(*) FROM controllers) as total_controllers,
    (SELECT COUNT(*) FROM controllers WHERE is_online = 1) as online_controllers,
    CAST(COALESCE(SUM(pa.quantity), 0) AS INTEGER) as total_items_in_stock,
    COUNT(DISTINCT CASE WHEN p.is_favorite = 1 THEN p.id END) as favorite_parts
FROM parts p
LEFT JOIN part_assignments pa ON p.id = pa.part_id;

-- name: GetDashboardGrid :many
SELECT 
    c.id as controller_id,
    c.name as controller_name,
    b.id as bin_id, 
    b.name as bin_name, 
    b.grid_x, 
    b.grid_y,
    p.id as part_id, 
    pa.quantity, 
    p.min_stock_threshold, 
    p.reorder_level
FROM bins b
JOIN controllers c ON b.controller_id = c.id
LEFT JOIN part_assignments pa ON b.id = pa.bin_id
LEFT JOIN parts p ON pa.part_id = p.id
WHERE b.grid_x IS NOT NULL AND b.grid_y IS NOT NULL
ORDER BY c.name ASC, b.grid_y ASC, b.grid_x ASC;