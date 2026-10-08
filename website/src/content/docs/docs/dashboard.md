---
title: "Use the operator dashboard"
description: "Navigate queue health, requests, inferred threads, credentials, and runtime settings."
---

The runtime dashboard lives on your instance, not on this public documentation website. Open the instance URL printed in the terminal. Sign in with its dashboard password or a one-time link from `switchboard auth login-link`. The generated first-run password is shown once; [reset it locally](../security/) if needed.

## Overview

The overview separates live gauges from windowed measurements. **Queued**, **in flight**, and **online channels** describe current state. Throughput, success rate, token counts, and latency percentiles describe the selected trailing window. Empty windows show no samples rather than a measured zero latency.

Use queue time and time-to-first-token to diagnose whether an agent is unavailable or simply slow; see [observability](../observability/).

## Requests

Filter by status, API key, channel, model, conversation, or text. A request row shows its caller, serving channel, latest prompt/answer preview, status, latency, and token counts. Select its ID for the full context and response.

The detail page contains messages, tool calls, live output, generation parameters, raw request/response JSON, and a timestamped timeline. Available actions include manual answer, requeue, and cancel. Read [lifecycle](../lifecycle/) before requeueing streamed output.

## Conversations and channels

**Conversations** reconstructs exchanges from resubmitted history. It can be approximate; read [conversation matching](../conversations/) before treating it as a client session identifier.

**Channels** shows agent status, model patterns, concurrency, served and failed counts, and average latency. Drain stops new claims while allowing current work to finish. Closing releases its current work subject to retry safety. Editing channel properties can be overwritten by that agent's next `open_channel` call.

## Access and system

**API keys** manages caller credentials, limits, allowed models, priority, timeout overrides, and pinning. Full secrets are shown once.

**Agents** manages static tokens and OAuth-issued tokens and lists registered OAuth clients. Removing a client registration does not automatically revoke all its existing token records; revoke those explicitly.

**Events** is the persistent audit trail. **Settings** changes runtime tuning, password, sessions, and retention. **Connect & docs** contains instance-specific connection snippets.

## Live updates

The dashboard receives events over SSE and refreshes affected sections with HTMX. Hidden tabs release their streams and catch up when visible again, avoiding exhaustion of HTTP/1.1 browser connections. If the live indicator goes offline, verify the server and proxy rather than assuming the displayed values are current.
