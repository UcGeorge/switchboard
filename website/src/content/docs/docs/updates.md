---
title: "Update Switchboard"
description: "Check for new releases and safely replace the local executable."
---

Switchboard can check the project's GitHub releases and install the matching
binary for your operating system and architecture. Updating the executable does
not modify your database, credentials or configuration.

## Check and install

```sh
switchboard update --check
switchboard update
```

The first command reports the installed and available versions without changing
anything. The second downloads the platform archive and checksum manifest,
verifies SHA-256, extracts only the expected executable, and replaces the local
binary. A checksum mismatch leaves the executable unchanged. Back up before
upgrading and restart a running server afterward; its process continues using
the old executable until restarted.

## Automatic notices

The CLI makes an opportunistic background release check at most once per day
and prints a notice to stderr when a newer release is known. It does not block
normal commands, and it never installs an update automatically. Very short
commands may exit before a network check finishes; use `update --check` for an
immediate result. TUI notices appear after leaving the screen, rather than
being written over it. JSON commands and remote administration subprocesses
suppress automatic notices.

Set `SWITCHBOARD_NO_UPDATE_CHECK=1` to disable background checks. Explicit
`update --check` still works. Cached release information lives under your OS
user-cache directory; `SWITCHBOARD_UPDATE_CACHE_DIR` overrides that location.
The checker sends the selected repository name and an updater User-Agent to
GitHub; it does not transmit request history or API/agent credentials.

## Pin, reinstall, or replace a source build

```sh
switchboard update --version v0.2.0
switchboard update --force
switchboard update --check --json
```

An explicit version can also downgrade, so confirm that the target binary is
compatible with your database schema. Use a matching backup when rolling back
schema changes. `--force` explicitly replaces an unversioned source build or
reinstalls a release. Normal update comparisons use release versions, so a
source build based on a release is not silently replaced with that same older
release. Unversioned development binaries require `--force`.

## Local versus remote and containers

`update` always updates the CLI executable on the machine running the command,
even when `SWITCHBOARD_URL` is set. It does not replace a remote server binary.
For Compose/Keel deployments, update source or the image and recreate the
service using the documented deployment procedure. Do not modify a running
container's executable as your upgrade strategy.

A user-writable installation directory is required. Switchboard does not run
sudo or request administrator elevation. For package-manager-managed binaries,
prefer that package manager's upgrade mechanism. On Windows the updater keeps
a rollback copy during replacement; a running original can leave an `.old-*`
file until it exits. That file can be removed after the updating process ends.

## Release trust

By default the updater reads `UcGeorge/switchboard`. `SWITCHBOARD_REPO` selects
another owner/repository for a compatible fork. Only use a repository you trust:
a checksum served with an archive detects corruption, not an untrusted
publisher. HTTPS is used for the public GitHub API and release downloads.
