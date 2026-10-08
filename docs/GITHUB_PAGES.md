# GitHub Pages setup

For **UcGeorge/switchboard**, open **Settings → Pages → Build and deployment**
and choose **GitHub Actions** as Source. The repository already has this setting
as of 8 October 2026.

Then open **Actions → Documentation → Run workflow → main → Run workflow**.
Wait for `build` and `deploy` to succeed. The site is:

https://ucgeorge.github.io/switchboard/

The root is the landing page; its Documentation link opens the operator guides.
No `gh-pages` branch or generated files committed into `docs/` are needed.

From the CLI: `gh workflow run pages.yml --repo UcGeorge/switchboard --ref main`.
Future website changes pushed to main deploy automatically. If a run waits for
approval, inspect the `github-pages` environment's deployment protection rules.

GitHub Pages hosts the static public website, not the API/MCP server. The latter
runs on a persistent host with Docker Compose. For the full troubleshooting and
custom-domain guide, see `website/src/content/docs/docs/github-pages.md`.
