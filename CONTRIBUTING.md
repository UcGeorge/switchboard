# Contributing

Use Go 1.26.3 or later. No Node build, external database, or CGO is needed.

```sh
git clone https://github.com/ucgeorge/switchboard.git
cd switchboard
make build
make check
./bin/switchboard serve --headless --db /tmp/switchboard-dev/instance.db
```

Generated SQL query code belongs in source control. After changing migrations
or queries, install sqlc 1.31.1 and run `make generate`, then `sqlc diff`.
Never edit `internal/db/sqlcgen` by hand. Add a new numbered migration for
schema changes so existing installations upgrade safely.

For behavior changes, include a regression test covering the user's path.
Run tests with a fresh temporary database; do not use your personal data.
CI checks Go formatting, vet, race tests on macOS/Linux/Windows, generated
SQL, and the Unix installer against a local release fixture.

Open an issue describing the problem and reproduction steps before large
changes. Keep pull requests focused and include validation results. Do not
include API keys, agent credentials, request histories, or database files.

See [release procedures](docs/RELEASING.md) for packaging and publishing.
Contributions are licensed under the project's MIT license.
