---
title: "Run with Docker Compose"
description: "Deploy SQLite, bundled PostgreSQL, or an external database with optional HTTPS."
---

Docker Compose is another way to run the same application. It needs Docker Engine with the Compose plugin. The repository's Dockerfile builds a static Go executable; the runtime runs as a non-root user and includes a PostgreSQL backup client.

## Start the default SQLite instance

```sh
cp .env.example .env
docker compose up -d --build --wait
docker compose logs switchboard
```

The application is bound to host loopback at `http://127.0.0.1:8080`. A named volume retains SQLite data and settings. The first-run login information appears in logs unless you supplied a dashboard password. Logs may contain access links—keep them private.

## Set secrets and settings

Edit `.env` before first start. It is excluded from Git. Use distinct values for `SWITCHBOARD_ADMIN_PASSWORD` and `SWITCHBOARD_CONTROL_TOKEN`; an administration token is not an MCP or caller credential. The password should be strong and unique; `openssl rand -hex 32` can generate an administration token and PostgreSQL password.

Runtime settings can be supplied as `SWITCHBOARD_<SETTING_NAME>` environment variables, including public URL, lease, timeout, stale-channel interval and retention. Environment values override stored settings while present. A dashboard or CLI edit to the stored value will not override a deployment environment variable. Changing Compose environment requires recreating the container; the server reloads database-stored settings without a restart.

`SWITCHBOARD_ADMIN_PASSWORD` applies the configured dashboard password at startup. Remove it if you want the dashboard/CLI to own subsequent password changes instead. `SWITCHBOARD_CONTROL_TOKEN` takes precedence over a token minted into the database.

## PostgreSQL on the same host

```sh
docker compose -f compose.yaml -f compose.postgres.yaml up -d --build --wait
```

Set `POSTGRES_PASSWORD` first. Use URL-safe characters because the internal connection URL is assembled from it. Its named volume persists across deployments. Updating `POSTGRES_PASSWORD` in `.env` does not change an existing database account password: explicitly rotate the PostgreSQL role password before updating the app's connection configuration.

For an external database, use only `compose.yaml` and set `SWITCHBOARD_DATABASE_URL`. Read [PostgreSQL](../postgres/) for TLS, migrations and backup details.

## Optional public HTTPS

Set `SWITCHBOARD_DOMAIN` to a DNS name resolving to the host and `SWITCHBOARD_PUBLIC_URL=https://that-name`. Open TCP 80/443 in both the cloud security rules and the OS firewall. Then add the TLS overlay:

```sh
docker compose -f compose.yaml -f compose.postgres.yaml -f compose.tls.yaml   up -d --build --wait
```

Caddy obtains TLS certificates and streams responses through to Switchboard. The application's host binding stays loopback; public traffic enters through Caddy. For SQLite, omit the PostgreSQL overlay.

Without a domain, an SSH tunnel keeps access private:

```sh
ssh -L 8080:127.0.0.1:8080 your-user@your-host
```

Open `http://localhost:8080` locally. Hosted agents need a genuinely reachable HTTPS URL; they cannot use your laptop's tunnel address.

## Management and maintenance

```sh
docker compose exec switchboard switchboard keys list
docker compose exec switchboard switchboard db path
```

Compose exec inherits database configuration from the container. Alternatively use the [remote CLI](../remote/) with the administration token. Health checks verify database access, not availability of an eligible serving agent.

To upgrade, back up, update the checkout, and run `up -d --build --wait` with the same files and project name. `docker compose down` stops containers; **do not use `down -v` unless you deliberately intend to delete persistent database volumes**. A crash restarts the server via `unless-stopped`, but the old caller connections cannot be resumed.
