# Self-hosting

Use existing hardware for no additional hosting subscription, or a persistent
Linux VM. Oracle Always Free is a zero-cost cloud candidate within quotas, but
capacity and idle-instance reclamation mean it is not guaranteed always-on.
Current official free-tenancy A1 limits are 2 OCPUs/12 GB RAM, as checked on
8 October 2026: https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm

PostgreSQL support is in current main/source builds and is newer than v0.1.0.
Use the Compose build or build main locally until a matching binary release exists.

## Compose

Copy `.env.example` to `.env` and set distinct administrator/control credentials.
SQLite: `docker compose up -d --build --wait`.
Bundled PostgreSQL: set a strong URL-safe `POSTGRES_PASSWORD`, then run
`docker compose -f compose.yaml -f compose.postgres.yaml up -d --build --wait`.
Existing PostgreSQL: set `SWITCHBOARD_DATABASE_URL` and use only compose.yaml.

Add compose.tls.yaml with a configured domain/public URL for Caddy HTTPS.
Default app host publication is loopback. Retain the same project name for
persistent volumes; never use `down -v` unless intentionally deleting data.

Environment `SWITCHBOARD_<UPPERCASE_SETTING>` overrides a stored runtime setting.
`SWITCHBOARD_ADMIN_PASSWORD` applies the configured dashboard password on startup.

## Keel

Use [Keel](https://keel-cloud.mintlify.site/) with `keel validate`, `keel dev`,
or `keel deploy compose-ssh --var-file /path/to/private-values.env`.
The existing remote host needs Docker Compose, an authorized deployment account,
and a verified SSH host key. Strict SSH host checking remains enabled. Keel's
runner does not need a Docker daemon inside; builds happen on the remote host.

The deployment does not provision a VM or create paid resources. Keep the
Compose project ID unchanged across target renames. See the website's Compose,
PostgreSQL, hosting, Keel, backup and security guides for complete operation.
