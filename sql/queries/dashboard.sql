-- name: GetDashboardStats :one
SELECT
    COUNT(DISTINCT p.id) as total_parts,
    (SELECT COUNT(*) FROM controllers) as total_controllers,
    (SELECT COUNT(*) FROM controllers WHERE is_online = 1) as online_controllers,
    CAST(COALESCE(SUM(pa.quantity), 0) AS INTEGER) as total_items_in_stock,
    COUNT(DISTINCT CASE WHEN p.is_favorite = 1 THEN p.id END) as favorite_parts,
    COALESCE(SUM(pa.quantity * p.unit_cost), 0.0) as total_stock_value
FROM parts p
LEFT JOIN part_assignments pa ON p.id = pa.part_id;

-- name: GetDashboardGrid :many
SELECT 
    c.id as controller_id,
    c.name as controller_name,
    c.is_online,
    cont.id as container_id,
    cont.name as container_name,
    cont.segment_id,
    b.id as bin_id, 
    b.name as bin_name, 
    b.grid_x, 
    b.grid_y,
    p.id as part_id, 
    pa.quantity, 
    p.min_stock_threshold, 
    p.reorder_level
FROM bins b
JOIN containers cont ON b.container_id = cont.id
JOIN controllers c ON cont.controller_id = c.id
LEFT JOIN part_assignments pa ON b.id = pa.bin_id
LEFT JOIN parts p ON pa.part_id = p.id
WHERE b.grid_x IS NOT NULL AND b.grid_y IS NOT NULL
ORDER BY c.name ASC, cont.name ASC, b.grid_y ASC, b.grid_x ASC;

-- name: GetWallContainerBins :many
SELECT 
    wc.wall_id,
    wc.position_index,
    c.id as container_id,
    c.name as container_name,
    c.segment_id,
    c.config_json as container_config,
    ctrl.id as controller_id,
    ctrl.name as controller_name,
    ctrl.is_online,
    b.id as bin_id, 
    b.name as bin_name, 
    b.grid_x, 
    b.grid_y,
    p.id as part_id, 
    pa.quantity, 
    p.min_stock_threshold, 
    p.reorder_level
FROM wall_cards wc
JOIN containers c ON wc.container_id = c.id
JOIN controllers ctrl ON c.controller_id = ctrl.id
LEFT JOIN bins b ON c.id = b.container_id
LEFT JOIN part_assignments pa ON b.id = pa.bin_id
LEFT JOIN parts p ON pa.part_id = p.id
WHERE wc.wall_id = ?
ORDER BY wc.position_index, b.grid_y, b.grid_x;

-- name: GetAllWallContainerBins :many
SELECT 
    wc.wall_id,
    wc.position_index,
    c.id as container_id,
    c.name as container_name,
    c.segment_id,
    c.config_json as container_config,
    ctrl.id as controller_id,
    ctrl.name as controller_name,
    ctrl.is_online,
    b.id as bin_id, 
    b.name as bin_name, 
    b.grid_x, 
    b.grid_y,
    p.id as part_id, 
    pa.quantity, 
    p.min_stock_threshold, 
    p.reorder_level
FROM wall_cards wc
JOIN containers c ON wc.container_id = c.id
JOIN controllers ctrl ON c.controller_id = ctrl.id
LEFT JOIN bins b ON c.id = b.container_id
LEFT JOIN part_assignments pa ON b.id = pa.bin_id
LEFT JOIN parts p ON pa.part_id = p.id
ORDER BY wc.wall_id, wc.position_index, b.grid_y, b.grid_x;
