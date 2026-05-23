---
title: Quickstart
description: Create an addons.toml manifest and install the first addon.
---

# Quickstart

This quickstart starts from an existing Godot project with a `project.godot`
file.

## 1. Install gpm

On macOS:

```bash
brew install --cask cafecito-games/tap/gpm
```

Anywhere Go 1.26.2+ is available:

```bash
go install github.com/cafecito-games/godot-package-manager/cmd/gpm@latest
```

Verify the binary:

```bash
gpm --version
```

## 2. Create the manifest

From your Godot project root:

```bash
gpm init
```

This creates `addons.toml` next to `project.godot`.

## 3. Add an addon

Search AssetLib interactively:

```bash
gpm assetlib
```

Press `f` in the interactive browser to browse AssetLib categories or search
within the selected category. Interactive searches fetch up to 20 results at a
time.

When browsing outside a Godot project, `gpm` uses the latest stable Godot
version and disables install confirmation until you run it inside a project with
`addons.toml`.

Or add a known AssetLib asset ID:

```bash
gpm assetlib add 2598
```

Or add a Git-backed addon directly:

```bash
gpm add --name dialogue_manager \
  --source git \
  --url https://github.com/nathanhoad/godot_dialogue_manager.git \
  --version v2.1.0 \
  --source-path addons/dialogue_manager
```

`gpm assetlib add` resolves the selected AssetLib entry to an archive URL,
writes the manifest entry, and installs the addon immediately. If the install
fails, the new manifest entry is rolled back.

## 4. Reproduce installs

On another machine or in CI:

```bash
gpm install
```

`gpm install` honors consistent entries in `addons.lock`, so existing pins are
installed instead of being re-resolved.

## 5. Commit the dependency files

Commit the manifest and lockfile:

```bash
git add addons.toml addons.lock
git commit -m "Add Godot addon dependencies"
```

Whether you commit the installed `addons/` directory is a project policy choice.
The important part for `gpm` is that `addons.toml` and `addons.lock` are
reviewed and versioned.
