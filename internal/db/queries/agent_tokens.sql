-- name: CreateAgentToken :one
INSERT INTO agent_tokens (id, name, kind, token_hash, token_prefix, refresh_token_hash, oauth_client_id, scope, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAgentToken :one
SELECT * FROM agent_tokens WHERE id = ?;

-- name: GetAgentTokenByHash :one
SELECT * FROM agent_tokens WHERE token_hash = ?;

-- name: GetAgentTokenByRefreshHash :one
SELECT * FROM agent_tokens WHERE refresh_token_hash = ?;

-- name: ListAgentTokens :many
SELECT * FROM agent_tokens ORDER BY created_at DESC;

-- name: RotateAgentToken :exec
UPDATE agent_tokens
SET token_hash = ?, token_prefix = ?, refresh_token_hash = ?, expires_at = ?
WHERE id = ?;

-- name: RevokeAgentToken :execrows
UPDATE agent_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: TouchAgentToken :exec
UPDATE agent_tokens SET last_used_at = ? WHERE id = ?;

-- name: DeleteAgentToken :execrows
DELETE FROM agent_tokens WHERE id = ?;

-- name: DeleteExpiredOAuthTokens :execrows
DELETE FROM agent_tokens WHERE kind = 'oauth' AND expires_at IS NOT NULL AND expires_at < ? AND (refresh_token_hash IS NULL OR created_at < ?);
