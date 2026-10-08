---
title: "Connect an OpenAI-compatible application"
description: "Configure base URLs, credentials, models, streaming, and tool-calling behavior."
---

Start the server and create an API key before configuring a caller. If you have not done that, follow the [quickstart](../quickstart/). At least one agent must serve the requested model, or you must [answer manually](../human-in-the-loop/).

## Base URL and key

Set the application's provider base URL to **`http://127.0.0.1:8080/v1`**, including `/v1`, and use the full `sk-sb-...` key. This is not the MCP endpoint. For a remote server, replace the host with its HTTPS address.

Some tools use environment variables; others have a settings screen. Verify that the tool actually accepts a custom base URL and uses **chat completions**, rather than the Responses API or embeddings.

```sh
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
export OPENAI_API_KEY=sk-sb-your-key
```

## Python SDK

Install the `openai` package in your application environment. Pass the base URL explicitly if you are unsure whether your framework reads environment variables:

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8080/v1", api_key="sk-sb-your-key")
answer = client.chat.completions.create(
    model="switchboard/auto",
    messages=[{"role": "user", "content": "Explain this error briefly."}],
)
print(answer.choices[0].message.content)
```

The call blocks while queued and served. Your SDK timeout must exceed the time you intend to allow Switchboard. Automatic SDK retries can submit duplicate requests after failures; use explicit request timeouts and review your client's retry policy.

## JavaScript SDK

```js
import OpenAI from 'openai';
const client = new OpenAI({
  baseURL: 'http://127.0.0.1:8080/v1',
  apiKey: 'sk-sb-your-key',
});
const response = await client.chat.completions.create({
  model: 'switchboard/auto',
  messages: [{ role: 'user', content: 'Hello' }],
});
console.log(response.choices[0].message.content);
```

Do not embed your API key in a publicly distributed frontend. The caller API permits browser cross-origin requests, but a shipped secret can be extracted by users.

## Streaming

Set `stream: true`. Switchboard emits OpenAI-shaped SSE chunks, an optional usage chunk when `stream_options.include_usage` is true, and `[DONE]`. Partial output only starts when the serving agent emits a delta. While waiting, periodic SSE comments keep the connection alive.

## Tools and response formats

Switchboard forwards `tools`, `tool_choice`, `response_format`, and generation parameters to the agent. The serving agent decides the answer and must follow those instructions. Switchboard does not run tools supplied by the caller. A tool-call answer goes back to the caller, which normally executes the tool and submits its result in the next request.

This is format compatibility, not equivalent model behavior. An agent's tool access and capabilities may differ from the requested model label. See [MCP serving](../mcp/) and the [API reference](../api-reference/).
