-- name: RestoreSettings :exec
INSERT INTO settings (
    id, require_auth_for_read, locate_timeout_seconds, enable_locate_timeout, 
    color_locate, color_stock_ok, color_stock_low, color_stock_critical, 
    created_at, updated_at, enable_debug_logs
) VALUES (
    1, ?, ?, ?, 
    ?, ?, ?, ?, 
    ?, ?, ?
);

-- name: GetSettings :one
SELECT * FROM settings WHERE id = 1;

-- name: InitSettings :exec
INSERT OR IGNORE INTO settings (id, require_auth_for_read, color_locate, color_stock_ok, color_stock_low, color_stock_critical, locate_timeout_seconds, enable_locate_timeout, enable_debug_logs)
VALUES (1, 1, '#0000FF', '#00FF00', '#FFFF00', '#FF0000', 10, 1, 0);

-- name: UpdateGeneralSettings :exec
UPDATE settings 
SET require_auth_for_read = ?, locate_timeout_seconds = ?, enable_locate_timeout = ?, enable_debug_logs = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

-- name: UpdateColors :exec

UPDATE settings

SET color_locate = ?, color_stock_ok = ?, color_stock_low = ?, color_stock_critical = ?, updated_at = CURRENT_TIMESTAMP

WHERE id = 1;



-- name: MarkInspirationSeedsApplied :exec

UPDATE settings

SET inspiration_seeds_applied = 1, updated_at = CURRENT_TIMESTAMP

WHERE id = 1;
