# Required values — Switchboard

**Deployment:** `compose-ssh` &nbsp;·&nbsp; **Generated:** October 8, 2026

Deploy Switchboard and its database to an existing low-cost Linux host.

The deployment needs the **12 values** listed below. Each entry explains what the value is, why it is needed, and how to obtain it. Values marked **sensitive** should be shared through a secure channel — never by plain email or chat.

| # | Value | Type | Sensitive | Required |
|---|-------|------|-----------|----------|
| 1 | Hostname or IP address | Text | No | Yes |
| 2 | Deployment user | Text | No | Yes |
| 3 | SSH private key | Multi-line text | Yes | Yes |
| 4 | Verified SSH host key | Multi-line text | No | Yes |
| 5 | Remote deployment directory | Text | No | Yes |
| 6 | Stable Compose project identifier | Text | No | Yes |
| 7 | External PostgreSQL connection URL | Text | Yes | Yes |
| 8 | PostgreSQL password | Text | Yes | Yes |
| 9 | Remote administration token | Text | Yes | Yes |
| 10 | Dashboard password | Text | Yes | Yes |
| 11 | Optional HTTPS domain | Text | No | No |
| 12 | Public application URL | URL | No | No |

---

## 1. Hostname or IP address

Type: Text

**Why it is needed**

Identifies the existing host running Docker Compose.

**How to get it**

Copy the host's public IPv4 address or its DNS name from your provider console.

**Format**

Hostname or IPv4 address only..

## 2. Deployment user

Type: Text

**Why it is needed**

Selects the account that can deploy to the host.

**How to get it**

Create a dedicated deployment user and authorize it to use Docker; Docker access is root-equivalent.

**Format**

Linux account name..

## 3. SSH private key

Type: Multi-line text · **Sensitive — share securely**

**Why it is needed**

Authenticates the deployment account without an interactive password.

**How to get it**

Generate a dedicated SSH key locally and install its public key in the user's authorized_keys.

## 4. Verified SSH host key

Type: Multi-line text

**Why it is needed**

Prevents connecting to an impersonated host; strict verification stays enabled.

**How to get it**

Obtain the host key via a trusted console and verify its fingerprint before supplying a known_hosts line.

## 5. Remote deployment directory

Type: Text · Default: `/opt/switchboard`

**Why it is needed**

Stores uploaded source and private environment configuration.

**How to get it**

Create this directory owned by the deployment user before the first run. Use a distinct path per instance.

**Format**

Absolute path without spaces..

## 6. Stable Compose project identifier

Type: Text · Default: `switchboard`

**Why it is needed**

Selects persistent volumes and prevents mixing different instances.

**How to get it**

Choose one stable name per instance; do not change it when renaming a Keel target.

**Format**

Keep this identifier unchanged to preserve volume names..

## 7. External PostgreSQL connection URL

Type: Text · **Sensitive — share securely** · Only applies when DATABASE_BACKEND = external-postgres

**Why it is needed**

Connects the application to your existing PostgreSQL service instead of starting a database container.

**How to get it**

Copy the provider's connection URL; use sslmode=verify-full with a trusted CA for external connections.

**Format**

PostgreSQL URL with database credentials and TLS options..

## 8. PostgreSQL password

Type: Text · **Sensitive — share securely** · Only applies when DATABASE_BACKEND = postgres

**Why it is needed**

Secures the internal PostgreSQL service. Changing this does not rotate a password in an existing volume.

**How to get it**

Generate a strong URL-safe value and retain it across deployments, or explicitly rotate it inside PostgreSQL first.

**Format**

At least 24 URL-safe characters; openssl rand -hex 32 works..

## 9. Remote administration token

Type: Text · **Sensitive — share securely**

**Why it is needed**

Enables authenticated management from a laptop.

**How to get it**

Generate with openssl rand -hex 32. Keep this separate from agent and caller credentials.

**Format**

At least 32 characters.

## 10. Dashboard password

Type: Text · **Sensitive — share securely**

**Why it is needed**

Protects the web dashboard without relying on a generated first-run password in logs.

**How to get it**

Generate a strong unique password and store it in your password manager.

**Format**

At least 12 characters.

## 11. Optional HTTPS domain

Type: Text · Optional

**Why it is needed**

Enables Caddy TLS and public OAuth discovery; leave blank for an SSH-tunnel-only instance.

**How to get it**

Point the domain's DNS record at the host and open TCP 80/443 in its firewalls.

**Format**

DNS name without scheme or path..

## 12. Public application URL

Type: URL · Optional · Only applies when DOMAIN is set

**Why it is needed**

Supplies the externally reachable HTTPS URL to OAuth discovery and connection links.

**How to get it**

Use https:// followed by exactly the supplied domain.

---

*Generated by [Keel](https://keel.dev) from this project's deployment configuration.*
