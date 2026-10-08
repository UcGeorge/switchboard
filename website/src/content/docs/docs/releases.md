---
title: "Releases and Pages publishing"
description: "Publish tested binaries and the static documentation from the same repository."
---

This page is for maintainers. Operators can use the [installer](../installation/) and [upgrade guidance](../backups/) without running a release workflow.

## Repository configuration

Default source and installer references use `ucgeorge/switchboard`. If the public repository has a different name, update those references and the module import path. The Pages workflow derives the hosting base from `GITHUB_REPOSITORY`; a project repository uses `/<repository>/` automatically.

Enable **Settings → Pages → Source: GitHub Actions**. Enable Actions and private vulnerability reporting before public launch. The checkout alone cannot enable repository settings or create a published URL; those require a GitHub repository and permissions.

## Packaging rehearsal

```sh
make check
sqlc diff
python3 scripts/release.py --version 0.1.0
python3 scripts/test_installer.py
```

Six archives cover macOS/Linux/Windows and amd64/arm64. Each contains the executable, README, MIT license and third-party notices. `checksums.txt` accompanies them. The Unix installer test uses a local fixture and checks corrupted-download rejection.

Cross-compilation verifies artifacts build; it does not validate every platform's interactive experience. CI also runs Go tests natively across macOS, Linux and Windows. Exercise installers on their target systems before recommending them broadly.

## Publish source and a release

After review and committing a release-ready changelog:

```sh
git tag -a v0.1.0 -m 'Switchboard 0.1.0'
git push origin main
git push origin v0.1.0
```

The tag workflow runs tests, packages binaries, and creates the GitHub release using a repository-scoped token. The installer's latest URL becomes usable only after the first release assets exist. Verify it against the published release.

## Publish the website

The documentation workflow builds `website`, validates links, uploads the static artifact and deploys to GitHub Pages on changes to main. The root path is the landing page; `/docs/` paths are the operator guides. For a custom domain, configure Pages and use `SITE_URL`/`SITE_BASE` consistently with the deployment.

No paid documentation provider or runtime server is needed for the public site. Switchboard itself still needs a machine or container for its live API and SQLite state; GitHub Pages does not host that service.
