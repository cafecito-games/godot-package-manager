# gam — Godot Addon Manager

`gam` is a command-line addon manager for Godot projects. You check in an
`addons.toml` manifest that declares every addon your project depends on and
where to obtain it. `gam` resolves and installs those addons into the project's
`addons/` directory. A committed `addons.lock` file pins exact versions for
reproducible installs across machines and CI runs.

---

## Installation

### Homebrew

```sh
brew install cafecito-games/tap/gam
```

### From source

Requires **Go 1.26.2 or newer** and `git` on your `PATH`.

```sh
go install github.com/cafecito-games/godot-addon-manager/cmd/gam@latest
```

Or build locally:

```sh
git clone https://github.com/cafecito-games/godot-addon-manager
cd godot-addon-manager
go build ./cmd/gam
```

---

## Project layout

`gam` discovers the Godot project root by walking up from the current directory
until it finds `project.godot`. `addons.toml` and `addons.lock` live alongside
`project.godot`. Addons are installed under `<project-root>/addons/`.

If no project root is found, `gam` fails with a clear error (the exception is
`gam init`, which creates `addons.toml` in the current directory).

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

When `source_path` is omitted, `gam` inspects the fetched tree:

1. If a single `addons/<name>/` directory exists, that directory is used.
2. Otherwise the root of the fetched tree is used.

If the source has multiple directories under `addons/`, `gam` fails and
instructs you to set `source_path` explicitly.

---

## Commands

### `gam init`

Create a starter `addons.toml` in the current directory.

```sh
gam init
```

### `gam add`

Add an addon to `addons.toml` and install it immediately.

**Interactive (TUI wizard):** run with no flags.

```sh
gam add
```

**Non-interactive:** pass all values as flags.

```sh
gam add --name dialogue_manager \
        --source git \
        --url https://github.com/nathanhoad/godot_dialogue_manager.git \
        --version v2.1.0

gam add --name some_plugin \
        --source github-release \
        --repo owner/some_plugin \
        --version 1.4.0 \
        --asset some_plugin.zip

gam add --name raw_thing \
        --source archive \
        --url https://example.com/thing-1.0.zip
```

Available flags: `--name`, `--source`, `--url`, `--repo`, `--version`,
`--asset`, `--source-path`, `--install-as`, `--dir`.

### `gam remove <name>`

Remove an addon from `addons.toml` and `addons.lock` and delete its installed
directory under `addons/`.

```sh
gam remove dialogue_manager
```

### `gam install`

Install all addons declared in `addons.toml`, honoring `addons.lock` where
entries are consistent with the manifest.

```sh
gam install
```

If an addon's manifest entry differs from its lock entry (or no lock entry
exists), that addon is re-resolved and the lock is updated.

### `gam update [name...]`

Re-resolve all addons (or only the named ones), install them, and rewrite
`addons.lock`. Existing lock pins are ignored.

```sh
gam update                  # update everything
gam update dialogue_manager # update a single addon
```

### `gam list`

List all configured addons with their resolved/installed state.

```sh
gam list
```

### `gam completion`

Generate shell completion scripts (provided by Cobra).

```sh
gam completion bash   # add to ~/.bashrc
gam completion zsh    # add to ~/.zshrc
gam completion fish
gam completion powershell
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

`gam` shells out to the system `git` binary. Private repositories work
automatically via your existing SSH keys and credential helpers — no credentials
are stored by `gam`.

### GitHub release sources

Set `GITHUB_TOKEN` (or `GH_TOKEN` as a fallback) to authenticate API requests
for private repositories or to avoid rate limiting. Public repositories work
without a token.

```sh
export GITHUB_TOKEN=ghp_...
gam install
```

### Archive sources

Archives are fetched over plain HTTPS. Embed credentials in the URL if the host
requires authentication. `gam` does not manage a credential store.

---

## The lockfile

`addons.lock` is generated by `gam` and should be committed to version control.
It pins:

- The resolved commit SHA (git sources) or the SHA-256 of the downloaded asset
  (github-release and archive sources).
- The resolved `version` and `source_path` actually used.
- A hash of the corresponding `addons.toml` entry, to detect manifest drift.

`gam install` uses `addons.lock` when present. If an entry is consistent with
the manifest the pinned version is installed without re-fetching. `gam update`
always ignores existing pins, re-resolves, and rewrites the lockfile.
