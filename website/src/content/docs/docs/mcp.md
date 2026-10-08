---
title: "Serve requests over MCP"
description: "Run the agent loop and keep request leases and channels healthy."
---

MCP is the interface used by serving agents. Switchboard supports **Streamable HTTP** at `/mcp`. It authenticates every request with a static agent token or OAuth access token. The endpoint is not a stdio command.

## Connect the agent

For a static token, create it through **Agents** in the dashboard or:

```sh
switchboard tokens create --name my-serving-agent
```

Configure the MCP URL with `Authorization: Bearer sba_...`. Token creation displays the full value once. For a client that supports discovery and OAuth, add the MCP URL and approve access through the dashboard; see [OAuth](../oauth/).

## Start the serving loop

Ask the connected agent to serve Switchboard requests. It can use the MCP prompt `serve`, read `switchboard://guide`, or follow the installed `switchboard-agent` skill.

1. Call `open_channel` once with a descriptive name, model patterns, and concurrency. Keep the returned `channel_id`.
2. Call `claim_request` with that channel and `wait_seconds: 25`. When `found` is false, poll again. Empty queues do not mean the serving session should stop.
3. Read the returned `messages`, `params`, model label, deadline, and lease expiration. Produce the assistant reply to the last turn using earlier turns as context.
4. Optionally call `stream_delta` with partial text. It renews the lease and feeds the waiting caller.
5. Call `complete_request` with the final answer. If all text was already streamed, content can be empty and the accumulated text is used.
6. Continue claiming. On shutdown, call `close_channel`.

## Serving contract

Return the requested format without commentary about Switchboard. Use only tool definitions the caller supplied when returning `tool_calls`. If JSON output is requested, return valid JSON. Parameters such as `max_tokens` and temperature are forwarded instructions, not enforcement by a model runtime inside Switchboard.

## Long reasoning and errors

Use `extend_lease` before the claim lease expires. Use a channel `heartbeat` when idle without polling. These are different operations; channel activity alone does not renew held requests. The defaults are described in [lifecycle](../lifecycle/).

If the agent cannot answer, `fail_request` with `retryable: true` permits another attempt if safe. `retryable: false` returns an error to the caller. `release_request` hands work back when shutting down. A cancelled, expired, or completed request cannot be completed again; drop it and claim the next.

## Agent access is broad

An authorized agent can read queued requests across caller keys. Channels restrict ownership of held work, but there are no per-caller agent isolation roles. Approve only trusted serving agents. See [security](../security/) and the [tool reference](../mcp-reference/).
