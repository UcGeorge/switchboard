package mcpserver

// AgentGuide is the serving manual exposed as an MCP resource, used by the
// `serve` prompt, and embedded in the installable Claude Code skill.
const AgentGuide = `# Serving requests from Switchboard

Switchboard is an OpenAI-compatible API whose "model" is you. Applications
call its /v1/chat/completions endpoint; those requests wait in a queue until
an agent claims them over MCP, answers, and the answer is returned to the
caller. Your job is to run that loop.

## The loop

1. **open_channel** once per session. Pick a stable, descriptive name for this
   agent instance (for example "claude-desktop" or "worker-2"). Declare the
   models you will serve (default ["*"] = anything) and how many requests you
   can work on at once (default 1). Keep the returned channel_id.
2. **claim_request** with your channel_id and wait_seconds (use 25). It
   long-polls: when a request is available it is returned to you; when the
   wait elapses with nothing queued it returns found=false. Just call it
   again. Every claim also counts as a heartbeat.
3. Read the claimed request: messages (the full chat history in OpenAI
   format), the requested model, generation params (temperature, tools,
   response_format, ...) and the deadline. Produce the assistant's reply to
   the last message exactly as the model named would: match the requested
   format, honour tools by returning tool_calls, respect max_tokens, and do
   not add commentary about Switchboard.
4. Deliver the reply with **complete_request** (content, optional tool_calls,
   finish_reason). For long answers, call **stream_delta** repeatedly with
   partial text first so the caller sees tokens arrive, then finish with
   complete_request (content may then be empty: the streamed text is used).
5. If you cannot answer, call **fail_request** with a reason. retryable=true
   returns it to the queue for another agent; retryable=false fails it for
   the caller. Use **release_request** when you are shutting down and want to
   hand work back without blame.
6. Repeat from step 2. When you are done serving, call **close_channel**.

## Rules that keep callers happy

- Answer only the last user/tool turn; the earlier turns are context.
- Never invent tool calls the request did not define in params.tools.
- If params.response_format asks for JSON, return only valid JSON.
- Work fast: each claim has a lease. If you need longer, call stream_delta or
  extend_lease to renew it, otherwise the request is handed to someone else.
- Check the deadline on the request; if it has passed, release it.
- A request can be cancelled by its caller while you work. complete_request
  then returns an error saying the request is cancelled: drop it and move on.
- Do not stop the loop because the queue is empty. Keep claiming with
  wait_seconds=25 until you are told to stop.

## Useful extras

- **queue_status** shows what is waiting and which models are requested.
- **get_request** shows the current state of a request you hold.
- **heartbeat** keeps your channel online while you are idle and not polling.
- Resource switchboard://status is the same information as queue_status.
`
