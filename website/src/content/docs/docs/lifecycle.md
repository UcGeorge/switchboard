---
title: "Request lifecycle and reliability"
description: "Understand leases, deadlines, retries, cancellation, and restart recovery."
---

Switchboard stores a request before making it claimable. The caller stays connected until a terminal result, disconnect, or timeout. This is a durable queue behind a synchronous chat API, not an asynchronous job API that the caller can resume after disconnecting.

## States

| State | Meaning | Normal next state |
| --- | --- | --- |
| `queued` | Waiting for an eligible agent | `claimed`, `cancelled`, `expired` |
| `claimed` | One channel owns the work under a lease | `streaming`, `completed`, `queued`, `failed` |
| `streaming` | Partial output has been received | `completed`, `failed`, `cancelled`, `expired` |
| `completed` | Final answer recorded and delivered | Terminal |
| `failed` | Agent or retry policy could not finish | Terminal |
| `cancelled` | Caller disconnected or operator cancelled | Terminal |
| `expired` | Request deadline passed | Terminal |

## Claim lease

A claim has a lease, default **120 seconds**. `stream_delta`, `extend_lease`, or completion shows progress. If the agent goes silent beyond the lease, the reaper attempts recovery. The agent must call `extend_lease` during long reasoning; a channel heartbeat alone does not do this.

## Retry budget

The default maximum is **three claims**. A retryable failure or an expired lease requeues only when attempts remain and no partial output has been received. Otherwise the request fails. A retry may go to the same channel or another eligible channel; it is not guaranteed to use a different agent.

Once partial output has arrived, a retry would risk sending two different answers to one caller. Switchboard fails that request instead. It does not promise exactly-once execution of an agent's external tools; an agent may have performed actions before its lease expires. Design serving agents accordingly.

## Deadline

The global timeout is **600 seconds**, overridable by API key. Time spent queuing counts toward the deadline. Agents receive the deadline in the claim payload. Expiration gives a non-streaming caller `504 timeout`. A stream that already has HTTP 200 carries an error event followed by `[DONE]`.

## Cancellation and shutdown

When the caller disconnects, Switchboard cancels the request. An agent that later completes it receives an actionable tool error and should drop the work. An operator can also cancel or requeue through the dashboard and CLI.

On restart, active requests from the previous server run are cancelled because their original caller connections were lost. Open agent channels become offline until they return. Histories remain available. A recorded completed answer is not deleted by a restart.

## Capacity and backpressure

Per-key rate limits and a configurable queue depth return `429` with `Retry-After`. Channel concurrency bounds simultaneous claims. SQLite uses WAL mode and a busy timeout; its single writer limits throughput. This is designed for local agent pools, not a horizontally scaled inference gateway.

Use [observability](../observability/) to distinguish queue time from agent processing time, and [settings](../settings/) to adjust defaults.
