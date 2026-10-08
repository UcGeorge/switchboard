---
title: "Deploy with Keel over SSH"
description: "Use a validated deployment form to run Compose on an existing low-cost host."
---

[Keel](https://keel-cloud.mintlify.site/) packages deployment steps and typed inputs into a local UI or headless command. Switchboard's `compose-ssh` deployment targets an **existing Linux host**, using Docker Compose there. It does not buy, create, or configure a cloud VM.

This replaces the proposed Vercel runtime target. The API/MCP server needs a persistent process and shared in-memory connection state; deploying this binary unchanged as short-lived function instances would break that model. The public website remains independently hosted on GitHub Pages.

## Prepare the host

1. Choose an [appropriate low-cost host](../hosting/), install Docker Engine and its Compose plugin, and create a deployment account.
2. Authorize a dedicated SSH key for that account. Docker access is effectively root access; keep the key restricted to deployment use.
3. Create `/opt/switchboard` (or your chosen directory), owned by the deployment account.
4. Obtain the SSH host key through a trusted console and verify its fingerprint. The deployment requires a known_hosts entry and keeps strict host verification enabled.
5. For public HTTPS, configure DNS and firewalls. For private use, keep application traffic behind an SSH tunnel.

## Configure and deploy

Install Keel using its [CLI guide](https://keel-cloud.mintlify.site/reference/cli), then:

```sh
keel validate
keel dev
```

Create a target for `compose-ssh` and enter its host, SSH key, verified host key, stable Compose project name, database backend, and secrets. The database defaults to bundled PostgreSQL; SQLite or an external PostgreSQL URL is selectable. Keel masks secret inputs, but do not deliberately print them in deployment scripts.

Headless use:

```sh
keel manifest compose-ssh -o required-values.md
keel deploy compose-ssh --var-file /path/to/private-values.env
```

Keep that private values file out of Git. See [Keel's variable configuration](https://keel-cloud.mintlify.site/reference/keel-yaml) for typed inputs, saved targets, conditions and outputs.

## What the deployment does

It verifies SSH and remote Docker, uploads the application source into a private per-run release directory, creates a private `.env` file, builds on the remote host and starts the selected Compose services. It waits for service health and returns the public URL, or `localhost:8080` for access through a tunnel.

The Keel environment contains SSH, Python, tar and CA certificates. It does not run a Docker daemon: Docker builds happen on the remote host over SSH, consistent with the [Keel environment model](https://keel-cloud.mintlify.site/guides/environment-images).

Keep `COMPOSE_PROJECT` stable across runs and target renames; it determines persistent volume names. Choose a distinct project and directory for each independent instance. Never reuse one database across concurrently running Switchboard replicas. Back up before upgrades. Keel does not provide automatic rollback or cloud capacity guarantees.

## Secrets and recovery

Changing the PostgreSQL input does not rotate an existing database role password. Rotate the role explicitly, then update the target value. Rotating the administration token invalidates old remote CLI credentials after the replacement container starts. The configured dashboard password is applied on startup.

No live SSH deployment can be tested without your host and credentials. The repository validates the Keel schema, builds the environment image, tests script command construction, and exercises Compose locally. Before a first production run, verify SSH access and restore a backup on a disposable instance.
