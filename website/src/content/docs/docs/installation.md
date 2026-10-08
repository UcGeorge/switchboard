---
title: "Install Switchboard"
description: "Install a release binary or build from source, then verify your installation."
---

Switchboard runs on macOS, Linux, and Windows on **amd64/x86-64** and **arm64**. Release binaries contain the web assets and database driver. Running a binary needs neither Go nor Node.

## Release installer: macOS and Linux

After the project has a published GitHub release:

```sh
curl -fsSL https://raw.githubusercontent.com/ucgeorge/switchboard/main/scripts/install.sh | sh
```

The installer detects your platform, downloads the release archive and checksum manifest, verifies SHA-256, and installs into `~/.local/bin` without sudo. If the checksum does not match, it leaves an existing installation untouched.

If your shell cannot find the command, add the directory to your shell profile and start a new terminal:

```sh
export PATH="$HOME/.local/bin:$PATH"
switchboard version
```

Choose a directory or pin a release:

```sh
curl -fsSL https://raw.githubusercontent.com/ucgeorge/switchboard/main/scripts/install.sh   | SWITCHBOARD_INSTALL_DIR="$HOME/bin" SWITCHBOARD_VERSION=0.1.0 sh
```

These URLs require both published source and a first tagged release. Before publication, use the local source installation below.

## Windows

In PowerShell:

```powershell
irm https://raw.githubusercontent.com/ucgeorge/switchboard/main/scripts/install.ps1 | iex
```

The installer verifies the archive, writes `switchboard.exe` into `%LOCALAPPDATA%\Switchboard\bin`, and adds that directory to your user PATH. Open a new terminal, then run `switchboard version`.

To inspect the script before running it, download it to a file first. Downloaded checksum manifests detect corruption; they do not substitute for trusting the publisher's repository.

## Build from source

Use Go **1.26.3 or later**. The generated SQL code is checked in, so sqlc is only required when changing SQL.

```sh
git clone https://github.com/ucgeorge/switchboard.git
cd switchboard
go install ./cmd/switchboard
```

`go install` normally puts the executable in `$(go env GOPATH)/bin`. Alternatively, `make build` writes `bin/switchboard` within the checkout. After publication, Go users can install with `go install github.com/ucgeorge/switchboard/cmd/switchboard@latest`.

## Verify and start

```sh
switchboard version
switchboard --open
```

On first run Switchboard creates its private data directory and database. The terminal displays the dashboard address, a one-time login link, and the generated dashboard password. [Continue with the quickstart](../quickstart/).

## Upgrade and uninstall

Back up your database before upgrading. Re-run the installer for the latest release, or pin a version. Schema migrations run automatically when the new version opens the database. Downgrading can require restoring a matching backup; see [backups and retention](../backups/).

To uninstall, remove the installed executable or Windows installation directory and its PATH entry. Data remains deliberately untouched. `switchboard db path` tells you where it lives; remove it only after preserving anything you need.
