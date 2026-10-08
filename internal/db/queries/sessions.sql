-- name: CreateSession :exec
INSERT INTO sessions (id, created_at, expires_at, user_agent) VALUES (?, ?, ?, ?);

-- name: GetSession :one
SELECT * FROM sessions WHERE id = ? AND expires_at > ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- name: DeleteAllSessions :execrows
DELETE FROM sessions;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < ?;

-- name: CreateLoginToken :exec
INSERT INTO login_tokens (token_hash, expires_at) VALUES (?, ?);

-- name: ConsumeLoginToken :execrows
UPDATE login_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?;

-- name: DeleteExpiredLoginTokens :execrows
DELETE FROM login_tokens WHERE expires_at < ?;
