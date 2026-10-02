---
title: Manifest
description: Configure addons.toml entries.
---

# Manifest

`addons.toml` is the editable manifest for your Godot addon dependencies.

## Git Source

```toml
[addons.dialogue_manager]
source = "git"
url = "https://github.com/nathanhoad/godot_dialogue_manager.git"
version = "v2.1.0"
source_path = "addons/dialogue_manager"
install_as = "dialogue_manager"
exclude = ["dotnet"]
```

`version` can be a tag, branch, or commit SHA. For reproducible installs,
prefer tags or commit SHAs and commit `addons.lock`.

## GitHub Release Source

```toml
[addons.some_plugin]
source = "github-release"
repo = "owner/some_plugin"
version = "1.4.0"
asset = "some_plugin.zip"
source_path = "addons/some_plugin"
checksum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

`asset` is optional only when the release has exactly one asset. When supplied,
it uses `path.Match` glob syntax, so patterns such as `*.zip` are supported.

## Archive Source

```toml
[addons.raw_thing]
source = "archive"
url = "https://example.com/thing-1.0.zip"
checksum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

Archives may be zip or tar formats supported by `gpm`. Archive downloads are
capped at 512 MiB.

## AssetLib-Added Entries

`gpm assetlib add` does not create a new manifest source type. It resolves the
AssetLib asset's `download_url` and writes a normal archive entry:

```toml
[addons.dialogue_engine]
source = "archive"
url = "https://example.com/dialogue-engine/archive/main.zip"
version = "1.6.0"
```

Use `source_path` or `install_as` with `gpm assetlib add` when the downloaded
archive layout needs the same disambiguation as any other archive source.

## Platform Declarations

The optional `[project]` table declares the Godot platforms the project targets.
It applies to every addon that does not declare its own list:

```toml
[project]
platforms = ["windows.x86_64", "linux.x86_64"]
```

An addon may declare its own list, which **replaces** the project list for that
addon rather than merging with it:

```toml
[addons.limboai]
source = "archive"
url = "https://example.com/limboai-1.4.0-core.zip"
version = "1.4.0"
index = "https://example.com/gpm-index.toml"
platforms = ["ios.arm64"]
```

Replacement is deliberate: an addon that declares its own platforms is
unaffected by later additions to `[project] platforms`. An inherited list is
never written into the addon table, so `gpm add`, `gpm remove`, and the AssetLib
wizard all preserve inheritance instead of freezing the resolved list into a
per-addon override.

The two ways of writing "no platforms" differ by table. In `[project]`, an absent
`platforms` key and `platforms = []` both mean the project declares nothing. On
an addon, an absent `platforms` key **inherits** the project list, while
`platforms = []` is an explicit override that declares nothing for that addon —
so it receives only the `core` slice and the host's slice.

The `core` slice and the slice for the machine running `gpm` are always installed
and may not be declared here.

`index` marks an `archive` source as sliced and is the absolute URL of the
addon's `gpm-index.toml`. It is not valid for any other source type: a
`github-release` source discovers its index from the release assets, and a `git`
source is never sliced.

Platform tags, the full list of known platforms and architectures, and what
`gpm` installs for each are covered in [Platform slices](slices.md).

Unknown keys inside `[project]` are rejected. A stray key elsewhere in
`addons.toml` is silently ignored.

## Field Reference

| Field | Applies to | Required | Notes |
| --- | --- | --- | --- |
| `source` | all | yes | `git`, `github-release`, or `archive`. |
| `url` | `git`, `archive` | yes | Git clone URL or direct archive URL. |
| `repo` | `github-release` | yes | GitHub repository as `owner/repo`. |
| `version` | `git`, `github-release` | yes | Git ref or GitHub release tag. |
| `asset` | `github-release` | no | Asset name or glob; required when more than one asset matches. |
| `source_path` | all | no | Subdirectory inside the fetched tree to install. |
| `install_as` | all | no | Directory name under `addons/`; defaults to the table key. |
| `exclude` | all | no | Directories under the selected install root to skip. |
| `checksum` | `github-release`, `archive` | no | Expected SHA-256 of the downloaded archive or release asset. Not valid for a sliced addon. |
| `platforms` | all | no | Declared platform tags for this addon. Replaces `[project] platforms` when present; `[]` declares none. See [Platform slices](slices.md). |
| `index` | `archive` | no | URL of the addon's `gpm-index.toml`, marking the archive as sliced. |

The `[project]` table carries one key:

| Field | Required | Notes |
| --- | --- | --- |
| `platforms` | no | Declared platform tags for every addon that does not declare its own. |

## source_path Auto-Detection

When `source_path` is omitted, `gpm` inspects the fetched tree:

1. If exactly one `addons/<name>/` directory exists, that directory is used.
2. Otherwise the root of the fetched tree is installed.

If the source has multiple addon directories under `addons/`, set
`source_path` explicitly so the install target is unambiguous.

## Install Exclusions

Use `exclude` to skip directories inside the selected install root:

```toml
[addons.some_addon]
source = "archive"
url = "https://example.com/some-addon.zip"
exclude = ["dotnet", "bindings/dotnet"]
```

Exclusions are relative to the directory chosen by `source_path` or
auto-detection. Missing exclusion directories are ignored.

## Validation

`gpm` validates manifests before fetching:

- Git sources require `url` and `version`.
- GitHub release sources require `repo` and `version`.
- Archive sources require an HTTP(S) `url`.
- `source_path` cannot be absolute or escape the fetched root with `..`.
- `exclude` entries cannot be empty, absolute, the install root itself, or
  escape the install root with `..`.
- `checksum` must be a 64-character lowercase SHA-256 digest and is not valid
  for Git sources.
- Every `platforms` entry must be a known platform tag, and `core` may not be
  declared. The `[project]` list is checked on its own, so a typo is reported
  even in a manifest that declares no addons yet.
- `index` is valid only for `archive` sources and must be an HTTP(S) URL.
