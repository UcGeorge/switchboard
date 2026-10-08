# Troubleshooting

## First run

`switchboard --open` creates `~/.switchboard` automatically, starts the TUI,
and opens the dashboard with a one-time login link. `--headless` prints URLs
and logs to stderr. If your environment has no browser, omit `--open` and
open the displayed URL manually. The dashboard needs no network assets.

## Command not found after install

Unix installs default to `~/.local/bin`. Add this to your shell profile:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Windows installs into `%LOCALAPPDATA%\Switchboard\bin` and updates the user
PATH. Open a new terminal after installation.

## Port already in use

Choose another address: `switchboard --addr 127.0.0.1:8081`. Use the same
`--addr` for `auth login-link` and MCP configuration commands. One server
should own each database; management CLI commands can run alongside it.

## Password forgotten

`switchboard auth login-link` produces a single-use URL valid for 15 minutes.
Or run `switchboard auth set-password` from the machine that owns the database.

## Requests wait forever / return 504

An agent must actively run the serving loop; merely adding the MCP server to
its configuration does not make it serve. Ask it to serve Switchboard requests.
Check `switchboard channels list`, `requests pending`, and `events tail`.
Check model patterns, key pinning, drain status and concurrency limits.

## Agent lease expired

Call `extend_lease` periodically during long reasoning, or stream output.
Channel heartbeats do not renew request leases. Configure the stale threshold
to exceed expected inactivity, or heartbeat while reasoning.

## Public OAuth URLs point at localhost

Set `switchboard settings set public_url https://your-domain.example` and
restart the client connection. Your TLS proxy must preserve the Host header
and set X-Forwarded-Proto correctly. Disable proxy buffering for SSE.

## Data and uninstall

Unix: delete the installed executable to uninstall. Windows: remove the
installation directory and its user PATH entry. Data is deliberately retained.
Use `switchboard db path` to locate it. Back up before removing any database.
Logs and request histories may contain sensitive information; redact them
before sharing bug reports.
