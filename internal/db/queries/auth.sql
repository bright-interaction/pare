-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: InsertUser :one
INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: InsertSession :exec
INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, $3);

-- name: GetSession :one
SELECT s.user_id, s.expires_at, u.email, u.role
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token = $1;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < now();
