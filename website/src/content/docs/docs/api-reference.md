---
title: "Caller HTTP API reference"
description: "Supported routes, authentication, request shapes, response correlation, and error codes."
---

All `/v1` caller routes require `Authorization: Bearer sk-sb-...` or `X-Api-Key`. The base URL used by an SDK includes `/v1`. This reference describes Switchboard's supported subset, not the entire OpenAI platform.

## Chat completions

`POST /v1/chat/completions`

```json
{"model":"switchboard/auto","messages":[{"role":"user","content":"Hello"}],"stream":false}
```

`messages` must be nonempty. Supported roles are system, developer, user, assistant, tool and legacy function. Content can be a string, null for a tool-calling assistant, or content parts forwarded to the agent. Switchboard does not perform image/audio inference on those parts itself.

`model` selects routing. `stream` controls SSE output. `stream_options.include_usage` requests a trailing usage chunk. `n > 1` is rejected. Extra generation fields are preserved in the stored body and forwarded to the agent as parameters.

The non-streaming response uses `chat.completion`, `chatcmpl-...`, a single choice, assistant content/tool calls, finish reason, usage, and a Switchboard system fingerprint. Streaming uses `chat.completion.chunk`, then `[DONE]`. After HTTP headers are sent, errors must travel inside the stream rather than changing HTTP status.

## Legacy completions

`POST /v1/completions` accepts a string or string-array prompt and converts it into chat context for an agent. The response uses `text_completion`. This is a convenience compatibility path; legacy generation features are not an inference implementation.

## Models

`GET /v1/models` lists advertised settings plus concrete model names declared by open channels. Globs are not advertised as model IDs. Listing a name does not guarantee an agent is online or idle for it. `GET /v1/models/{id}` looks up a name subject to the instance's model policy.

## Request status extension

`GET /v1/requests/{id}` reports a request belonging to the authenticated caller key, with status, model, channel, conversation, attempts, timestamps, and completed response when available. Requests from other keys are not exposed through this route.

## Correlation headers

- `x-request-id` / `x-switchboard-request-id`: stored request ID.
- `x-switchboard-conversation-id`: inferred conversation ID.
- `openai-processing-ms`: completed non-streaming latency.
- Rate-limit headers and `Retry-After` where applicable.

## Errors

| HTTP | Code | Cause |
| --- | --- | --- |
| 400 | `invalid_request` | Invalid JSON or unsupported request shape |
| 401 | `missing_api_key`, `invalid_api_key`, `api_key_revoked` | Caller authentication |
| 403 | `model_not_allowed` | Key model policy |
| 404 | `model_not_found`, `unknown_url`, `request_not_found` | Unsupported route/model or inaccessible request |
| 413 | `body_too_large` | Body exceeds 16 MiB |
| 429 | `rate_limit_exceeded`, `queue_full` | Backpressure |
| 502 | `agent_error` | Serving failure |
| 503 | `request_cancelled` | Operator cancellation |
| 504 | `timeout` | Deadline |

Error JSON contains an `error` object with message, type, param and code. `/healthz` is a separate public operational route. Administration uses `/admin/commands` and `/admin/backup` with a distinct token; use the [remote CLI](../remote/) rather than caller credentials.
