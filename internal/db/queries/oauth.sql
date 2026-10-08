-- name: CreateOAuthClient :one
INSERT INTO oauth_clients (id, secret_hash, name, redirect_uris, grant_types, token_endpoint_auth_method, metadata, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetOAuthClient :one
SELECT * FROM oauth_clients WHERE id = ?;

-- name: ListOAuthClients :many
SELECT * FROM oauth_clients ORDER BY created_at DESC;

-- name: DeleteOAuthClient :execrows
DELETE FROM oauth_clients WHERE id = ?;

-- name: CreateOAuthCode :exec
INSERT INTO oauth_codes (code_hash, client_id, redirect_uri, code_challenge, code_challenge_method, scope, resource, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetOAuthCode :one
SELECT * FROM oauth_codes WHERE code_hash = ?;

-- name: ConsumeOAuthCode :execrows
UPDATE oauth_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?;

-- name: DeleteExpiredOAuthCodes :execrows
DELETE FROM oauth_codes WHERE expires_at < ?;
