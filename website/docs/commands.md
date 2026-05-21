---
title: Commands
description: gpm command reference.
---

# Commands

## gpm init

Create a starter `addons.toml`.

```bash
gpm init
gpm init --dir path/to/game
```

`gpm init` fails if `addons.toml` already exists.

## gpm add

Add an addon to `addons.toml` and install it immediately.

Interactive wizard:

```bash
gpm add
```

Non-interactive Git source:

```bash
gpm add --name dialogue_manager \
  --source git \
  --url https://github.com/nathanhoad/godot_dialogue_manager.git \
  --version v2.1.0 \
  --source-path addons/dialogue_manager
```

Non-interactive GitHub release source:

```bash
gpm add --name some_plugin \
  --source github-release \
  --repo owner/some_plugin \
  --version 1.4.0 \
  --asset some_plugin.zip
```

Non-interactive archive source:

```bash
gpm add --name raw_thing \
  --source archive \
  --url https://example.com/thing-1.0.zip
```

Available flags:

| Flag | Notes |
| --- | --- |
| `--name` | Addon table key under `[addons]`. |
| `--source` | `git`, `github-release`, or `archive`. |
| `--url` | Git clone URL or archive URL. |
| `--repo` | GitHub release repository as `owner/repo`. |
| `--version` | Git ref or release tag. |
| `--asset` | GitHub release asset name or glob. |
| `--source-path` | Subdirectory inside the fetched source. |
| `--install-as` | Directory name under `addons/`. |
| `--dir` | Start directory for project discovery. |

`gpm add` saves the manifest before installing. If installation fails, it rolls
back the manifest entry.

## gpm assetlib

Search Godot AssetLib, add selected addons to `addons.toml`, and install them.

Interactive search:

```bash
gpm assetlib
```

If no `project.godot` is found, the interactive browser uses the latest stable
Godot version for search filtering. Adding and installing still requires running
inside a Godot project with `addons.toml`; without one, asset detail opens in
browse-only mode.

In the interactive browser, press `f` to open the category filter drawer.
Selecting a category browses that category; typed search text searches within
the selected category. Interactive searches fetch up to 20 results at a time;
refine the query or category when you need a narrower set.

Non-interactive search:

```bash
gpm assetlib search dialogue --godot-version 4.2
```

`gpm assetlib search` can run outside a Godot project and does not require
`addons.toml`. If no project is found and `--godot-version` is omitted, it uses
the latest stable Godot version for filtering.

Add by AssetLib ID:

```bash
gpm assetlib add 2598
```

`gpm assetlib add` fetches AssetLib detail for the ID, stores the selected addon
as an `archive` entry in `addons.toml`, and installs it immediately. If
installation fails, the manifest entry is rolled back.

Search flags:

| Flag | Notes |
| --- | --- |
| `--godot-version` | Godot version filter, such as `4.2`. If omitted, `gpm` tries `project.godot` `config/features`; outside a project it uses the latest stable Godot version. |
| `--category` | AssetLib category ID or name. |
| `--support` | `official`, `featured`, `community`, or `testing`. |
| `--sort` | `rating`, `cost`, `name`, or `updated`. |
| `--max-results` | Result count, from 1 to 500. |
| `--dir` | Start directory for project discovery. |

Add flags:

| Flag | Notes |
| --- | --- |
| `--name` | Override the manifest key derived from the AssetLib title. |
| `--source-path` | Subdirectory inside the downloaded asset to install. |
| `--install-as` | Directory name under `addons/`. |
| `--dir` | Start directory for project discovery. |

## gpm install

Install every addon declared in `addons.toml`:

```bash
gpm install
```

When a lock entry is consistent with the manifest, `gpm install` honors the
existing pin. If an addon is new or its manifest entry changed, that addon is
re-resolved and the lockfile is updated.

## gpm update

Re-resolve addons and rewrite lock pins:

```bash
gpm update
gpm update dialogue_manager
gpm update dialogue_manager some_plugin
```

With no names, every addon is updated. With names, only those addons are
updated.

## gpm remove

Remove an addon from the manifest, lockfile, and `addons/` directory:

```bash
gpm remove dialogue_manager
```

The on-disk addon directory is removed before manifest changes are saved. If
filesystem removal fails, the manifest and lockfile stay internally consistent.

## gpm list

List configured addons and whether their install directory exists:

```bash
gpm list
```

## Shell Completion

`gpm` uses Cobra shell completion:

```bash
gpm completion bash
gpm completion zsh
gpm completion fish
gpm completion powershell
```

## Global Flags

| Flag | Short | Notes |
| --- | --- | --- |
| `--json` | | Emit machine-readable JSON for supported commands. |
| `--verbose` | `-v` | Print project, manifest, and lockfile paths. |
| `--quiet` | `-q` | Suppress non-error output. |

`--json` is supported by `add`, `assetlib search`, `assetlib add`, `install`,
`update`, and `list`.
