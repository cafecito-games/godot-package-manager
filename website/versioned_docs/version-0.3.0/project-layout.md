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
  addons/
    dialogue_manager/
    some_plugin/
```

`addons.toml` and `addons.lock` live next to `project.godot`. Installed addons
are placed under `<project-root>/addons/`.

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
