-- name: CreateAPIKey :one
INSERT INTO api_keys (id, name, key_hash, key_prefix, rate_limit_rpm, allowed_models, pinned_channel_id, timeout_seconds, priority, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetAPIKey :one
SELECT * FROM api_keys WHERE id = ?;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = ?;

-- name: ListAPIKeys :many
SELECT * FROM api_keys ORDER BY created_at DESC;

-- name: UpdateAPIKey :exec
UPDATE api_keys
SET name = ?, rate_limit_rpm = ?, allowed_models = ?, pinned_channel_id = ?, timeout_seconds = ?, priority = ?
WHERE id = ?;

-- name: RevokeAPIKey :execrows
UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: TouchAPIKey :exec
UPDATE api_keys SET last_used_at = ?, request_count = request_count + 1 WHERE id = ?;

-- name: DeleteAPIKey :execrows
DELETE FROM api_keys WHERE id = ?;

-- name: CountActiveAPIKeys :one
SELECT COUNT(*) FROM api_keys WHERE revoked_at IS NULL;
