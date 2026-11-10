-- name: CreateUser :one
INSERT INTO users (id, username, password_hash) VALUES ($1, $2, $3) RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1;

-- name: UpdatePasswordHash :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: DeleteUser :exec
-- Cascades to every row the user owns.
DELETE FROM users WHERE id = $1;

-- name: CreateDefaultSettings :exec
-- Dated 1970 so the phone's first real edit always wins last-write-wins, whatever its clock says.
INSERT INTO settings (user_id, updated_at) VALUES ($1, 'epoch') ON CONFLICT (user_id) DO NOTHING;

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3);

-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = $1;

-- name: TouchSession :exec
UPDATE sessions SET last_used_at = now(), expires_at = $2 WHERE token_hash = $1;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteOtherSessions :exec
DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2;
