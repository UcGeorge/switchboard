---
title: "Self-host Switchboard"
description: "Run locally, under a service manager, or behind a TLS proxy and public hostname."
---

Switchboard is a single-server application backed by SQLite or PostgreSQL. Use one process per database; do not scale replicas over a shared volume. This public GitHub Pages site hosts documentation only. GitHub Pages cannot run the Switchboard server or store its live database.

## Local desktop

```sh
switchboard --open
```

The default loopback bind limits access to your machine. Leave it that way for local applications and agents. A local CLI can manage the same database while the TUI keeps the server running.

## Linux service

Install the executable, create a dedicated user/data directory, and use a process supervisor. Example systemd unit:

```ini
[Unit]
Description=Switchboard agent-backed chat provider
After=network.target

[Service]
User=switchboard
Group=switchboard
ExecStart=/usr/local/bin/switchboard serve --headless --addr 127.0.0.1:8080 --db /var/lib/switchboard/switchboard.db
Restart=on-failure
WorkingDirectory=/var/lib/switchboard
UMask=0077
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

Paths and the service user are deployment choices; create them before starting the unit. Set credentials with a root-protected environment file or your secret manager. Run CLI management under a user that can access the same database.

## Container

The repository's Dockerfile builds a static executable and runs it as a non-root user. Persist `/data` on a writable volume. The container listens on `0.0.0.0:8080`; publish it only to host loopback when a host proxy handles TLS.

```sh
docker build -t switchboard .
docker run --name switchboard -p 127.0.0.1:8080:8080   -v switchboard-data:/data switchboard
```

Use `docker exec switchboard switchboard --db /data/switchboard.db auth create-admin-token` to bootstrap remote administration. The binary is on the container PATH. Then manage the instance through [remote mode](../remote/).

## TLS reverse proxy

Example Caddy configuration:

```text
switchboard.example.com {
  reverse_proxy 127.0.0.1:8080 {
    flush_interval -1
  }
}
```

Set the instance's external URL:

```sh
switchboard settings set public_url https://switchboard.example.com
```

Your proxy must preserve the public Host and set trusted forwarded scheme headers. Disable buffering for caller streams, MCP streams and the dashboard's SSE feed. Configure proxy timeouts to cover your request deadlines. Do not pass attacker-supplied forwarded headers through unchanged.

## Tunnels and hosted agents

A tunnel can expose an otherwise local instance. Use its stable HTTPS hostname as `public_url`, then configure the hosted MCP client with that URL. A cloud-hosted agent cannot connect to your private loopback address. Review [security](../security/) before making the endpoint reachable.

## Health and upgrades

`GET /healthz` checks database access and reports queue counts, online channels, version and uptime. Health does not mean an agent is available for every model. Back up before upgrading, stop the old process, replace the binary, and restart. Recovery cancels caller work whose connections were lost; see [backups](../backups/) and [lifecycle](../lifecycle/).

## Compose and deployment targets

Use the [Compose guide](../compose/) for SQLite, bundled PostgreSQL or an external database, and the [Keel deployment](../keel/) for typed SSH deployment inputs. [Host selection](../hosting/) explains zero-cost candidates and their limits.
