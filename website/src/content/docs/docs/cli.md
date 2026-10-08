---
title: "CLI command guide"
description: "Know which commands run locally, which address the instance, and how to get machine output."
---

Run `switchboard --help` for the command tree and `switchboard COMMAND --help` for options. The default mode starts a local instance with the terminal UI. A server process must stay running for caller HTTP requests and MCP agents to connect.

## Select your instance

Local management uses the database at `~/.switchboard/switchboard.db`. `--db /path/instance.db` selects another file. You can manage it even while the server is stopped. `--addr` controls where a local server listens and the URLs printed by local connection commands.

Remote management uses `--url https://your-instance.example` or `SWITCHBOARD_URL`, plus an administration token. It never opens a client-local instance database. See [remote setup](../remote/). Do not combine `--db` with `--url`.

## Commands

| Group | Operations | Purpose |
| --- | --- | --- |
| `serve` | `--headless`, `--open`, `--addr` | Start the server locally |
| `keys` | `list`, `create`, `revoke`, `delete` | Caller credentials |
| `tokens` | `list`, `create`, `revoke`, `delete` | Agent credentials |
| `channels` | `list`, `show`, `drain`, `resume`, `close`, `delete` | Agent instances |
| `requests` | `list`, `pending`, `show`, `answer`, `cancel`, `requeue` | Requests and operator intervention |
| `conversations` | `list`, `show` | Inferred threads |
| `send` | Prompt with optional model/system/timeout | Test a round trip |
| `events` | `list`, `tail` | Persistent event history |
| `stats` | `--window 24h` | Aggregate usage and latency |
| `settings` | `list`, `get`, `set` | Runtime configuration |
| `auth` | `set-password`, `login-link`, `logout-all`, `create-admin-token` | Operator access |
| `mcp` | `url`, `config`, `add` | Configure local clients for a selected instance |
| `skill` | `install`, `print` | Serving instructions on the local machine |
| `db` | `path`, `backup`, `prune`, `vacuum` | Storage maintenance |
| `version`, `completion` | Version and shell completions | Local CLI tooling |

`auth create-admin-token` and `serve` are server-local operations. Skill installation and client registration are local even when the selected instance is remote. `db backup` remotely downloads into a new client-local file.

## Create and restrict a key

```sh
switchboard keys create --name editor --rpm 60 --models 'gpt-4*'   --timeout 300 --priority 5
```

Use `--pin ch_CHANNEL_ID` only when that caller must wait for one particular channel. A pinned offline channel can leave work queued while other channels are free.

## Inspect work

```sh
switchboard requests list --status queued --limit 50
switchboard requests list --key key_ID --model gpt-4o
switchboard requests show req_ID --raw
switchboard events list --request req_ID
switchboard channels list --all
```

`--json` produces JSON for listings and creation commands; it is not a guarantee that every mutation prints a JSON envelope. Creation output includes full credentials once. Treat captured JSON as sensitive.

## Passwords and sessions

Local `auth set-password` prompts when no argument is supplied. Remote password changes require `--password`. Avoid storing that command in shell history. `auth login-link` creates a single-use dashboard link; `auth logout-all` invalidates existing dashboard sessions without deleting caller or agent credentials.

## Failure behavior

Errors return a nonzero exit status. Remote authentication errors include the HTTP status. Ctrl+C stops local servers or long-running CLI operations. Cancellation of a remote send also cancels its server-side process and request; an already committed mutation is not rolled back just because the CLI disconnects.
