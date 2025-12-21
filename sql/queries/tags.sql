-- name: GetTagByName :one
SELECT * FROM tags WHERE name = ? LIMIT 1;

-- name: CreateTag :one
INSERT INTO tags (name) VALUES (?) RETURNING *;

-- name: ListAllTags :many
SELECT * FROM tags ORDER BY name ASC;

-- name: AddTagToPart :exec
INSERT OR IGNORE INTO part_tags (part_id, tag_id) VALUES (?, ?);

-- name: RemoveTagsFromPart :exec
DELETE FROM part_tags WHERE part_id = ?;

-- name: GetTagsForPart :many
SELECT t.* FROM tags t
JOIN part_tags pt ON t.id = pt.tag_id
WHERE pt.part_id = ?
ORDER BY t.name ASC;

-- name: DeleteUnusedTags :exec
DELETE FROM tags WHERE id NOT IN (SELECT DISTINCT tag_id FROM part_tags);

-- name: GetAllPartTags :many
SELECT * FROM part_tags;

-- name: RestoreTag :exec
INSERT INTO tags (id, name) VALUES (?, ?);

-- name: RestorePartTag :exec
INSERT INTO part_tags (part_id, tag_id) VALUES (?, ?);
