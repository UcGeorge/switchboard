-- name: InsertEvent :exec
INSERT INTO events (ts, kind, level, request_id, channel_id, api_key_id, conversation_id, message, data)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListEvents :many
SELECT * FROM events
WHERE (kind = sqlc.arg(kind) OR sqlc.arg(kind) = '')
  AND (level = sqlc.arg(level) OR sqlc.arg(level) = '')
  AND (request_id = sqlc.arg(request_id) OR sqlc.arg(request_id) = '')
  AND (channel_id = sqlc.arg(channel_id) OR sqlc.arg(channel_id) = '')
  AND (api_key_id = sqlc.arg(api_key_id) OR sqlc.arg(api_key_id) = '')
  AND (message LIKE sqlc.arg(search) OR sqlc.arg(search) = '')
ORDER BY id DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountEvents :one
SELECT COUNT(*) FROM events
WHERE (kind = sqlc.arg(kind) OR sqlc.arg(kind) = '')
  AND (level = sqlc.arg(level) OR sqlc.arg(level) = '')
  AND (request_id = sqlc.arg(request_id) OR sqlc.arg(request_id) = '')
  AND (channel_id = sqlc.arg(channel_id) OR sqlc.arg(channel_id) = '')
  AND (api_key_id = sqlc.arg(api_key_id) OR sqlc.arg(api_key_id) = '')
  AND (message LIKE sqlc.arg(search) OR sqlc.arg(search) = '');

-- name: ListRequestEvents :many
SELECT * FROM events WHERE request_id = ? ORDER BY id ASC;

-- name: ListRecentEvents :many
SELECT * FROM events ORDER BY id DESC LIMIT ?;

-- name: DeleteEventsOlderThan :execrows
DELETE FROM events WHERE ts < ?;

-- name: DistinctEventKinds :many
SELECT DISTINCT kind FROM events ORDER BY kind;
