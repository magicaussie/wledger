-- name: GetSettings :one
SELECT * FROM settings WHERE id = 1;

-- name: InitSettings :exec
INSERT OR IGNORE INTO settings (id) VALUES (1);

-- name: UpdateSettings :exec
UPDATE settings 
SET locate_timeout_seconds = ?, enable_locate_timeout = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = 1;

-- name: UpdateColors :exec
UPDATE settings
SET color_locate = ?, color_stock_ok = ?, color_stock_low = ?, color_stock_critical = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = 1;