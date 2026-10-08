---
title: "Use PostgreSQL"
description: "Select the PostgreSQL backend, manage its schema and credentials, and back it up."
---

SQLite remains the zero-configuration default. PostgreSQL is an optional persistent backend for operators who already manage a database or prefer a database service. PostgreSQL does not turn Switchboard into a multi-replica service: run one API/MCP server per instance because caller connections and live deltas are held in memory.

## Version requirement

PostgreSQL support requires **Switchboard v0.2.0 or newer**. Install a current
release, or run `switchboard update` if your installed version already includes
the updater. For a v0.1.0 installation, rerun the installer once; that release
predates the `update` command. See [installation](../installation/) and
[updating Switchboard](../updates/).

## Connect a local binary

```sh
export SWITCHBOARD_DATABASE_URL='postgresql://switchboard:URL_ENCODED_PASSWORD@db.example.com:5432/switchboard?sslmode=verify-full'
switchboard serve --headless
```

`--database-url` is equivalent, but an environment variable keeps credentials out of command-line history. `DATABASE_URL` is a fallback when `SWITCHBOARD_DATABASE_URL` is unset. A configured PostgreSQL URL takes precedence over the SQLite `--db` path. Local management commands must use the same database selection as the running instance.

Use `postgres://` or `postgresql://`. URL-encode reserved characters in the username/password. Prefer `sslmode=verify-full` with a trusted CA for an external service. `sslmode=disable` is appropriate only for the private database network in the bundled Compose example.

Create the database and an account authorized to create and modify its tables before starting Switchboard. Startup applies native PostgreSQL migrations under a transaction-scoped advisory lock. Ordinary server operations then use the shared sqlc query definitions through a PostgreSQL dialect adapter; conditional updates and transaction locking preserve claim ownership and per-channel capacity.

## Bundled PostgreSQL

```sh
cp .env.example .env
# Set POSTGRES_PASSWORD to a strong URL-safe value in .env, for example
# a value produced by: openssl rand -hex 32
docker compose -f compose.yaml -f compose.postgres.yaml up -d --build --wait
```

The overlay starts PostgreSQL 17 with a persistent named volume and waits for database health before starting Switchboard. PostgreSQL is not published to the host network. App credentials and other runtime settings are described in [Compose deployment](../compose/).

## Existing PostgreSQL

Use the base Compose file alone and set `SWITCHBOARD_DATABASE_URL` in `.env`. Do not include the bundled PostgreSQL overlay when selecting an external database; the overlay intentionally supplies its own internal connection URL.

New installations create schema in the database selected by the URL. Changing the URL selects different data—it does not copy your SQLite histories, credentials or settings into PostgreSQL. There is currently no automatic cross-engine data migration. Back up the original database and retain it before switching.

## Backup and restore

`switchboard db backup backup.dump` produces a PostgreSQL **custom-format logical dump**, rather than a SQLite file. A compatible `pg_dump` must be on PATH where the backup executes; the Docker image includes PostgreSQL 17's client. An external database newer than the bundled client requires a matching/newer client or a provider-native backup.

Remote `db backup` downloads the dump to your laptop. Credentials are passed to the backup subprocess through environment variables, not command arguments. Protect the dump as sensitive conversation data.

Restore with a compatible `pg_restore` into a stopped instance's database. Test the restore in a disposable database first. Restoring can resurrect old credential/session state; rotate credentials as needed. `db vacuum` runs PostgreSQL maintenance, while `db prune` removes history using the normal retention policy. Provider backups and point-in-time recovery are separate from this logical export.

Supported-backend CI exercises broker transactions, concurrency, OAuth, streaming, MCP and dashboard behavior against PostgreSQL as well as SQLite.
