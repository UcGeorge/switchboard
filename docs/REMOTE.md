# Operate a remote instance

Instance management works over authenticated HTTP, without sharing the SQLite
file. Use the server's base URL, without `/v1` or `/mcp`:

```sh
# Run once on the server. The token is displayed once, stored hashed, and
# rotating it invalidates the previous token after the settings reload.
switchboard auth create-admin-token

# On your laptop (use HTTPS beyond loopback):
export SWITCHBOARD_URL=https://switchboard.example.com
export SWITCHBOARD_ADMIN_TOKEN=sbc_...
switchboard keys list
switchboard stats
switchboard requests pending
switchboard mcp add claude-code
```

`--url` and `--admin-token` are equivalent flags. Prefer the environment for
credentials to avoid shell history. `SWITCHBOARD_CONTROL_TOKEN` optionally
sets the server's administration token from your deployment secret store.
That override takes precedence over a locally minted token.

API keys (`sk-sb-`) authenticate callers; agent tokens (`sba_` / `sbo_`)
authenticate MCP agents; administration tokens (`sbc_`) manage the instance.
Do not interchange them. Administration grants broad access to histories,
keys, passwords, settings and backups. Do not give that token to serving agents.

## Remote behavior

- Keys, tokens, channels, requests, conversations, settings, stats, events,
  password changes, login links, and database maintenance address the server.
- `mcp url/config/add` use the remote MCP URL. New agent tokens are minted on
  the server; `claude mcp add` runs on the local machine.
- `mcp config --token sba_...` and `mcp url` need no administration token.
- `skill install` installs locally, where the agent runs.
- `requests answer --file reply.txt` reads your local file and sends its text.
- `events tail` polls remotely; Ctrl+C stops following.
- `db backup backup.db` downloads a consistent SQLite snapshot into a new
  local file. The file is not written on the server at a client-supplied path.
- `db path` returns the server's database path; `db prune/vacuum` run there.
- `auth create-admin-token` and `serve` run on the server machine; remote
  mode rejects them. `--db` cannot be combined with `--url`.
- `version`, skill installation, help and shell completions are local.

Remote password changes require `auth set-password --password ...`; there is
no prompt on the remote server. Use a trusted shell and avoid retaining the
command in history. A future credential input interface can improve this.

The control API runs only an allowlisted set of commands in isolated instances
of the same executable, with no shell interpolation. This works for installed
binaries and container deployments. API clients are not redirected to other
hosts, so administration credentials are not forwarded by redirects.
