---
title: "Troubleshooting"
description: "Resolve startup, connection, authorization, queue, streaming, and remote CLI problems."
---

Start with `switchboard version`, `/healthz`, and `switchboard events list`. Redact credentials, login links and message bodies before sharing output.

## Startup says the data or log directory is missing

Current builds create the data directory before opening the TUI log. If an older binary reports `open log file ... no such file or directory`, rebuild or upgrade; this was a first-run bug. If the directory exists but creation still fails, verify ownership and the selected `--db` path. Avoid running the application with sudo just to work around a user-directory permissions problem.

## Port in use

Use `switchboard --addr 127.0.0.1:8081`. Local connection commands should use the same `--addr`. Do not run another server over the same database to bypass a port conflict.

## Login link expired or password lost

Generate a new one with `switchboard auth login-link`. Local terminal users can reset the password with `auth set-password`. If you are connecting remotely, use an administration token or contact the server operator. Magic links are single use.

## No response / 504

The application may be connected correctly but no agent is serving. Check `channels list` and `requests pending`. Ask the agent to start the [serving loop](../mcp/). Check model patterns, pinned channels, draining status, concurrency, request deadline, and the caller SDK's own timeout.

## 401 or 403

- Caller `401`: use the full `sk-sb-` key, not its display prefix or an agent token.
- MCP `401`: use an agent/OAuth token, check revocation/expiry, or repeat OAuth authorization.
- Caller `403 model_not_allowed`: the key's allowlist does not permit that model.
- Remote CLI `401`: use an administration token and the instance base URL.
- Dashboard `403`: reload the page for a fresh CSRF token; do not issue cross-origin mutations.

## Lease failures while the agent is thinking

Extend the request lease explicitly. Heartbeat the channel separately when not polling. Raise relevant intervals only after understanding the [two clocks](../lifecycle/); a long caller deadline alone does not preserve a silent claim.

## Stream appears all at once

The agent may not be sending partial deltas. Check request state and TTFT. Disable buffering in your reverse proxy and client (`curl -N`). Some applications render only a final answer even while receiving chunks.

## OAuth callback or metadata errors

Set a reachable `public_url` before connecting hosted clients. Match the registered redirect URI exactly, use PKCE S256, and provide the matching client ID when refreshing. A callback fragment is not allowed. See [OAuth](../oauth/).

## Remote commands create the wrong instance

Use `--url` or `SWITCHBOARD_URL`, not `--addr`; address selects a local bind/link host. Remote mode rejects a configured `--db`. Clear `SWITCHBOARD_DB` when moving to remote operation. `mcp add` still configures the local agent, intentionally.

## Supported application but unexpected output

Switchboard forwards parameters; an agent interprets them. A model name is a routing label. Inspect messages, tools, response format, and the agent's answer. A request using Responses, embeddings, image, or audio APIs is outside the [compatibility surface](../api-reference/).
