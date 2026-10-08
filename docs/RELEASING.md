# Releasing Switchboard

## Before making the repository public

1. Confirm the repository name. The module and default installer URL use
   `ucgeorge/switchboard`; if publishing elsewhere, update those references.
2. Enable GitHub Actions and private vulnerability reporting.
3. Run `make check`, `sqlc diff`, and the local release/installer checks below.
4. Review LICENSE, third-party notices, changelog, and supported platforms.
5. Commit source, generated sqlc files, workflows, scripts, and documentation.
   Exclude `bin`, `dist`, credentials, logs, databases and editor settings.

## Local packaging rehearsal

```sh
python3 scripts/release.py --version 0.1.0
python3 scripts/test_installer.py
```

This builds macOS/Linux/Windows archives for amd64 and arm64 and writes
`dist/checksums.txt`. Every archive includes the executable, README, MIT
license and third-party notices. No signing credentials or publishing token
is needed for the rehearsal. Windows installer execution is a separate
Windows check; cross-compilation alone does not validate Windows UX.

## Publish

After review, push source to GitHub and tag a release:

```sh
git tag -a v0.1.0 -m 'Switchboard 0.1.0'
git push origin main
git push origin v0.1.0
```

The release workflow tests first, builds all six archives, then publishes a
GitHub release with checksums and generated release notes. Its publishing
job uses the repository-scoped GITHUB_TOKEN. No personal token is required.

The public single-command installer works **after** the source and the first
release are published. Until then, install from the local checkout:
`go install ./cmd/switchboard`. Do not advertise a release URL as live before
verifying the installer against the actual published assets.

## Upgrade and rollback

Run the installer again to upgrade, or set `SWITCHBOARD_VERSION=0.1.0` to pin.
Before upgrading, use `switchboard db backup backup.db`. Database migrations
run automatically. Older binaries may not understand newer schemas: rollback
requires restoring the matching backup, with the service stopped.

## Documentation website

Enable GitHub Pages with GitHub Actions as its source. The Pages workflow
builds the landing page and Starlight docs, validates local links, and deploys
a static artifact. `GITHUB_REPOSITORY` determines the project base path.
For local review, use `npm ci`, `npm run build`, and `npm run check` in `website`.
The public site needs no paid hosting. The live application needs a server;
GitHub Pages cannot run its API or database.
