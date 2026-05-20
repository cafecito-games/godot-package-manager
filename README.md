# gpm - Godot Package Manager

`gpm` is a command-line addon manager for Godot projects. It installs addons
declared in `addons.toml` into your project's `addons/` directory and writes an
`addons.lock` file for reproducible installs across machines and CI.

Full documentation: <https://cafecito-games.github.io/godot-package-manager/>

## Install

macOS:

```bash
brew install --cask cafecito-games/tap/gpm
```

Go:

```bash
go install github.com/cafecito-games/godot-package-manager/cmd/gpm@latest
```

Requires Go 1.26.2+ when installing from source. Git sources also require
`git` on `PATH`.

## Quick Usage

Create a manifest next to `project.godot`:

```bash
gpm init
```

Add an addon interactively:

```bash
gpm add
```

Or add one non-interactively:

```bash
gpm add --name dialogue_manager \
  --source git \
  --url https://github.com/nathanhoad/godot_dialogue_manager.git \
  --version v2.1.0 \
  --source-path addons/dialogue_manager
```

Install declared addons:

```bash
gpm install
```

Update pins intentionally:

```bash
gpm update
gpm update dialogue_manager
```

List configured addons:

```bash
gpm list
```

Commit `addons.toml` and `addons.lock` so installs stay reproducible.

## Source Types

`gpm` supports:

- `git`: clone a Git repository at a tag, branch, or commit SHA.
- `github-release`: download one asset from a GitHub release.
- `archive`: download a direct HTTP(S) zip or tar archive.

Use `source_path` when the addon lives inside a subdirectory of the fetched
source, and `install_as` when the installed directory name should differ from
the manifest key.

## Development

```bash
go test ./...
go test -race ./...
go build ./cmd/gpm
```

The documentation site lives in `website/`:

```bash
cd website
npm ci
npm run build
```
