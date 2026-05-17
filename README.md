# gpm — Godot Package Manager

`gpm` is a command-line addon manager for Godot projects. You check in an
`addons.toml` manifest that declares every addon your project depends on and
where to obtain it. `gpm` resolves and installs those addons into the project's
`addons/` directory. A committed `addons.lock` file pins exact versions for
reproducible installs across machines and CI runs.

---

## Installation

### macOS

```sh
brew install --cask cafecito-games/tap/gpm
```

If Homebrew reports that `cafecito-games/tap/gpm` is unavailable after a new
release, refresh the tap checkout and retry:

```sh
brew untap cafecito-games/tap
brew tap cafecito-games/tap
brew install --cask cafecito-games/tap/gpm
```

### Linux

```sh
go install github.com/cafecito-games/godot-package-manager/cmd/gpm@latest
```

### From source

Requires **Go 1.26.2 or newer** and `git` on your `PATH`.

```sh
go install github.com/cafecito-games/godot-package-manager/cmd/gpm@latest
```

Or build locally:

```sh
git clone https://github.com/cafecito-games/godot-package-manager
cd godot-package-manager
go build ./cmd/gpm
```

---

## Project layout

`gpm` discovers the Godot project root by walking up from the current directory
until it finds `project.godot`. `addons.toml` and `addons.lock` live alongside
`project.godot`. Addons are installed under `<project-root>/addons/`.

If no project root is found, `gpm` fails with a clear error (the exception is
`gpm init`, which creates `addons.toml` in the current directory).

---

## addons.toml

`addons.toml` is a hand-editable (or TUI-edited) TOML file that declares your
addons. It lives next to `project.godot`.

### Git source

```toml
[addons.dialogue_manager]
source      = "git"
url         = "https://github.com/nathanhoad/godot_dialogue_manager.git"
version     = "v2.1.0"          # git tag, branch, or SHA
source_path = "addons/dialogue_manager"  # optional; see auto-detection below
install_as  = "dialogue_manager"         # optional; defaults to the table key
```

### GitHub release source

```toml
[addons.some_plugin]
source  = "github-release"
repo    = "owner/some_plugin"   # GitHub owner/repo
version = "1.4.0"               # release tag
asset   = "some_plugin.zip"     # optional; asset name or glob
source_path = "addons/some_plugin"  # optional
```

### Archive source

```toml
[addons.raw_thing]
source = "archive"
url    = "https://example.com/thing-1.0.zip"  # zip or tarball
```

### Field reference

| Field         | Applies to              | Required | Notes |
|---------------|-------------------------|----------|-------|
| `source`      | all                     | yes      | `git`, `github-release`, or `archive` |
| `url`         | git, archive            | yes      | clone URL or archive URL |
| `repo`        | github-release          | yes      | `owner/repo` |
| `version`     | git, github-release     | yes      | git ref or release tag |
| `asset`       | github-release          | no       | asset name or glob; required when a release has more than one asset |
| `source_path` | all                     | no       | subdirectory within the fetched tree to install; see auto-detection below |
| `install_as`  | all                     | no       | directory name under `addons/`; defaults to the addon's table key |

### `source_path` auto-detection

When `source_path` is omitted, `gpm` inspects the fetched tree:

1. If a single `addons/<name>/` directory exists, that directory is used.
2. Otherwise the root of the fetched tree is used.

If the source has multiple directories under `addons/`, `gpm` fails and
instructs you to set `source_path` explicitly.

---

## Commands

### `gpm init`

Create a starter `addons.toml` in the current directory.

```sh
gpm init
```

### `gpm add`

Add an addon to `addons.toml` and install it immediately.

**Interactive (TUI wizard):** run with no flags.

```sh
gpm add
```

**Non-interactive:** pass all values as flags.

```sh
gpm add --name dialogue_manager \
        --source git \
        --url https://github.com/nathanhoad/godot_dialogue_manager.git \
        --version v2.1.0

gpm add --name some_plugin \
        --source github-release \
        --repo owner/some_plugin \
        --version 1.4.0 \
        --asset some_plugin.zip

gpm add --name raw_thing \
        --source archive \
        --url https://example.com/thing-1.0.zip
```

Available flags: `--name`, `--source`, `--url`, `--repo`, `--version`,
`--asset`, `--source-path`, `--install-as`, `--dir`.

### `gpm remove <name>`

Remove an addon from `addons.toml` and `addons.lock` and delete its installed
directory under `addons/`.

```sh
gpm remove dialogue_manager
```

### `gpm install`

Install all addons declared in `addons.toml`, honoring `addons.lock` where
entries are consistent with the manifest.

```sh
gpm install
```

If an addon's manifest entry differs from its lock entry (or no lock entry
exists), that addon is re-resolved and the lock is updated.

### `gpm update [name...]`

Re-resolve all addons (or only the named ones), install them, and rewrite
`addons.lock`. Existing lock pins are ignored.

```sh
gpm update                  # update everything
gpm update dialogue_manager # update a single addon
```

### `gpm list`

List all configured addons with their resolved/installed state.

```sh
gpm list
```

### `gpm completion`

Generate shell completion scripts (provided by Cobra).

```sh
gpm completion bash   # add to ~/.bashrc
gpm completion zsh    # add to ~/.zshrc
gpm completion fish
gpm completion powershell
```

---

## Global flags

| Flag              | Short | Default | Description |
|-------------------|-------|---------|-------------|
| `--json`          |       | false   | Emit machine-readable JSON output (useful for AI agents and scripts) |
| `--verbose`       | `-v`  | false   | Enable verbose logging |
| `--quiet`         | `-q`  | false   | Suppress non-error output |

`--json` is supported by `add`, `install`, `update`, and `list`. On failure,
these commands emit a JSON object with an `error` field.

---

## Exit codes

| Code | Meaning |
|------|---------|
| 0    | Success |
| 1    | Generic / unexpected error |
| 2    | Usage error (bad flags or arguments) |
| 3    | Manifest or lockfile error (parse, validation) |
| 4    | Fetch error (network, auth, source not found) |
| 5    | Install error (filesystem, extraction) |

---

## Authentication

### Git sources

`gpm` shells out to the system `git` binary. Private repositories work
automatically via your existing SSH keys and credential helpers — no credentials
are stored by `gpm`.

### GitHub release sources

Set `GITHUB_TOKEN` (or `GH_TOKEN` as a fallback) to authenticate API requests
for private repositories or to avoid rate limiting. Public repositories work
without a token.

```sh
export GITHUB_TOKEN=ghp_...
gpm install
```

### Archive sources

Archives are fetched over plain HTTPS. Embed credentials in the URL if the host
requires authentication. `gpm` does not manage a credential store. Archive
downloads are capped at 512 MiB.

---

## The lockfile

`addons.lock` is generated by `gpm` and should be committed to version control.
It pins:

- The resolved commit SHA (git sources) or the SHA-256 of the downloaded asset
  (github-release and archive sources).
- The resolved `version` and `source_path` actually used.
- A hash of the corresponding `addons.toml` entry, to detect manifest drift.

`gpm install` uses `addons.lock` when present. If an entry is consistent with
the manifest the pinned version is installed without re-fetching. `gpm update`
always ignores existing pins, re-resolves, and rewrites the lockfile.
