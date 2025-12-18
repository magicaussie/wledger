-- name: GetDashboardStats :one
SELECT 
    (SELECT COUNT(*) FROM parts) as total_parts,
    (SELECT COUNT(*) FROM controllers) as total_controllers,
    (SELECT COUNT(*) FROM controllers WHERE is_online = 1) as online_controllers,
    -- CAST to INTEGER so sqlc generates 'int64' instead of 'interface{}'
    (SELECT CAST(COALESCE(SUM(quantity), 0) AS INTEGER) FROM part_assignments) as total_items_in_stock,
    (SELECT COUNT(*) FROM parts WHERE is_favorite = 1) as favorite_parts;

-- name: GetDashboardGrid :many
SELECT 
    b.id as bin_id, 
    b.name as bin_name, 
    b.grid_x, 
    b.grid_y,
    p.id as part_id, 
    pa.quantity, 
    p.min_stock_threshold, 
    p.reorder_level
FROM bins b
LEFT JOIN part_assignments pa ON b.id = pa.bin_id
LEFT JOIN parts p ON pa.part_id = p.id
WHERE b.grid_x IS NOT NULL AND b.grid_y IS NOT NULL AND b.controller_id IS NOT NULL
ORDER BY b.grid_y, b.grid_x;