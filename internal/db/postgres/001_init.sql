-- Switchboard schema. All timestamps are unix milliseconds (BIGINT).

CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at BIGINT NOT NULL
);

-- API keys used by OpenAI-compatible callers (Authorization: Bearer sk-sb-...).
CREATE TABLE api_keys (
  id                TEXT PRIMARY KEY,
  name              TEXT NOT NULL,
  key_hash          TEXT NOT NULL UNIQUE,
  key_prefix        TEXT NOT NULL,
  rate_limit_rpm    BIGINT NOT NULL DEFAULT 0,
  allowed_models    TEXT NOT NULL DEFAULT '',
  pinned_channel_id TEXT,
  timeout_seconds   BIGINT NOT NULL DEFAULT 0,
  priority          BIGINT NOT NULL DEFAULT 0,
  request_count     BIGINT NOT NULL DEFAULT 0,
  last_used_at      BIGINT,
  created_at        BIGINT NOT NULL,
  revoked_at        BIGINT
);

-- Bearer credentials accepted by the MCP endpoint (static tokens or OAuth-issued).
CREATE TABLE agent_tokens (
  id                 TEXT PRIMARY KEY,
  name               TEXT NOT NULL,
  kind               TEXT NOT NULL,
  token_hash         TEXT NOT NULL UNIQUE,
  token_prefix       TEXT NOT NULL,
  refresh_token_hash TEXT UNIQUE,
  oauth_client_id    TEXT,
  scope              TEXT NOT NULL DEFAULT 'mcp',
  expires_at         BIGINT,
  last_used_at       BIGINT,
  created_at         BIGINT NOT NULL,
  revoked_at         BIGINT
);

CREATE TABLE oauth_clients (
  id                         TEXT PRIMARY KEY,
  secret_hash                TEXT,
  name                       TEXT NOT NULL,
  redirect_uris              TEXT NOT NULL,
  grant_types                TEXT NOT NULL,
  token_endpoint_auth_method TEXT NOT NULL,
  metadata                   TEXT NOT NULL DEFAULT '{}',
  created_at                 BIGINT NOT NULL
);

CREATE TABLE oauth_codes (
  code_hash             TEXT PRIMARY KEY,
  client_id             TEXT NOT NULL,
  redirect_uri          TEXT NOT NULL,
  code_challenge        TEXT NOT NULL,
  code_challenge_method TEXT NOT NULL,
  scope                 TEXT NOT NULL,
  resource              TEXT NOT NULL DEFAULT '',
  expires_at            BIGINT NOT NULL,
  used_at               BIGINT,
  created_at            BIGINT NOT NULL
);

-- A channel is one connected agent instance that claims and answers requests.
CREATE TABLE channels (
  id               TEXT PRIMARY KEY,
  name             TEXT NOT NULL,
  description      TEXT NOT NULL DEFAULT '',
  agent_token_id   TEXT,
  status           TEXT NOT NULL DEFAULT 'online',
  models           TEXT NOT NULL DEFAULT '*',
  concurrency      BIGINT NOT NULL DEFAULT 1,
  served_count     BIGINT NOT NULL DEFAULT 0,
  failed_count     BIGINT NOT NULL DEFAULT 0,
  total_latency_ms BIGINT NOT NULL DEFAULT 0,
  last_seen_at     BIGINT NOT NULL,
  created_at       BIGINT NOT NULL,
  closed_at        BIGINT
);
CREATE INDEX idx_channels_status ON channels(status, closed_at);

CREATE TABLE conversations (
  id              TEXT PRIMARY KEY,
  api_key_id      TEXT NOT NULL,
  root_hash       TEXT NOT NULL,
  title           TEXT NOT NULL,
  model           TEXT NOT NULL,
  turn_count      BIGINT NOT NULL DEFAULT 0,
  last_request_id TEXT,
  created_at      BIGINT NOT NULL,
  updated_at      BIGINT NOT NULL
);
CREATE INDEX idx_conversations_root ON conversations(api_key_id, root_hash);
CREATE INDEX idx_conversations_updated ON conversations(updated_at DESC);

CREATE TABLE requests (
  id                TEXT PRIMARY KEY,
  api_key_id        TEXT NOT NULL,
  channel_id        TEXT,
  route_channel_id  TEXT,
  conversation_id   TEXT,
  turn_index        BIGINT NOT NULL DEFAULT 0,
  status            TEXT NOT NULL,
  model             TEXT NOT NULL,
  endpoint          TEXT NOT NULL,
  stream            BOOLEAN NOT NULL DEFAULT FALSE,
  priority          BIGINT NOT NULL DEFAULT 0,
  request_body      TEXT NOT NULL,
  response_body     TEXT,
  response_text     TEXT,
  error_message     TEXT,
  history_hash      TEXT NOT NULL,
  full_hash         TEXT NOT NULL,
  next_hash         TEXT,
  message_count     BIGINT NOT NULL DEFAULT 0,
  prompt_chars      BIGINT NOT NULL DEFAULT 0,
  prompt_tokens     BIGINT NOT NULL DEFAULT 0,
  completion_tokens BIGINT NOT NULL DEFAULT 0,
  usage_estimated   BOOLEAN NOT NULL DEFAULT TRUE,
  attempts          BIGINT NOT NULL DEFAULT 0,
  max_attempts      BIGINT NOT NULL DEFAULT 3,
  client_ip         TEXT NOT NULL DEFAULT '',
  user_agent        TEXT NOT NULL DEFAULT '',
  finish_reason     TEXT,
  created_at        BIGINT NOT NULL,
  claimed_at        BIGINT,
  first_token_at    BIGINT,
  completed_at      BIGINT,
  lease_expires_at  BIGINT,
  timeout_at        BIGINT NOT NULL
);
CREATE INDEX idx_requests_queue ON requests(status, priority DESC, created_at);
CREATE INDEX idx_requests_key ON requests(api_key_id, created_at DESC);
CREATE INDEX idx_requests_channel ON requests(channel_id, created_at DESC);
CREATE INDEX idx_requests_conversation ON requests(conversation_id, turn_index);
CREATE INDEX idx_requests_next_hash ON requests(api_key_id, next_hash);
CREATE INDEX idx_requests_created ON requests(created_at DESC);

CREATE TABLE events (
  id              BIGSERIAL PRIMARY KEY,
  ts              BIGINT NOT NULL,
  kind            TEXT NOT NULL,
  level           TEXT NOT NULL DEFAULT 'info',
  request_id      TEXT,
  channel_id      TEXT,
  api_key_id      TEXT,
  conversation_id TEXT,
  message         TEXT NOT NULL,
  data            TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_events_ts ON events(ts DESC);
CREATE INDEX idx_events_request ON events(request_id);
CREATE INDEX idx_events_channel ON events(channel_id);

-- Dashboard sessions and one-time login links.
CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  created_at BIGINT NOT NULL,
  expires_at BIGINT NOT NULL,
  user_agent TEXT NOT NULL DEFAULT ''
);

CREATE TABLE login_tokens (
  token_hash TEXT PRIMARY KEY,
  expires_at BIGINT NOT NULL,
  used_at    BIGINT
);
