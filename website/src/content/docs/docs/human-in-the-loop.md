---
title: "Answer a request yourself"
description: "Use manual replies to prototype, debug, and review prompts without a serving agent."
---

A manual answer lets you act as the model for one waiting caller. It is useful when testing an application before connecting an agent, reviewing a prompt, or reproducing a response format.

## From the dashboard

Open **Requests**, choose a queued request, and select **Answer manually**. Read its latest message and context, type the assistant reply, and send it. The caller receives your text as its normal assistant response. The request is attributed to the `manual` channel.

If another channel already owns it, the UI offers a takeover option. Taking over partially streamed work can create an inconsistent answer for the caller; prefer cancelling and having the caller issue a fresh request when possible.

## From the CLI

```sh
switchboard requests pending
switchboard requests show req_REPLACE_WITH_ID
switchboard requests answer req_REPLACE_WITH_ID --text "The answer is 4."
```

For a long reply:

```sh
switchboard requests answer req_REPLACE_WITH_ID --file reply.txt
# Or pipe the assistant response:
printf '%s' 'The answer is 4.' | switchboard requests answer req_REPLACE_WITH_ID
```

A reply file is local to the machine running the command. In [remote mode](../remote/), the CLI sends its contents to the instance; it never asks the server to read your file path. `--force` takes over a held request. Terminal states cannot be answered again.

## Match the caller's expectations

If the request asks for JSON, provide valid JSON without surrounding explanation. If you use an application-specific schema, inspect its `response_format` in the request details. The text-answer CLI/UI is intended for ordinary content; MCP agents can submit structured tool calls with `complete_request`.

## Quick testing without curl

`switchboard send "Your prompt"` creates a request attributed to the internal `switchboard-cli` key and waits. In another terminal, answer its request ID. Use `--model`, `--system`, and `--timeout` to shape the test. This works remotely as well when administration access is configured.
