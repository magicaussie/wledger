-- name: ListDrawerLEDAllocationsByDrawer :many
SELECT * FROM drawer_led_allocations
WHERE drawer_id = ?
ORDER BY display_order ASC, id ASC;

-- name: ListDrawerLEDAllocationsByCabinet :many
SELECT * FROM drawer_led_allocations
WHERE cabinet_id = ?
ORDER BY start_pixel ASC, drawer_id ASC, display_order ASC, id ASC;

-- name: CreateDrawerLEDAllocation :one
INSERT INTO drawer_led_allocations (cabinet_id, drawer_id, start_pixel, pixel_count, display_order)
VALUES (?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateDrawerLEDAllocation :execrows
UPDATE drawer_led_allocations
SET start_pixel = ?, pixel_count = ?, display_order = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND cabinet_id = ? AND drawer_id = ?;

-- name: DeleteDrawerLEDAllocation :execrows
DELETE FROM drawer_led_allocations
WHERE id = ? AND cabinet_id = ? AND drawer_id = ?;

-- name: ListAllDrawerLEDAllocations :many
SELECT * FROM drawer_led_allocations
ORDER BY cabinet_id ASC, drawer_id ASC, display_order ASC, id ASC;

-- name: ClearDrawerLEDAllocations :exec
DELETE FROM drawer_led_allocations;

-- name: RestoreDrawerLEDAllocation :exec
INSERT INTO drawer_led_allocations (id, cabinet_id, drawer_id, start_pixel, pixel_count, display_order, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);
