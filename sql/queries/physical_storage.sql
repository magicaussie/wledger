-- name: CreateCabinet :one
INSERT INTO cabinets (name, controller_id, coordinate_system, display_order)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: GetCabinet :one
SELECT * FROM cabinets WHERE id = ?;

-- name: GetCabinetByController :one
SELECT * FROM cabinets WHERE controller_id = ?;

-- name: ListCabinets :many
SELECT * FROM cabinets
WHERE archived = 0
ORDER BY display_order ASC, id ASC;

-- name: ListCabinetsIncludingArchived :many
SELECT * FROM cabinets
ORDER BY display_order ASC, id ASC;

-- name: UpdateCabinet :execrows
UPDATE cabinets
SET name = ?, controller_id = ?, coordinate_system = ?, display_order = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateCabinetLayoutRevision :execrows
UPDATE cabinets
SET layout_revision = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateCabinetElectricalRevision :execrows
UPDATE cabinets
SET electrical_revision = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ArchiveCabinet :execrows
UPDATE cabinets
SET archived = 1, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UnarchiveCabinet :execrows
UPDATE cabinets
SET archived = 0, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteCabinet :execrows
DELETE FROM cabinets WHERE id = ?;

-- name: CreateDrawer :one
INSERT INTO drawers (cabinet_id, label, display_order, x, y, width, height)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: GetDrawer :one
SELECT * FROM drawers WHERE id = ?;

-- name: ListDrawersByCabinet :many
SELECT * FROM drawers
WHERE cabinet_id = ? AND archived = 0
ORDER BY display_order ASC, id ASC;

-- name: ListDrawersByCabinetIncludingArchived :many
SELECT * FROM drawers
WHERE cabinet_id = ?
ORDER BY display_order ASC, id ASC;

-- name: UpdateDrawer :execrows
UPDATE drawers
SET cabinet_id = ?, label = ?, display_order = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateDrawerGeometry :execrows
UPDATE drawers
SET x = ?, y = ?, width = ?, height = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ArchiveDrawer :execrows
UPDATE drawers
SET archived = 1, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UnarchiveDrawer :execrows
UPDATE drawers
SET archived = 0, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteDrawer :execrows
DELETE FROM drawers WHERE id = ?;

-- name: CreateBinDrawerAttachment :exec
INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetBinDrawerAttachment :one
SELECT * FROM bin_drawer_attachments WHERE bin_id = ?;

-- name: ListBinDrawerAttachments :many
SELECT * FROM bin_drawer_attachments
ORDER BY drawer_id ASC, display_order ASC, bin_id ASC;

-- name: ListBinDrawerAttachmentsByDrawer :many
SELECT * FROM bin_drawer_attachments
WHERE drawer_id = ?
ORDER BY display_order ASC, bin_id ASC;

-- name: UpdateBinDrawerAttachment :execrows
UPDATE bin_drawer_attachments
SET drawer_id = ?, display_order = ?, updated_at = CURRENT_TIMESTAMP
WHERE bin_id = ?;

-- name: UpdateBinDrawerAttachmentGeometry :execrows
UPDATE bin_drawer_attachments
SET x = ?, y = ?, width = ?, height = ?, updated_at = CURRENT_TIMESTAMP
WHERE bin_id = ?;

-- name: DeleteBinDrawerAttachment :execrows
DELETE FROM bin_drawer_attachments WHERE bin_id = ?;

-- ListAllDrawers returns every drawer, including archived rows and every
-- cabinet, in a deterministic order so an exported snapshot is stable and
-- complete.
-- name: ListAllDrawers :many
SELECT * FROM drawers
ORDER BY cabinet_id ASC, display_order ASC, id ASC;

-- ClearBinDrawerAttachments empties the bin attachment table. It must run
-- before any legacy bin or drawer deletion: attachments reference both.
-- name: ClearBinDrawerAttachments :exec
DELETE FROM bin_drawer_attachments;

-- ClearDrawers empties the drawer table. It must run before any legacy cabinet
-- or controller deletion, and after the attachments that reference drawers.
-- name: ClearDrawers :exec
DELETE FROM drawers;

-- ClearCabinets empties the cabinet table. It must run before any legacy
-- controller deletion, and after the drawers and cabinets it references.
-- name: ClearCabinets :exec
DELETE FROM cabinets;

-- RestoreCabinet re-inserts a captured cabinet with its exact id, revisions,
-- archived state and timestamps.
-- name: RestoreCabinet :exec
INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- RestoreDrawer re-inserts a captured drawer with its exact id, geometry,
-- archived state and timestamps.
-- name: RestoreDrawer :exec
INSERT INTO drawers (id, cabinet_id, label, display_order, x, y, width, height, archived, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- RestoreBinDrawerAttachment re-inserts a captured bin attachment with its
-- exact bin, drawer, geometry and timestamps.
-- name: RestoreBinDrawerAttachment :exec
INSERT INTO bin_drawer_attachments (bin_id, drawer_id, x, y, width, height, display_order, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
