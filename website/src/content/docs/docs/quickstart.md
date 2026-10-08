---
title: "Your first request"
description: "Complete a caller-to-answer round trip locally, with or without an agent."
---

This walkthrough assumes [Switchboard is installed](../installation/). Keep the server running in terminal A. Terminal B creates credentials and sends the caller request; use terminal C (or the dashboard) to answer while that request waits.

## 1. Start the instance

In terminal A:

```sh
switchboard --open
```

The default addresses are the dashboard at `http://127.0.0.1:8080`, the caller API at `/v1`, and the agent MCP endpoint at `/mcp`. Keep terminal A open. Press `o` to open the dashboard, `l` for a fresh login link, or `q` to stop.

If you are running through SSH or a process supervisor, use `switchboard serve --headless`. It prints the same connection information without a terminal screen.

## 2. Create a caller credential

In terminal B:

```sh
switchboard keys create --name quickstart-app
```

Copy the full `sk-sb-...` key immediately. It is displayed only once. The name identifies your application in request history; it is not the credential. Export the values in terminal B, replacing the example key with your real one:

```sh
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
export OPENAI_API_KEY=sk-sb-your-real-key
```

## 3. Send a request

```sh
curl "$OPENAI_BASE_URL/chat/completions"   -H "Authorization: Bearer $OPENAI_API_KEY"   -H "Content-Type: application/json"   -d '{"model":"switchboard/auto","messages":[{"role":"user","content":"What is 2 + 2?"}]}'
```

The call waits. That is expected: no serving agent has answered yet. Open another terminal, or use the dashboard to answer it.

## 4. Deliver a manual answer

In terminal C:

```sh
switchboard requests pending
switchboard requests answer req_REPLACE_WITH_THE_LISTED_ID --text "4"
```

The waiting curl call returns a `chat.completion` object. Its first choice contains an assistant message with `content: "4"`. CLI answers reach the running caller through database polling, normally within about two seconds.

In the dashboard, open **Requests**, select the request, and inspect its messages, timings, answer, and timeline. **Conversations** shows the reconstructed thread.

## 5. Automate answering with an agent

For Claude Code:

```sh
switchboard mcp add claude-code
switchboard skill install
```

Start a Claude Code session with that MCP server loaded, then ask: **Serve Switchboard requests.** The skill teaches the agent to open a channel and repeatedly claim and answer requests. A channel should appear in the dashboard. Adding the MCP configuration alone is not sufficient.

For other clients, read [client configuration](../clients/) or use the [MCP serving guide](../mcp/).

## 6. Verify streaming

Repeat the curl request with `"stream":true` in its JSON body and `curl -N` to disable output buffering. The caller receives server-sent chunks when the agent calls `stream_delta`, then `[DONE]` when completed. If the agent answers all at once, Switchboard still returns a valid SSE response.

Next: [connect a real application](../openai/), [understand channels](../concepts/), or [manage a remote instance](../remote/).
