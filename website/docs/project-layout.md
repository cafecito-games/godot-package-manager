---
title: Project layout
description: Where gpm looks for project files and installs addons.
---

# Project Layout

`gpm` works from a Godot project root. For most commands, it starts in the
current directory and walks upward until it finds `project.godot`.

```text
game/
  project.godot
  addons.toml
  addons.lock
  .gpm-state.toml
  addons/
    dialogue_manager/
    some_plugin/
```

`addons.toml`, `addons.lock`, and `.gpm-state.toml` live next to
`project.godot`. Installed addons are placed under `<project-root>/addons/`.

## Machine-Local State

`.gpm-state.toml` records which [platform slices](slices.md) this particular
disk holds for each addon and the installed file paths used for completeness
checks. It is machine-local: `gpm init` adds it to `.gitignore`, because
committing it would publish one machine's layout to the whole team.

```toml
[addons]
  [addons.limboai]
    resolved_version = "1.4.0"
    slices = ["core", "linux.x86_64", "macos", "windows.x86_64"]
    pin = "4a79717d9d4b2573188bb7396e10b37e5e5852531302542d3741dd00c67ff217"
    file_manifest_version = 1
    files = ["plugin.cfg", "scripts/limbo_state.gd"]
```

| Key | Meaning |
| --- | --- |
| `resolved_version` | The version these slices came from. |
| `slices` | The slice IDs present in `addons/` on this disk. |
| `pin` | The lockfile pin these slices were materialized from. |
| `file_manifest_version` | The completeness-check schema used for this install. |
| `files` | Relative paths that must still be regular files under the installed addon. |

`pin` is a sliced addon's `index_sha256` or an unsliced addon's archive
checksum, and it is empty for a `git` source, whose resolved commit SHA is
already its content identity. It is recorded because a resolved version does not
identify content: a release keeps its tag when it is republished, so two branches
of one repository can share an `addons.toml` — and therefore a `spec_hash` and a
resolved version — while their committed `addons.lock` files pin different bytes.
Comparing the pin is what stops a state entry written against one lock from
satisfying another and leaving the wrong files in place, unfetched and unverified.

The version 1 file manifest checks paths, not file contents. If a recorded file
has been deleted or replaced by something other than a regular file, the next
`gpm install` re-fetches and replaces that addon's directory. Editing an existing
file or adding a new file does not trigger a re-fetch, so developing an addon in
place does not create an install loop. `gpm list` reports an addon with a missing
recorded file as not installed. Removing an empty directory is not detected.
Because repair replaces the whole addon directory, any local edits can still be
lost when a different recorded file is missing; commit or copy work you need
before using `gpm install` to repair an addon.

State entries written by older gpm versions have no file manifest. The first
`gpm install` after upgrading re-materializes each such addon once and records
the manifest; later intact installs use the normal no-fetch fast path.

`.gpm-state.toml` is never authoritative over `addons.lock`. It answers which
slice IDs this machine materialized and whether required file paths remain
present, never whether their bytes are correct, so discarding it costs a
re-download and nothing more. An unparseable state file is reported and treated
as empty rather than failing the run.

If `gpm init` finds that `.gitignore` is not a regular file — a symbolic link,
for example — it refuses to write through it and reports a manifest error.

## Project Discovery

Commands that operate on a project support `--dir`:

```bash
gpm install --dir path/to/game
gpm add --dir path/to/game --name my_addon --source archive --url https://example.com/addon.zip
```

When `--dir` is omitted, discovery starts from the current working directory.
If no `project.godot` is found, project commands fail with a project discovery
error.

`gpm init` is the exception. It creates `addons.toml` in the current directory,
or in the directory passed with `--dir`.

## Installed Names

By default, an addon installs under `addons/<table-key>/`. Use `install_as` to
choose a different directory name:

```toml
[addons.dialogue_manager]
source = "git"
url = "https://github.com/nathanhoad/godot_dialogue_manager.git"
version = "v2.1.0"
source_path = "addons/dialogue_manager"
install_as = "dialogue_manager"
```

Addon names and `install_as` values must be single directory names. Absolute
paths, path separators, `.` and `..` are rejected.

`install_as` works for a sliced addon too: the installed `.gdextension`'s entry
values are re-rooted from the addon's published directory to the one it is
installed under.
