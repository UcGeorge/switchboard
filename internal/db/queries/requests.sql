-- name: CreateRequest :one
INSERT INTO requests (
  id, api_key_id, route_channel_id, conversation_id, turn_index, status, model, endpoint, stream, priority,
  request_body, history_hash, full_hash, message_count, prompt_chars, prompt_tokens, max_attempts,
  client_ip, user_agent, created_at, timeout_at
) VALUES (?, ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetRequest :one
SELECT * FROM requests WHERE id = ?;

-- name: FindRequestByNextHash :one
SELECT * FROM requests WHERE api_key_id = ? AND next_hash = ? ORDER BY created_at DESC LIMIT 1;

-- name: ListClaimableRequests :many
SELECT * FROM requests
WHERE status = 'queued'
  AND timeout_at > sqlc.arg(now)
  AND (route_channel_id IS NULL OR route_channel_id = sqlc.arg(channel_id))
ORDER BY priority DESC, created_at ASC
LIMIT 50;

-- name: ClaimRequest :execrows
UPDATE requests
SET status = 'claimed', channel_id = ?, claimed_at = ?, lease_expires_at = ?, attempts = attempts + 1, error_message = NULL
WHERE id = ? AND status = 'queued';

-- name: MarkRequestStreaming :execrows
UPDATE requests
SET status = 'streaming', first_token_at = COALESCE(first_token_at, ?), lease_expires_at = ?
WHERE id = ? AND channel_id = ? AND status IN ('claimed', 'streaming');

-- name: ExtendRequestLease :execrows
UPDATE requests SET lease_expires_at = ? WHERE id = ? AND channel_id = ? AND status IN ('claimed', 'streaming');

-- name: CompleteRequest :execrows
UPDATE requests
SET status = 'completed', response_body = ?, response_text = ?, next_hash = ?, prompt_tokens = ?, completion_tokens = ?,
    usage_estimated = ?, finish_reason = ?, completed_at = ?, first_token_at = COALESCE(first_token_at, ?)
WHERE id = ? AND channel_id = ? AND status IN ('claimed', 'streaming');

-- name: FailRequest :execrows
UPDATE requests SET status = 'failed', error_message = ?, completed_at = ?
WHERE id = ? AND status IN ('queued', 'claimed', 'streaming');

-- name: RequeueRequest :execrows
UPDATE requests
SET status = 'queued', channel_id = NULL, claimed_at = NULL, lease_expires_at = NULL, first_token_at = NULL, error_message = ?
WHERE id = ? AND status IN ('claimed', 'streaming');

-- name: CancelRequest :execrows
UPDATE requests SET status = 'cancelled', error_message = ?, completed_at = ?
WHERE id = ? AND status IN ('queued', 'claimed', 'streaming');

-- name: ExpireRequest :execrows
UPDATE requests SET status = 'expired', error_message = ?, completed_at = ?
WHERE id = ? AND status IN ('queued', 'claimed', 'streaming');

-- name: CancelAllActiveRequests :execrows
UPDATE requests SET status = 'cancelled', error_message = ?, completed_at = ?
WHERE status IN ('queued', 'claimed', 'streaming');

-- name: ListExpiredLeases :many
SELECT * FROM requests WHERE status IN ('claimed', 'streaming') AND lease_expires_at IS NOT NULL AND lease_expires_at < ?;

-- name: ListTimedOutRequests :many
SELECT * FROM requests WHERE status IN ('queued', 'claimed', 'streaming') AND timeout_at < ?;

-- name: ListInFlightForChannel :many
SELECT * FROM requests WHERE channel_id = ? AND status IN ('claimed', 'streaming');

-- name: ListActiveRequests :many
SELECT * FROM requests WHERE status IN ('queued', 'claimed', 'streaming') ORDER BY created_at ASC;

-- name: ListRequests :many
SELECT * FROM requests
WHERE (status = sqlc.arg(status) OR sqlc.arg(status) = '')
  AND (api_key_id = sqlc.arg(api_key_id) OR sqlc.arg(api_key_id) = '')
  AND (channel_id = sqlc.arg(channel_id) OR sqlc.arg(channel_id) = '')
  AND (conversation_id = sqlc.arg(conversation_id) OR sqlc.arg(conversation_id) = '')
  AND (model = sqlc.arg(model) OR sqlc.arg(model) = '')
  AND (id LIKE sqlc.arg(search) OR response_text LIKE sqlc.arg(search) OR request_body LIKE sqlc.arg(search) OR sqlc.arg(search) = '')
ORDER BY created_at DESC
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(off);

-- name: CountRequests :one
SELECT COUNT(*) FROM requests
WHERE (status = sqlc.arg(status) OR sqlc.arg(status) = '')
  AND (api_key_id = sqlc.arg(api_key_id) OR sqlc.arg(api_key_id) = '')
  AND (channel_id = sqlc.arg(channel_id) OR sqlc.arg(channel_id) = '')
  AND (conversation_id = sqlc.arg(conversation_id) OR sqlc.arg(conversation_id) = '')
  AND (model = sqlc.arg(model) OR sqlc.arg(model) = '')
  AND (id LIKE sqlc.arg(search) OR response_text LIKE sqlc.arg(search) OR request_body LIKE sqlc.arg(search) OR sqlc.arg(search) = '');

-- name: CountRequestsByStatus :one
SELECT COUNT(*) FROM requests WHERE status = ?;

-- name: CountQueued :one
SELECT COUNT(*) FROM requests WHERE status = 'queued';

-- name: CountInFlight :one
SELECT COUNT(*) FROM requests WHERE status IN ('claimed', 'streaming');

-- name: ListRecentRequests :many
SELECT * FROM requests ORDER BY created_at DESC LIMIT ?;

-- name: DeleteFinishedRequestsOlderThan :execrows
DELETE FROM requests WHERE created_at < ? AND status NOT IN ('queued', 'claimed', 'streaming');

-- name: RequestStatsSince :one
SELECT
  COUNT(*) AS total,
  CAST(COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS completed,
  CAST(COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS failed,
  CAST(COALESCE(SUM(CASE WHEN status IN ('cancelled', 'expired') THEN 1 ELSE 0 END), 0) AS INTEGER) AS dropped,
  CAST(COALESCE(SUM(prompt_tokens), 0) AS INTEGER) AS prompt_tokens,
  CAST(COALESCE(SUM(completion_tokens), 0) AS INTEGER) AS completion_tokens
FROM requests WHERE created_at >= ?;

-- name: LatencySamplesSince :many
SELECT
  CAST(completed_at - created_at AS INTEGER) AS latency_ms,
  CAST(COALESCE(first_token_at, completed_at) - created_at AS INTEGER) AS ttft_ms,
  CAST(COALESCE(claimed_at, created_at) - created_at AS INTEGER) AS queue_ms
FROM requests
WHERE status = 'completed' AND completed_at IS NOT NULL AND created_at >= ?
ORDER BY created_at DESC LIMIT 5000;

-- name: ThroughputBuckets :many
SELECT
  CAST(created_at / sqlc.arg(bucket_ms) AS INTEGER) AS bucket,
  COUNT(*) AS total,
  CAST(COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS completed,
  CAST(COALESCE(SUM(CASE WHEN status IN ('failed', 'cancelled', 'expired') THEN 1 ELSE 0 END), 0) AS INTEGER) AS failed
FROM requests
WHERE created_at >= sqlc.arg(since)
GROUP BY bucket ORDER BY bucket;

-- name: ModelUsageSince :many
SELECT
  model,
  COUNT(*) AS total,
  CAST(COALESCE(SUM(prompt_tokens), 0) AS INTEGER) AS prompt_tokens,
  CAST(COALESCE(SUM(completion_tokens), 0) AS INTEGER) AS completion_tokens
FROM requests WHERE created_at >= ?
GROUP BY model ORDER BY total DESC LIMIT 20;

-- name: KeyUsageSince :many
SELECT
  api_key_id,
  COUNT(*) AS total,
  CAST(COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS completed,
  CAST(COALESCE(SUM(prompt_tokens), 0) AS INTEGER) AS prompt_tokens,
  CAST(COALESCE(SUM(completion_tokens), 0) AS INTEGER) AS completion_tokens
FROM requests WHERE created_at >= ?
GROUP BY api_key_id ORDER BY total DESC;

-- name: ChannelUsageSince :many
SELECT
  channel_id,
  COUNT(*) AS total,
  CAST(COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS completed,
  CAST(COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS failed
FROM requests WHERE created_at >= ? AND channel_id IS NOT NULL
GROUP BY channel_id ORDER BY total DESC;

-- name: DistinctModels :many
SELECT DISTINCT model FROM requests ORDER BY model;
