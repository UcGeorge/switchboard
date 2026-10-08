---
title: "Architecture and boundaries"
description: "Understand the source layout, database ownership, and local/remote command execution."
---

The Go executable owns all runtime surfaces. Its `app` package opens the selected SQLite or PostgreSQL backend, builds the core service, registers the caller API, MCP transport, OAuth endpoints, management interface, and dashboard, then starts background upkeep and the TUI when enabled.

## Request path

Caller HTTP authentication → request validation and limits → database request/conversation transaction → queue signal → MCP atomic claim → agent output → stored completion and event → waiting caller response.

Live deltas use an in-process registry. Persistent requests and final answers live in the selected database. The server polls for terminal results written by another CLI process, enabling manual answers without sharing process memory.

## Source map

| Package | Responsibility |
| --- | --- |
| `internal/core` | Broker, conversations, channels, keys, OAuth records, settings and metrics |
| `internal/api` | OpenAI-shaped caller routes |
| `internal/mcpserver` | Tools, resources, prompt, authenticated MCP HTTP transport |
| `internal/oauth` | Discovery, registration, consent and token endpoints |
| `internal/control` | Allowlisted remote administration and backup download |
| `internal/web` | Go templates, HTMX assets, live feed and operator actions |
| `internal/cli`, `internal/tui` | Management and terminal status |
| `internal/db` | Embedded migrations, SQL queries, generated sqlc code |
| `website` | Static Astro/Starlight public landing page and docs |

## Database and process model

SQLite runs in WAL mode with immediate write transactions and a busy timeout. PostgreSQL uses native migrations and transaction-scoped advisory locks for serialized domain writes; shared sqlc queries are adapted at the driver boundary. Claim updates are conditional so concurrent agents do not acquire the same queued request. Only one long-running server may own a database. CLI processes can open it for short management operations.

Remote administration invokes an explicit allowlist of commands in isolated copies of the installed binary, using the server database and bounded output. There is no shell command endpoint. MCP registration and skill installation remain client-local.

## What this design does not provide

No distributed queue, multi-node coordination, multi-user tenancy, or managed inference runtime. Agent side effects cannot be made exactly-once by the broker. Request records survive failure; the original caller connection cannot be resumed after server restart. These boundaries are deliberate and should inform deployments.
