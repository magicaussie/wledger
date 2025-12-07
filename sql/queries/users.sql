-- name: CreateUser :one
INSERT INTO users (email, password_hash, role) 
VALUES (?, ?, ?)
RETURNING id, email, role, created_at;

-- name: GetUserByEmail :one
SELECT id, email, password_hash, role, created_at 
FROM users 
WHERE email = ? LIMIT 1;

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: GetSession :one
SELECT token, data, expiry 
FROM sessions 
WHERE token = ?;

-- name: CreateSession :exec
INSERT INTO sessions (token, data, expiry) 
VALUES (?, ?, ?);

-- name: DeleteSession :exec
DELETE FROM sessions 
WHERE token = ?;

-- name: CleanupSessions :exec
DELETE FROM sessions 
WHERE expiry < ?;