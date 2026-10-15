-- name: CreateCabinet :one
INSERT INTO cabinets (name, controller_id, coordinate_system, display_order)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: GetCabinet :one
SELECT * FROM cabinets WHERE id = ?;

-- name: GetCabinetByController :one
SELECT * FROM cabinets WHERE controller_id = ?;

-- name: GetCabinetByAssignedController :one
SELECT c.* FROM cabinets c
JOIN cabinet_controllers cc ON cc.cabinet_id = c.id
WHERE cc.controller_id = ?;

-- name: ListCabinetControllers :many
SELECT * FROM cabinet_controllers
WHERE cabinet_id = ?
ORDER BY slot ASC;

-- name: ListAllCabinetControllers :many
SELECT * FROM cabinet_controllers
ORDER BY cabinet_id ASC, slot ASC;

-- name: CreateCabinetController :exec
INSERT INTO cabinet_controllers (cabinet_id, controller_id, slot)
VALUES (?, ?, ?);

-- name: DeleteCabinetController :execrows
DELETE FROM cabinet_controllers WHERE cabinet_id = ? AND controller_id = ?;

-- name: ClearCabinetControllers :exec
DELETE FROM cabinet_controllers;

-- name: RestoreCabinetController :exec
INSERT INTO cabinet_controllers (cabinet_id, controller_id, slot, strings_configured, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

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

-- name: UpdateCabinetController :execrows
UPDATE cabinets
SET controller_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateCabinetLayoutSettings :execrows
UPDATE cabinets
SET name = ?, coordinate_system = ?, display_order = ?, layout_width = ?, layout_height = ?, layout_decorations_json = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateCabinetLayoutRevision :execrows
UPDATE cabinets
SET layout_revision = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: AdvanceCabinetLayoutRevision :execrows
UPDATE cabinets
SET layout_revision = layout_revision + 1, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND layout_revision = ?;

-- name: UpdateCabinetElectricalRevision :execrows
UPDATE cabinets
SET electrical_revision = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: AdvanceCabinetElectricalRevision :execrows
UPDATE cabinets
SET electrical_revision = electrical_revision + 1, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND electrical_revision = ?;

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

-- name: ListUnattachedBins :many
SELECT id, name FROM bins
WHERE id NOT IN (SELECT bin_id FROM bin_drawer_attachments)
ORDER BY name ASC, id ASC;

-- name: UpdateBinName :execrows
UPDATE bins SET name = ? WHERE id = ?;

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
INSERT INTO cabinets (id, name, controller_id, coordinate_system, layout_revision, electrical_revision, display_order, archived, created_at, updated_at, layout_width, layout_height, layout_decorations_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

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

-- name: UpdateCabinetControllerSlot :execrows
UPDATE cabinet_controllers SET slot = ?, updated_at = CURRENT_TIMESTAMP
WHERE cabinet_id = ? AND controller_id = ?;

-- name: UpdateCabinetControllerStringsConfigured :execrows
UPDATE cabinet_controllers SET strings_configured = ?, updated_at = CURRENT_TIMESTAMP
WHERE cabinet_id = ? AND controller_id = ?;

-- name: CreateCabinetControllerReorderGuard :exec
INSERT INTO cabinet_controller_reorder_guard (cabinet_id) VALUES (?);

-- name: DeleteCabinetControllerReorderGuard :exec
DELETE FROM cabinet_controller_reorder_guard WHERE cabinet_id = ?;

-- name: ListCabinetLEDStrings :many
SELECT s.* FROM cabinet_led_strings s
JOIN cabinet_controllers cc ON cc.cabinet_id = s.cabinet_id AND cc.controller_id = s.controller_id
WHERE s.cabinet_id = ?
ORDER BY cc.slot ASC, s.display_order ASC, s.id ASC;

-- name: ListCabinetLEDStringsByController :many
SELECT * FROM cabinet_led_strings
WHERE cabinet_id = ? AND controller_id = ?
ORDER BY display_order ASC, id ASC;

-- name: CreateCabinetLEDString :one
INSERT INTO cabinet_led_strings (cabinet_id, controller_id, name, display_order, pixel_count, layout_complete, controller_output_id)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: UpdateCabinetLEDString :execrows
UPDATE cabinet_led_strings
SET name = ?, display_order = ?, pixel_count = ?, layout_complete = ?, controller_output_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND cabinet_id = ? AND controller_id = ?;

-- name: DeleteCabinetLEDString :execrows
DELETE FROM cabinet_led_strings WHERE id = ? AND cabinet_id = ? AND controller_id = ?;

-- name: UpdateCabinetLEDStringOutput :execrows
UPDATE cabinet_led_strings SET controller_output_id = ? WHERE id = ? AND controller_id = ?;

-- name: ListCabinetLEDPathSections :many
SELECT p.* FROM cabinet_led_path_sections p
JOIN cabinet_led_strings s ON s.id = p.string_id
WHERE s.cabinet_id = ?
ORDER BY s.controller_id ASC, s.display_order ASC, p.display_order ASC, p.id ASC;

-- name: ListCabinetLEDPathSectionsByString :many
SELECT * FROM cabinet_led_path_sections
WHERE string_id = ?
ORDER BY display_order ASC, id ASC;

-- name: CreateCabinetLEDPathSection :one
INSERT INTO cabinet_led_path_sections (string_id, display_order, pixel_count, points_json)
VALUES (?, ?, ?, ?)
RETURNING id;

-- name: UpdateCabinetLEDPathSection :execrows
UPDATE cabinet_led_path_sections
SET display_order = ?, pixel_count = ?, points_json = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND string_id = ?;

-- name: DeleteCabinetLEDPathSectionsByString :exec
DELETE FROM cabinet_led_path_sections WHERE string_id = ?;

-- name: RestoreCabinetLEDString :exec
INSERT INTO cabinet_led_strings (id, cabinet_id, controller_id, name, display_order, pixel_count, layout_complete, created_at, updated_at, controller_output_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: RestoreCabinetLEDPathSection :exec
INSERT INTO cabinet_led_path_sections (id, string_id, display_order, pixel_count, points_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: SetControllerLEDCapacityFromStrings :execrows
UPDATE controllers SET led_count = ? WHERE id = ?;

-- name: ListPhysicalLocateSessionTargets :many
SELECT * FROM physical_locate_session_targets
WHERE session_id = ?
ORDER BY controller_id ASC;

-- name: ClearPhysicalLocateSessionTargets :exec
DELETE FROM physical_locate_session_targets WHERE session_id = ?;

-- name: ClearPhysicalLocateSessionTargetsForPrimaryController :exec
DELETE FROM physical_locate_session_targets
WHERE physical_locate_session_targets.session_id = (
    SELECT physical_locate_sessions.session_id FROM physical_locate_sessions
    WHERE physical_locate_sessions.controller_id = ?
);

-- name: SavePhysicalLocateSessionTarget :exec
INSERT INTO physical_locate_session_targets
    (session_id, controller_id, controller_address, controller_port, ranges_json, commands_json, fingerprint, may_have_received, last_error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAllCabinetLEDStrings :many
SELECT * FROM cabinet_led_strings
ORDER BY cabinet_id ASC, controller_id ASC, display_order ASC, id ASC;

-- name: ListAllCabinetLEDPathSections :many
SELECT p.* FROM cabinet_led_path_sections p
JOIN cabinet_led_strings s ON s.id = p.string_id
ORDER BY s.cabinet_id ASC, s.controller_id ASC, s.display_order ASC, p.display_order ASC, p.id ASC;

-- name: ClearCabinetLEDPathSections :exec
DELETE FROM cabinet_led_path_sections;

-- name: ClearCabinetLEDStrings :exec
DELETE FROM cabinet_led_strings;
