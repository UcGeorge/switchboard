-- name: CreateChannel :one
INSERT INTO channels (id, name, description, agent_token_id, status, models, concurrency, last_seen_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetChannel :one
SELECT * FROM channels WHERE id = ?;

-- name: GetOpenChannelByName :one
SELECT * FROM channels WHERE name = ? AND closed_at IS NULL ORDER BY created_at DESC LIMIT 1;

-- name: ListChannels :many
SELECT * FROM channels ORDER BY (closed_at IS NULL) DESC, (status = 'online') DESC, last_seen_at DESC;

-- name: ListOpenChannels :many
SELECT * FROM channels WHERE closed_at IS NULL ORDER BY (status = 'online') DESC, last_seen_at DESC;

-- name: ListOnlineChannels :many
SELECT * FROM channels WHERE status = 'online' AND closed_at IS NULL ORDER BY last_seen_at DESC;

-- name: TouchChannel :exec
UPDATE channels SET last_seen_at = ?, status = 'online' WHERE id = ? AND closed_at IS NULL AND status != 'draining';

-- name: SetChannelStatus :exec
UPDATE channels SET status = ? WHERE id = ?;

-- name: CloseChannel :execrows
UPDATE channels SET status = 'offline', closed_at = ? WHERE id = ? AND closed_at IS NULL;

-- name: ReopenChannel :exec
UPDATE channels SET status = 'online', closed_at = NULL, last_seen_at = ?, agent_token_id = ?, models = ?, concurrency = ?, description = ? WHERE id = ?;

-- name: UpdateChannelConfig :exec
UPDATE channels SET name = ?, description = ?, models = ?, concurrency = ? WHERE id = ?;

-- name: IncrChannelServed :exec
UPDATE channels SET served_count = served_count + 1, total_latency_ms = total_latency_ms + ? WHERE id = ?;

-- name: IncrChannelFailed :exec
UPDATE channels SET failed_count = failed_count + 1 WHERE id = ?;

-- name: ListStaleOnlineChannels :many
SELECT * FROM channels WHERE status = 'online' AND closed_at IS NULL AND last_seen_at < ?;

-- name: CountChannelsByStatus :one
SELECT COUNT(*) FROM channels WHERE status = ? AND closed_at IS NULL;

-- name: CountChannelInFlight :one
SELECT COUNT(*) FROM requests WHERE channel_id = ? AND status IN ('claimed', 'streaming');

-- name: DeleteChannel :execrows
DELETE FROM channels WHERE id = ?;
