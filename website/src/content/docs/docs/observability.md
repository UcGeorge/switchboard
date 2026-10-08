---
title: "Observe latency and activity"
description: "Use live gauges, stored events, and request timings to diagnose bottlenecks."
---

Switchboard observes the path between a caller and a serving channel. It cannot see the internal reasoning, tool timing, or true token consumption of an external agent unless that agent reports it.

## Timing definitions

| Measurement | Calculation | What it suggests |
| --- | --- | --- |
| Queue time | Claim timestamp minus creation | Agent capacity/routing availability |
| Time to first token (TTFT) | First output minus creation | Queue plus delay to first output |
| End-to-end latency | Completion minus creation | Whole caller wait |
| Channel average latency | Sum of completed request latency / served count | Historical channel behavior |

A non-streaming agent's completion acts as its first output. Thus its TTFT may equal total latency. Values are wall-clock durations and include queued time; they are not model-only inference latency.

## Live gauges versus windows

Queued, in-flight, and online-channel counts reflect current records. Throughput and usage aggregate over a selected window. Percentiles use completed requests and are bounded to the latest 5,000 latency samples in that window. Failed or expired work does not contribute completed latency samples.

Success rate considers completed, failed, cancelled, and expired outcomes in the window. A burst of operator cancellations can reduce it without any agent inference failure. Keep numerator and outcome mix visible when interpreting it.

## Token usage

Prompt/completion values are estimates from text length unless the agent supplies actual counts. The dashboard marks estimates. These are useful for rough context comparisons, not authoritative cost or billing records. Tool-call arguments contribute to estimates.

## Events

State transitions are persisted before fan-out. Use a request timeline or:

```sh
switchboard events list --request req_ID
switchboard events list --channel ch_ID
switchboard events tail
```

Token deltas are ephemeral live events rather than one stored event per text fragment. Final request and response records preserve completed content. Remote event tail uses polling, with a bounded listing window; inspect stored events directly when investigating a high-volume burst.

## Health endpoint

`/healthz` returns uptime, version, queued count, in-flight count and online channels. It is intentionally reachable without a dashboard session and reveals aggregate operational information. A healthy database with zero eligible agents still cannot answer callers.

## Diagnostics sequence

1. Is the caller authenticated and calling the supported endpoint?
2. Is the request queued or claimed? Inspect key pinning and model patterns.
3. Is the channel draining, stale, or at concurrency capacity?
4. Has a lease expired or a deadline elapsed? Inspect its timeline.
5. Is a proxy buffering output or cutting the connection short?

See [troubleshooting](../troubleshooting/) for recovery actions.
