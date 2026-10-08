---
title: "Contribute to the project"
description: "Build, test, change SQL safely, and maintain the docs site."
---

The runtime uses Go 1.26.3+. The documentation site uses Node 22+ and npm. Runtime installation does not require Node.

```sh
git clone https://github.com/ucgeorge/switchboard.git
cd switchboard
make build
make check
```

`make check` runs vet, the race test suite, and formatting checks. Tests exercise the actual remote CLI binary, API streaming, MCP transport schemas, first-run storage, and dashboard behavior. Use temporary databases for development.

## SQL changes

Install sqlc 1.31.1, edit queries or add a new numbered migration, run `make generate`, then `sqlc diff`. Commit generated Go code with its SQL. Do not modify a migration already shipped to users to add new schema behavior.

## Docs development

```sh
cd website
npm ci
npm run dev
```

The site is open-source Astro/Starlight, with a custom landing page and searchable operator guides. `npm run build` produces static output; `npm run check` verifies types and internal links after building. GitHub Actions publishes it to Pages on main.

Use relative links between guides so a project-subpath deployment works. Keep commands consistent with `switchboard --help`. Explain where a command runs and which credential it requires, especially in remote examples.

## Contributions and reports

Open focused issues with version, OS, command, expected behavior and redacted output. Bug fixes should include a regression test along the failing user path. Do not attach real databases or credentials. Security issues belong in private vulnerability reporting, not public discussions.

Source and contributions are MIT-licensed. Third-party dependencies retain their own notices; see the repository's THIRD_PARTY_NOTICES.md.
