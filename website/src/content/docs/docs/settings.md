---
title: "Configuration reference"
description: "Process flags, environment variables, runtime settings, and safe tuning."
---

Switchboard has two configuration layers: **process configuration** chooses a database, bind address, and logging mode; **runtime settings** are stored in that database and reloaded while the server runs.

## Process options

| Flag | Environment | Default |
| --- | --- | --- |
| `--addr` | `SWITCHBOARD_ADDR` | `127.0.0.1:8080` |
| `--db` | `SWITCHBOARD_DB` | `~/.switchboard/switchboard.db` |
| — | `SWITCHBOARD_DATA_DIR` | `~/.switchboard` |
| `--log-level` | `SWITCHBOARD_LOG_LEVEL` | `info` |
| `--url` | `SWITCHBOARD_URL` | Empty: local management |
| `--admin-token` | `SWITCHBOARD_ADMIN_TOKEN` | Empty: no remote credential |
| — | `SWITCHBOARD_CONTROL_TOKEN` | Empty: server administration uses database token if configured |

`--headless` disables the TUI. `--open` asks the operating system to open the dashboard with a magic link. Go installation location and application data location are separate.

## Runtime settings

```sh
switchboard settings list
switchboard settings get lease_seconds
switchboard settings set lease_seconds 180
```

| Key | Default | Meaning |
| --- | --- | --- |
| `instance_name` | `Switchboard` | Dashboard display name |
| `public_url` | Empty | External HTTPS base URL for discovery and links |
| `models` | `switchboard/auto` | Comma-separated advertised model names |
| `accept_any_model` | `true` | Permit requests for undeclared names to queue |
| `request_timeout_seconds` | `600` | Total caller wait, overridable per key |
| `lease_seconds` | `120` | Claim progress interval |
| `channel_stale_seconds` | `90` | Channel silence threshold |
| `max_attempts` | `3` | Maximum claims per request |
| `max_queue_depth` | `0` | Queued-request cap; zero means unlimited |
| `default_rate_limit_rpm` | `0` | Fallback key rate; zero means unlimited |
| `stream_keepalive_seconds` | `15` | Waiting SSE comment interval |
| `retention_days` | `30` | Finished history age; zero disables history pruning |
| `oauth_access_ttl_seconds` | `86400` | OAuth access-token lifetime |
| `oauth_refresh_ttl_seconds` | `2592000` | Refresh eligibility window from initial issuance |
| `session_ttl_days` | `30` | Dashboard session lifetime |

Use positive time intervals for useful leases, deadlines, stale detection, and token lifetimes. Zero is meaningful only for settings documented as disabling a limit; a zero timeout can expire immediately. Runtime validation rejects negative numeric values, malformed booleans and unknown setting names.

## Tuning together

A longer request deadline does not extend the claim lease. Agents still need progress or lease renewal. A stale threshold shorter than the expected silent reasoning interval can mark a busy agent offline. Keep agents heartbeating or choose intervals consistent with their behavior. See [lifecycle](../lifecycle/).

Key-specific timeout, RPM, priority, model restrictions, and pinning live on the key record. The dashboard offers editing; creation flags are documented in the [CLI guide](../cli/).
