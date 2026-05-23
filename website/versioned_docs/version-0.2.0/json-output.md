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
