-- name: GetDashboardStats :one
SELECT 
    (SELECT COUNT(*) FROM parts) as total_parts,
    (SELECT COUNT(*) FROM controllers) as total_controllers,
    (SELECT COUNT(*) FROM controllers WHERE is_online = 1) as online_controllers,
    -- CAST to INTEGER so sqlc generates 'int64' instead of 'interface{}'
    (SELECT CAST(COALESCE(SUM(quantity), 0) AS INTEGER) FROM part_assignments) as total_items_in_stock,
    (SELECT COUNT(*) FROM parts WHERE is_favorite = 1) as favorite_parts;