---
title: "What is Switchboard?"
description: "Understand the application, its boundaries, and the two connections that make it useful."
---

Switchboard is an **OpenAI-compatible chat provider served by MCP-connected agents**. It does not run an inference model. Instead, it holds an application's request until an agent—or an operator—supplies the assistant's answer.

A chat application sends a request to `/v1/chat/completions`. Switchboard authenticates it, records the full request in SQLite, and puts it in a queue. An agent connected to `/mcp` claims that request, reads the conversation and generation parameters, and submits a response. Switchboard wraps the answer in the OpenAI response format and returns it to the waiting application.

## The two connections

| Connection | Who uses it | Address | Credential |
| --- | --- | --- | --- |
| Caller API | Your application, SDK, or script | `http://127.0.0.1:8080/v1` | API key beginning `sk-sb-` |
| Agent MCP | A serving agent | `http://127.0.0.1:8080/mcp` | Agent token or OAuth access token |

These connections do different jobs. Adding Switchboard to an agent's MCP configuration does not automatically start serving. You must ask the agent to run the [claim-and-answer loop](../mcp/).

## When to use it

- Connect an OpenAI-shaped application to a general-purpose agent that already has its own tools and working context.
- Inspect the full prompts an application sends, with a timeline for each request.
- Prototype an interaction before automating it by [answering requests yourself](../human-in-the-loop/).
- Share a queue among several agents, with model matching, concurrency limits, and request priorities.

## What ships

One Go executable runs the chat API, MCP transport, OAuth endpoints, HTMX dashboard, terminal status screen, and management CLI. SQLite is embedded by default; PostgreSQL is an optional backend. See [database selection](../postgres/). The runtime dashboard uses locally bundled assets. This public documentation site is a separate static website hosted on GitHub Pages.

## Compatibility boundaries

Switchboard implements chat completions, legacy completions, and model listing. It does **not** implement the Responses API, embeddings, image generation, transcription, or audio endpoints. A tool using one of those APIs will not work merely by changing its base URL. Generation parameters are passed to the agent; the agent is responsible for honoring them. See the [API reference](../api-reference/).

Start with [installation](../installation/), then complete the [quickstart](../quickstart/). If you are administering an existing deployment, start with [remote operation](../remote/).
