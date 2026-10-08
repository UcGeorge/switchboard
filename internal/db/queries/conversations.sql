-- name: CreateConversation :one
INSERT INTO conversations (id, api_key_id, root_hash, title, model, turn_count, last_request_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetConversation :one
SELECT * FROM conversations WHERE id = ?;

-- name: FindConversationByRoot :one
SELECT * FROM conversations
WHERE api_key_id = ? AND root_hash = ? AND updated_at >= ?
ORDER BY updated_at DESC LIMIT 1;

-- name: BumpConversation :exec
UPDATE conversations SET turn_count = ?, last_request_id = ?, model = ?, updated_at = ? WHERE id = ?;

-- name: ListConversations :many
SELECT * FROM conversations
WHERE (api_key_id = sqlc.arg(api_key_id) OR sqlc.arg(api_key_id) = '')
  AND (title LIKE sqlc.arg(search) OR id LIKE sqlc.arg(search) OR sqlc.arg(search) = '')
ORDER BY updated_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountConversations :one
SELECT COUNT(*) FROM conversations
WHERE (api_key_id = sqlc.arg(api_key_id) OR sqlc.arg(api_key_id) = '')
  AND (title LIKE sqlc.arg(search) OR id LIKE sqlc.arg(search) OR sqlc.arg(search) = '');

-- name: ListConversationRequests :many
SELECT * FROM requests WHERE conversation_id = ? ORDER BY turn_index ASC, created_at ASC;

-- name: DeleteConversation :execrows
DELETE FROM conversations WHERE id = ?;

-- name: DeleteOrphanConversations :execrows
DELETE FROM conversations WHERE id NOT IN (SELECT DISTINCT conversation_id FROM requests WHERE conversation_id IS NOT NULL);
