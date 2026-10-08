# Security

Switchboard is a single-operator local service. Treat its database, log,
login links, and backups as sensitive: requests and responses contain full
conversation contents. Credentials are hashed; conversations are plaintext.

Bind to loopback by default. For remote use, terminate TLS in a trusted proxy,
set `public_url`, and configure a strong dashboard password. Do not forward
untrusted `X-Forwarded-*` headers through your proxy. Do not expose a development
instance containing real conversation histories.

OAuth clients receive access to the request queue, which can contain data
from all API keys. Only authorize trusted agents. Revoking a token prevents
future calls; work already read by an agent cannot be recalled.

Before filing a public vulnerability report, use the repository's GitHub
**Security → Report a vulnerability** private reporting feature. Maintainers
should enable it before the first public release. Do not include secrets in
public issues. Only the latest release is supported; update before reporting.

Releases include SHA-256 manifests to catch corrupted downloads. A checksum
served beside an archive does not establish publisher identity; download
only from the project's official GitHub repository.

Remote administration is disabled until an administration token is configured.
Use `auth create-admin-token` locally on the server or provide the server's
`SWITCHBOARD_CONTROL_TOKEN` secret. Client-side `SWITCHBOARD_ADMIN_TOKEN` is
separate from MCP and caller credentials and gives full operator access.
Expose the control API only over trusted HTTPS. Requests with browser Origin
headers are rejected. Administration subprocesses use a fixed command
allowlist and never execute a shell. Do not permit multiple servers to use the
same database; the CLI subprocesses are short-lived management operations.
