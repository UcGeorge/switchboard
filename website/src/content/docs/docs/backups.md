---
title: "Backups, retention, and restore"
description: "Preserve the SQLite state safely and understand what cleanup removes."
---

The database is the durable state of the instance: requests, responses, inferred conversations, channels, credential hashes, OAuth registrations, sessions, and settings. Logs are separate. Protect both backups and the original file as sensitive data.

## Consistent backup

```sh
switchboard db backup switchboard-backup.db
```

This uses SQLite `VACUUM INTO` to create a consistent snapshot, including when the server is running. The destination must not already exist. Simply copying the main `.db` file during activity can omit data in the WAL, so use the backup command.

In [remote mode](../remote/), the command downloads the snapshot into your local destination. It does not interpret that file path on the remote server. Keep enough temporary disk space on the server for the snapshot.

## Restore

1. Stop the server and all CLI operations using the database.
2. Preserve the current database and associated WAL/SHM files if you may need to recover them.
3. Put the snapshot at the configured database path, with permissions appropriate for the server user.
4. Remove stale WAL/SHM files associated with the old database only after stopping all access and preserving what you need.
5. Start the matching binary and verify `/healthz`, settings, keys, and recent history.

Restoring a backup also restores credential and session state from that point in time. A token revoked after the backup may become valid again. Rotate sensitive credentials and invalidate sessions after restoring when necessary.

## Retention

`retention_days` defaults to 30. Background cleanup runs periodically and removes old finished requests and events; orphaned conversation records can then disappear. Active requests are not pruned by history retention. Expired sessions, login links, authorization codes and expired eligible token records have separate cleanup.

```sh
switchboard settings set retention_days 14
switchboard db prune
```

Setting history retention to zero disables age-based request/event pruning. It does not disable expiry of credentials or sessions.

## Reclaim storage

Deleting history frees pages inside SQLite but may not shrink the file immediately. `switchboard db vacuum` compacts it and may temporarily need additional disk space. Run it during a quiet period; writes can be delayed while maintenance holds the database.

## Upgrade safety

Create a backup before replacing a binary. Schema migrations apply automatically on open. A newer database may not work with an older binary; restore the corresponding pre-upgrade snapshot to roll back safely.

## PostgreSQL

PostgreSQL backups use a custom-format `pg_dump`, not a SQLite snapshot. See [PostgreSQL backup and restore](../postgres/#backup-and-restore) for client-version requirements and restoration.
