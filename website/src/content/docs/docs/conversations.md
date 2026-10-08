---
title: "Reconstructed conversations"
description: "Understand how request histories become threads and where reconstruction is approximate."
---

An OpenAI chat caller normally includes earlier messages in each new request. Switchboard uses that context to infer which request continues a prior exchange. The caller does not need to send a Switchboard conversation identifier.

## How matching works

Switchboard hashes normalized message prefixes. When a previous request completes, it records a hash of that request's context plus the returned assistant answer. A later request with that prefix continues the same conversation. The longest matching assistant prefix wins. Tool-call turns can participate in the same chain.

When exact matching fails, a continuation containing an earlier assistant message may fall back to the same API key and opening prompt within a 24-hour window. A fresh opening request does not use this fallback; otherwise unrelated chats starting with the same greeting would collapse into one thread.

## Scope and limits

Threads are scoped to the caller's API key. Two apps using different keys do not share inferred threads. Two apps sharing a key may be indistinguishable, so use a key per app.

Reconstruction is an inference, not a client-provided conversation contract. Edited assistant answers, trimmed history, changed system prompts, or branching conversations may split or merge threads through the fallback. Text normalization and tool-call representation also affect matching. Always inspect the raw request when evaluating an apparent mismatch.

## Where to inspect

Open **Conversations** in the dashboard to see titles based on the first user message, turns, model labels, channels, and timing. Select a turn's request ID for its full original context and JSON.

```sh
switchboard conversations list
switchboard conversations show conv_REPLACE_WITH_ID
switchboard requests list --conversation conv_REPLACE_WITH_ID
```

Responses expose `x-switchboard-conversation-id` and `x-switchboard-request-id` headers for correlation. Deleting a conversation record from the dashboard retains request records, but removes that inferred thread's entry. Retention can prune finished requests; see [backups](../backups/).
