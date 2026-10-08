---
title: "Manage a remote deployment"
description: "Administer a remote instance and configure local agents without sharing its database."
---

Remote mode sends instance commands over authenticated HTTP. It does not mount SQLite or SSH into the server. Use the **instance base URL**, without `/v1` or `/mcp`.

## 1. Enable administration on the server

On the server machine, against the database the server uses:

```sh
switchboard auth create-admin-token
```

Copy the generated `sbc_...` token. It is shown once and stored hashed. Running the command again rotates it; a running server picks up the change within a couple of seconds.

For a managed deployment, the server may instead read a secret from `SWITCHBOARD_CONTROL_TOKEN`. That override takes precedence over the database token. Keep it in the deployment's secret store, not an image or committed compose file.

## 2. Select the instance on your laptop

```sh
export SWITCHBOARD_URL=https://switchboard.example.com
export SWITCHBOARD_ADMIN_TOKEN=sbc-your-administration-token
switchboard stats
switchboard keys list
```

Equivalent flags are `--url` and `--admin-token`. Prefer an environment-provided token so it does not appear in command history. Beyond loopback, use HTTPS and a trusted certificate. The client refuses redirects rather than forwarding its credential to a new destination.

## 3. Connect a local agent

```sh
switchboard mcp add claude-code
switchboard skill install
```

The agent token is minted on the remote instance; `claude mcp add` executes on your laptop. The skill is installed beside your local agent. If you already have an agent token, `mcp config/add --token sba_...` and `mcp url` do not need administration access.

## Operation boundaries

Keys, agent tokens, channels, requests, conversations, settings, stats, events, password changes, login links, and maintenance address the remote server. `serve` and `auth create-admin-token` must run on the server itself.

```sh
switchboard requests answer req_ID --file reply.txt
switchboard events tail
switchboard db backup backup.db
```

The reply file is read on your laptop. A backup is downloaded into a new local file; an existing file is never overwritten. `db path` reports the remote server's filesystem path for diagnosis. `db prune` and `db vacuum` run there.

## Administration is not agent authorization

The administration token grants operator-level access to request histories, keys, settings, passwords, and backups. Do not place it in MCP client configuration. An agent needs an `sba_` token or OAuth access token; a caller needs an `sk-sb-` API key. See [security](../security/).

## Operational limits

The control interface uses an explicit command allowlist and isolated copies of the same installed executable, never a shell. Commands have bounded output and a 15-minute execution limit. Large histories should be queried with `--limit` and filters. Event following polls once a second and can miss bursts beyond its listing window. Keep server and CLI on matching releases for compatible flags.
