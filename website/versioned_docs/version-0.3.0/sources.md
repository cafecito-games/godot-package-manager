---
title: Sources
description: How gpm fetches Git repositories, GitHub releases, and archives.
---

# Sources

Every addon entry has one source type. The source determines how `gpm` fetches
files before installing the selected directory into `addons/`.

## Git

Git sources are cloned with the system `git` binary:

```toml
[addons.dialogue_manager]
source = "git"
url = "git@github.com:nathanhoad/godot_dialogue_manager.git"
version = "v2.1.0"
source_path = "addons/dialogue_manager"
```

Private repositories use your normal SSH keys, credential helpers, or local Git
configuration. `gpm` does not store Git credentials.

For Git sources, `addons.lock` pins the resolved commit SHA. On later
`gpm install` runs, `gpm` fetches that exact commit when the lock entry still
matches the manifest.

## GitHub Releases

GitHub release sources use the GitHub REST API to find a release by tag and
download one release asset:

```toml
[addons.some_plugin]
source = "github-release"
repo = "owner/some_plugin"
version = "1.4.0"
asset = "*.zip"
```

If `asset` is empty, the release must contain exactly one asset. If an asset
glob matches zero or multiple assets, `gpm` fails and asks you to disambiguate.

Set `GITHUB_TOKEN` or `GH_TOKEN` for private releases or higher rate limits.

## Archives

Archive sources fetch a direct HTTP(S) URL:

```toml
[addons.raw_thing]
source = "archive"
url = "https://example.com/thing-1.0.zip"
checksum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

Use `checksum` for archive and release sources whenever the upstream host does
not provide immutable URLs. The checksum is verified on the first fetch and on
locked installs.

If the archive host requires credentials, include them in the URL format the
host supports. `gpm` does not manage an archive credential store.

## Godot AssetLib

`gpm assetlib add` searches or fetches an asset from Godot AssetLib, resolves
the asset to its generated download URL, and stores it as an `archive` source in
`addons.toml`:

```toml
[addons.dialogue_engine]
source = "archive"
url = "https://example.com/dialogue-engine/archive/main.zip"
version = "1.6.0"
checksum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
```

There is no separate `source = "assetlib"` manifest type. Once the AssetLib
entry is resolved, installs use the same archive download, extraction, checksum,
and lockfile behavior as any other archive source.

The `version` field records the AssetLib version label as advisory metadata.
When AssetLib provides a SHA-256 `download_hash`, `gpm assetlib add` records it
as `checksum`; that checksum is what verifies the fetched archive.
