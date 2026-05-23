---
title: Overview
description: What gpm manages and how the files fit together.
---

# gpm

`gpm` is a command-line addon manager for Godot projects. It installs addons
declared in an `addons.toml` manifest into the project's `addons/` directory
and writes an `addons.lock` file with exact resolved pins.

The goal is to make addon dependencies explicit, reviewable, and reproducible:

- `addons.toml` says which addons your project uses and where to fetch them.
- `addons.lock` pins the resolved commit or archive checksum that was actually
  installed.
- `addons/` contains the installed Godot addon directories.

Commit `addons.toml` and `addons.lock` with your project. Teammates and CI can
then run `gpm install` to recreate the same addon set.

## Supported Sources

`gpm` can fetch addons from three source types:

| Source | Use for |
| --- | --- |
| `git` | Addons distributed from a Git repository tag, branch, or commit SHA. |
| `github-release` | Addons packaged as release assets on GitHub. |
| `archive` | Addons published as direct HTTP(S) zip or tar archives. |

Each source can install either the fetched root or a subdirectory selected with
`source_path`. If `source_path` is omitted, `gpm` can auto-detect a single
`addons/<name>/` directory in the fetched tree.

## Command Flow

For most projects:

1. Run `gpm init` next to `project.godot`.
2. Add dependencies with `gpm assetlib`, `gpm add`, or by editing `addons.toml`.
3. Run `gpm install` to populate `addons/` and write `addons.lock`.
4. Commit `addons.toml` and `addons.lock`.
5. Run `gpm update` when you intentionally want new pins.

Use `--json` on script-friendly commands when CI or another tool needs
machine-readable output.
