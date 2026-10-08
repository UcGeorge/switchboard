---
title: "Set up GitHub Pages"
description: "Enable and deploy this repository’s landing page and operator docs with GitHub Actions."
---

The repository contains `.github/workflows/pages.yml`. It builds `website/`, checks local links, uploads the static artifact and deploys it to Pages. You do not need a `gh-pages` branch or to copy generated files into `docs/`.

## Exact setup for UcGeorge/switchboard

1. Open **GitHub → UcGeorge/switchboard → Settings → Pages**.
2. Under **Build and deployment → Source**, select **GitHub Actions**.
3. Open **Actions → Documentation → Run workflow**, select `main`, and run it. This initial manual run is useful when the last pushed change did not match the workflow's website-path filter.
4. Wait for both the `build` and `deploy` jobs to succeed. The deployment job links to the site.
5. Open **https://ucgeorge.github.io/switchboard/** for the landing page, then use **Documentation** for the guides.

A repository check on 8 October 2026 showed that this repository already has `build_type: workflow`, so step 2 is already configured. Running the initial Documentation workflow is the next action if no successful Pages deployment is present.

Equivalent CLI invocation, using a GitHub account with workflow permission:

```sh
gh workflow run pages.yml --repo UcGeorge/switchboard --ref main
gh run list --repo UcGeorge/switchboard --workflow pages.yml
```

## Subsequent updates

Push changes under `website/` or to the Pages workflow onto `main`. The workflow rebuilds and deploys automatically. Pull-request CI builds and validates the website without publishing it. The `github-pages` environment may require approval if you configured deployment protection rules; approve that deployment in Actions when prompted.

## URL and custom domains

The site derives the project base path `/switchboard` from `GITHUB_REPOSITORY`. Links, scripts, search and images are built for that path. Do not serve this project build at `/` without changing its site/base configuration.

For a custom domain, configure its DNS and **Settings → Pages → Custom domain**, then set matching `SITE_URL` and `SITE_BASE` values in the build workflow. Verify HTTPS and the generated search assets. See [GitHub's publishing-source documentation](https://docs.github.com/en/pages/getting-started-with-github-pages/configuring-a-publishing-source-for-your-github-pages-site).

## If it does not deploy

- No run: manually dispatch Documentation or push a website change.
- Pages 404 in configure-pages: confirm Source is GitHub Actions and Pages is enabled.
- Build failure: inspect the npm build/check log; use Node 22.12+ and the committed lockfile.
- Deployment pending: check the github-pages environment rules and job permissions (`pages: write`, `id-token: write`).
- Landing page loads but images/search fail: check the configured project base path.

Pages is the public documentation site only. Your runtime dashboard is hosted by the [Compose deployment](../compose/), not by GitHub Pages.
