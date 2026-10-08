---
title: "MCP tool reference"
description: "Tool inputs and results for agents implementing the serving protocol."
---

Transport: Streamable HTTP at `/mcp`. Authentication: static or OAuth bearer token. Tools accept JSON objects; failures caused by domain state usually return an MCP tool result with `isError: true` and instructions rather than an HTTP transport error.

## Channel tools

| Tool | Inputs | Result / effect |
| --- | --- | --- |
| `open_channel` | `name` required; `models` default `["*"]`; `concurrency` default 1; optional `description` | Channel ID, status, model list, capacity, queue depth |
| `heartbeat` | `channel_id` | Refresh channel activity and return status |
| `close_channel` | `channel_id`, optional `reason` | Close channel and release held work according to retry policy |
| `list_channels` | Empty object | Open channels and serving state |

Concurrency is clamped to 1–256. Names must be nonempty and at most 80 characters. Each agent should keep its channel ID for the session.

## Claim and inspection

`claim_request` requires `channel_id`; `wait_seconds` defaults to 25 and is capped at 55. If no eligible request arrives, it returns `found: false`. Poll again to stay serving.

A found request includes its ID, conversation ID, turn index, model, endpoint, stream preference, attempt count, caller label, full messages, generation params, deadline and lease expiration. Read the deadline before spending time on work.

`get_request` takes `request_id` and returns current state, ownership, attempts, deadline and error. `queue_status` takes an empty object and returns queued/in-flight counts, online channels, oldest queue age and models waiting.

## Output tools

`stream_delta` requires channel and request IDs, with optional text `content` and `tool_calls`. Each tool-call object has a function name and arguments, with optional ID/type defaults. It appends partial output and renews the lease. Partial tool calls should be treated carefully; submit full tool calls rather than overlapping fragments.

`complete_request` requires channel and request IDs and accepts:

- `content`: final text, or empty to use previously streamed text.
- `tool_calls`: complete calls; omitted IDs/types are filled in.
- `finish_reason`: inferred `stop` or `tool_calls` when omitted.
- `refusal`: refusal text when applicable.
- `usage`: optional `prompt_tokens` and `completion_tokens`; total is computed.

Function arguments may be a JSON-encoded string or an object; objects are serialized to the OpenAI string form. Provide consistent final content after streaming: the caller cannot retract text already sent. See [lifecycle](../lifecycle/).

## Recovery tools

- `extend_lease`: channel/request IDs; renews held work.
- `fail_request`: IDs, `reason`, optional `retryable` (false by default).
- `release_request`: IDs and optional reason; returns work when retry-safe.

Retryable does not mean guaranteed retry: attempts and streamed output determine whether requeueing is safe. Completing terminal work returns an error; drop it and move on.

## Prompt and resources

The `serve` prompt accepts optional `channel_name` and `models` (comma-separated). Resources are `switchboard://guide` (serving instructions), `switchboard://status` (JSON queue status), and `switchboard://channels` (JSON channel overview). These help an agent learn the loop; they are not an autonomous worker process.
