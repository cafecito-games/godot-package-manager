---
title: Troubleshooting
description: Common gpm setup and install failures.
---

# Troubleshooting

## No Godot Project Was Found

Most commands discover the project by walking upward from the current directory
until they find `project.godot`.

Run from inside your Godot project:

```bash
cd path/to/game
gpm install
```

Or pass the start directory explicitly:

```bash
gpm install --dir path/to/game
```

`gpm init` creates `addons.toml` in the current directory unless `--dir` is
provided.

## addons.toml Is Missing

Create a starter manifest:

```bash
gpm init
```

Then add addons with `gpm add` or edit `addons.toml` by hand.

## Multiple Addon Directories Were Detected

When `source_path` is omitted, `gpm` can auto-detect a single
`addons/<name>/` directory. If a source contains multiple addon directories,
set `source_path`:

```toml
[addons.dialogue_manager]
source = "git"
url = "https://github.com/nathanhoad/godot_dialogue_manager.git"
version = "v2.1.0"
source_path = "addons/dialogue_manager"
```

## GitHub Release Returns HTTP 404

GitHub returns 404 for private repositories that the request cannot see. For
private release assets, set a token:

```bash
export GITHUB_TOKEN=$(gh auth token)
gpm install
```

Check that the token can read the repository contents.

## No Release Asset Matched

For `github-release` sources, `asset` is matched against release asset names.
If the release has more than one asset, set an exact name or a narrower glob:

```toml
asset = "my-addon-v1.4.0.zip"
```

## Checksum Mismatch

For archive and GitHub release sources, a checksum mismatch means the bytes
downloaded by `gpm` do not match the expected SHA-256. Check whether the
upstream asset was republished, the URL changed, or the manifest checksum was
copied incorrectly.

Use `gpm update <name>` only after you have confirmed the new asset is the one
you want.

## Unknown Addon

Commands that target a specific addon require the addon name from the
`addons.toml` table key:

```toml
[addons.dialogue_manager]
```

The command name is therefore:

```bash
gpm update dialogue_manager
gpm remove dialogue_manager
```

## Git Is Not Found

Git sources require `git` on `PATH`. Verify:

```bash
git --version
```

Install Git with your OS package manager, then retry `gpm install`.
