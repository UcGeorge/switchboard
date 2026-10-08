---
title: "Credentials and security"
description: "Protect operator access, agent authorization, request histories, and public deployments."
---

Switchboard has one operator identity. It does not implement multiple user accounts, roles, billing, or caller-to-agent tenancy isolation. Treat an authorized serving agent as trusted to read queued conversations.

## Credential types

| Credential | Prefix | Grants | How created |
| --- | --- | --- | --- |
| Caller API key | `sk-sb-` | Submit chat requests and inspect its own request status | API keys UI / `keys create` |
| Static agent token | `sba_` | Use MCP and serve the queue | Agents UI / `tokens create` |
| OAuth access token | `sbo_` | Authorized MCP access within lifetime | OAuth consent |
| Administration token | `sbc_` | Remote operator commands and backups | Server-local `auth create-admin-token` |
| Dashboard session | Cookie | Operator web UI | Password / one-time login link |

High-entropy credentials and session values are stored hashed. The dashboard password uses argon2id. Full API and agent tokens are displayed once; revoke and replace a lost value rather than expecting to retrieve it.

## Dashboard access

First startup generates a password. Store it securely, then change it with **Settings** or `auth set-password`. `auth login-link` prints a single-use URL, valid for 15 minutes by default. The link itself is a credential: do not paste it into an issue or public chat.

Session cookies are HttpOnly and SameSite=Lax; secure deployment marks them Secure. State-changing dashboard actions require CSRF protection. Password guesses are rate-limited by client address. A trusted proxy must control forwarded headers, otherwise address-based defenses are unreliable.

## Agent authorization

Only the channel's owning token can drive it. That does not make the channel a tenant boundary: queue status and claim access are shared among authorized agents. OAuth approval grants this broad serving capability. Revoking a token prevents later calls; it cannot erase data an agent already read.

## Data at rest

SQLite stores full request bodies, response contents, metadata, and timelines in plaintext. Backups contain that information too. Use restrictive filesystem permissions, disk encryption where needed, a dedicated service user, and appropriate retention. Do not commit database or log files. The runtime creates new private data directories and files; existing directory permissions are not automatically tightened.

## Remote exposure

Use HTTPS through a trusted proxy or tunnel. Keep administration tokens out of caller/agent configurations and browser code. Remote administration rejects browser-origin requests and requires a distinct operator token. The documented command allowlist is not an arbitrary shell endpoint.

## Report a vulnerability

Use the repository's private GitHub vulnerability reporting when enabled. Do not post sensitive histories or credentials in public issues. See the root SECURITY.md for supported release policy. Checksum verification detects archive corruption; it is not publisher signature verification.
