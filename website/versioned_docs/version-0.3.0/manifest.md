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
| `checksum` | `github-release`, `archive` | no | Expected SHA-256 of the downloaded archive or release asset. |

## source_path Auto-Detection

When `source_path` is omitted, `gpm` inspects the fetched tree:

1. If exactly one `addons/<name>/` directory exists, that directory is used.
2. Otherwise the root of the fetched tree is installed.

If the source has multiple addon directories under `addons/`, set
`source_path` explicitly so the install target is unambiguous.

## Validation

`gpm` validates manifests before fetching:

- Git sources require `url` and `version`.
- GitHub release sources require `repo` and `version`.
- Archive sources require an HTTP(S) `url`.
- `source_path` cannot be absolute or escape the fetched root with `..`.
- `checksum` must be a 64-character lowercase SHA-256 digest and is not valid
  for Git sources.
