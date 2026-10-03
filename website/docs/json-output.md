---
title: JSON output
description: Use gpm from CI, scripts, and automation.
---

# JSON Output

Use `--json` when another tool needs structured output:

```bash
gpm --json list
gpm --json install
gpm --json update dialogue_manager
gpm --json assetlib search dialogue --godot-version 4.2
```

`--json` is supported by:

- `add`
- `assetlib search`
- `assetlib add`
- `install`
- `update`
- `list`
- `package`

## Add, Install, And Update

Successful install operations emit one result per installed addon:

```json
[
  {
    "name": "dialogue_manager",
    "resolved_version": "8f7c2f0c...",
    "install_path": "dialogue_manager"
  }
]
```

For an addon published as [platform slices](slices.md), each result also carries
a `slices` array:

```json
[
  {
    "name": "limboai",
    "resolved_version": "1.4.0",
    "install_path": "limboai",
    "slices": [
      "core",
      "macos"
    ]
  }
]
```

`slices` reports what is **on disk**, not what the addon publishes and not what
the project declares. Under `--host-only` it lists `core` plus the host's slice;
under `--all-platforms` it lists every published slice. The field is omitted for
an unsliced addon. A format-2 install also reports an `artifacts` array containing
the non-selectable shared dependencies materialized for those slices.

## List

`gpm --json list` reports manifest entries and local install state:

```json
[
  {
    "name": "dialogue_manager",
    "source": "git",
    "version": "v2.1.0",
    "installed": true
  }
]
```

A sliced addon carries the same `slices` array, read from `.gpm-state.toml`, so
it too reports what this machine materialized rather than the published or
declared set:

```json
[
  {
    "name": "limboai",
    "source": "archive",
    "version": "1.4.0",
    "installed": true,
    "slices": [
      "core",
      "linux.x86_64",
      "macos",
      "windows.x86_64"
    ]
  }
]
```

## Package

`gpm --json package` reports the slices one packaging run published. When fan-out
bytes are shared by several architecture slices, it also reports an `artifacts`
array with the same `id`, `file`, `size`, and `sha256` shape:

```json
{
  "name": "limboai",
  "version": "1.4.0",
  "index": "/path/to/addon-repo/dist/gpm-index.toml",
  "slices": [
    {
      "id": "core",
      "file": "limboai-1.4.0-core.zip",
      "size": 410,
      "sha256": "e8de5ee7b224e7aa447885ec3c248f04151e111f5bd9f78bdbfc85aa25fbb8c4"
    },
    {
      "id": "android.arm64",
      "file": "limboai-1.4.0-android.arm64.zip",
      "size": 178,
      "sha256": "3a3441a0f24f45b89d7ee14a7b4ddf4458bbbdf715e652155f8b6695b1d70514"
    },
    {
      "id": "android.x86_64",
      "file": "limboai-1.4.0-android.x86_64.zip",
      "size": 182,
      "sha256": "4a3441a0f24f45b89d7ee14a7b4ddf4458bbbdf715e652155f8b6695b1d70514"
    },
    {
      "id": "macos",
      "file": "limboai-1.4.0-macos.zip",
      "size": 194,
      "sha256": "2a3441a0f24f45b89d7ee14a7b4ddf4458bbbdf715e652155f8b6695b1d70514"
    }
  ],
  "artifacts": [
    {
      "id": "android",
      "file": "limboai-1.4.0-shared-android.zip",
      "size": 76000,
      "sha256": "8ffda2f9a5237ddc7551d0d34db55cf01b79260ad9943166a54d829f57100f7b"
    }
  ]
}
```

## AssetLib Search

`gpm --json assetlib search <query>` emits AssetLib result objects:

```json
[
  {
    "asset_id": "2598",
    "title": "Dialogue Engine",
    "author": "Rubonnek",
    "author_id": "2467",
    "category": "Tools",
    "category_id": "5",
    "godot_version": "4.2",
    "rating": "0",
    "cost": "MIT",
    "support_level": "community",
    "icon_url": "https://example.com/icon.png",
    "version": "12",
    "version_string": "1.6.0",
    "modify_date": "2026-02-27 22:05:18"
  }
]
```

`gpm --json assetlib add <asset-id>` emits the same add/install result payload
as `gpm --json add`.

## Errors

When `--json` is set and a command fails, `gpm` emits an error object:

```json
{
  "error": "addon \"missing\" is unknown",
  "code": 3
}
```

Exit codes are still set, so scripts should check both the process status and
the JSON payload.

## Exit Codes

| Code | Meaning |
| --- | --- |
| 0 | Success. |
| 1 | Generic or unexpected error. |
| 2 | Usage error, such as bad flags or arguments. |
| 3 | Manifest or lockfile error. |
| 4 | Fetch error, such as network, auth, or source lookup failure. |
| 5 | Install error, such as filesystem or extraction failure. |
