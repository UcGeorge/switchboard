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
