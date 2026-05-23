---
title: Releases
description: How gpm releases and versioned docs are published.
---

# Releases

Releases are handled by GitHub Actions and GoReleaser.

## GitHub Pages

Documentation publishes from `website/` to GitHub Pages:

```text
https://cafecito-games.github.io/godot-package-manager/
```

Merges to `main` that change `website/**`, `README.md`, or docs workflow files
trigger a Pages deploy. These live docs represent the current `main` branch.

## Manual Releases

The release workflow can be run manually from `main` with a semver bump:

- `patch`
- `minor`
- `major`

For manual releases, the workflow:

1. Computes the next `vMAJOR.MINOR.PATCH` tag.
2. Installs the docs dependencies.
3. Snapshots Docusaurus docs for the new version.
4. Commits the versioned docs snapshot to `main`.
5. Tags that commit.
6. Runs GoReleaser against the tag.

The generated docs snapshot files live under:

```text
website/versioned_docs/
website/versioned_sidebars/
website/versions.json
```

## Tag Pushes

Pushing a `v*` tag directly still runs GoReleaser. Direct tag pushes do not
create a new Docusaurus version snapshot on `main`; use the manual release path
when you want the published docs version dropdown to include the release.

## Release Assets

GoReleaser builds `gpm` for macOS and Linux on amd64 and arm64. It also updates
the Cafecito Games Homebrew tap when `HOMEBREW_TAP_TOKEN` is configured.
