# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

`gpm` (Go module `github.com/cafecito-games/godot-package-manager`) is a CLI addon manager for
Godot projects. It reads `addons.toml` next to `project.godot`, fetches each declared addon,
installs it under `addons/`, and writes `addons.lock` for reproducible installs.

Note the naming mismatch: the checkout directory is `godot-addon-manager`, but the module,
repository, and binary are `godot-package-manager` / `gpm`.

## Commands

```bash
go build ./cmd/gpm          # build the binary
go test ./...               # full test suite
go test -race ./...         # what CI runs
go vet ./...
gofmt -l -w .
golangci-lint run ./...     # golangci-lint v2 config in .golangci.yml
go mod tidy && git diff --exit-code go.mod go.sum   # CI fails if not tidy
```

Single test / package:

```bash
go test ./internal/installer -run TestInstall -v
go test ./internal/cli -run TestEndToEndInstallGitAddon -v
```

Docs site (Docusaurus, in `website/`):

```bash
cd website && npm ci && npm run start   # npm run build is what the Pages workflow runs
```

Pre-commit hooks are managed with `prek` (`.pre-commit-config.yaml`): `prek install`,
`prek run --all-files`. The hook set runs gofmt, vet, mod tidy, golangci-lint and `go test ./...`,
and blocks direct commits to `main`.

## Architecture

Request flow for an install: `cmd/gpm` → `internal/cli` (cobra commands) → `internal/cli.Runner`
→ `internal/source` (fetch into temp dir) → `internal/installer` (atomic copy into `addons/`)
→ `internal/manifest` (write `addons.lock`).

- **`internal/project`** — locates the Godot project by walking up for `project.godot`, and
  derives `addons.toml`, `addons.lock`, `addons/` from that root. Also best-effort parses the
  Godot version out of `config/features`.
- **`internal/manifest`** — `AddonSpec`/`Manifest` (`addons.toml`), `LockEntry`/`Lockfile`
  (`addons.lock`), and validation. `AddonSpec.Hash()` hashes every resolvable field; `NeedsResolve`
  compares that hash against the lock's `spec_hash` to detect drift, so **any new `AddonSpec` field
  that affects resolution must be added to `Hash()`**. Both `Manifest.Save` and `Lockfile.Save`
  write via temp file + rename.
- **`internal/source`** — the `Fetcher` interface plus `git`, `archive`, and `github-release`
  implementations, selected by `FetcherForWithLimits(Limits)`. A fetch returns a temp dir, a
  resolved version (commit SHA or tag), and a SHA-256 checksum (empty for git). **The caller owns
  removing `FetchResult.Dir`.** `github-release` reads `GITHUB_TOKEN`, falling back to `GH_TOKEN`.
- **`internal/installer`** — resolves the subtree to install (explicit `source_path`, else a single
  auto-detected `addons/<name>/`, else the fetched root), then stages a copy and renames it into
  place, keeping a `.gpm-backup` so an interrupted swap can be recovered on the next run.
- **`internal/cli.Runner`** (`internal/cli/run.go`) — the shared orchestrator for `install` and
  `update`, distinguished only by `InstallMode`. `ModeInstall` honors a consistent lock entry (and
  for git pins the fetch to the locked SHA); `ModeUpdate` re-resolves everything. The lock is saved
  after each addon, and a full run prunes entries no longer in the manifest.
- **`internal/assetlib`** — HTTP client for the Godot Asset Library API, resolving assets to
  `archive` entries. Response bodies are size-capped.
- **`internal/tui`** — Bubble Tea models for the interactive `add` wizard and AssetLib browser.
- **`internal/output`** — exit-code taxonomy and the single JSON/text rendering path.

### Cross-cutting conventions

- **Errors carry their exit code.** Wrap failures in `output.ManifestError` (3), `output.FetchError`
  (4), or `output.InstallError` (5); `cli.UsageError` maps to 2. `output.CodeFor` unwraps via
  `errors.As`, so always wrap rather than returning bare errors from these layers.
- **Every command supports `--json`.** Build a struct with JSON tags and pass it through
  `output.Render(w, opts.JSON, payload, textFn)`; the text branch must respect `opts.Quiet`.
  Diagnostics go through `verbosef`, never `fmt.Print`.
- **Global flags live in one `cli.Options`** constructed in `newRootCommand` and passed to every
  subcommand constructor. Commands take `--dir` and go through `loadProject` so discovery,
  parsing, and validation stay uniform.
- **Nothing writes to stdout/stderr directly.** Use `cmd.OutOrStdout()` / `cmd.ErrOrStderr()` so
  tests can capture output; `cli.Execute(args, stdout, stderr)` is the single entry point shared by
  `main` and the end-to-end tests.
- **Seams for tests are package-level vars** — `Runner.FetcherFor`, `testFetcherFor`,
  `testAssetLibClient`, `runTUI`, `runAssetLibTUI`, `latestStableGodotVersion`. Prefer extending
  this pattern over introducing network access in tests. Tests use `testify/require` and
  `t.TempDir()`; some end-to-end tests shell out to real `git`.
- **Untrusted input is treated as hostile.** Addon names, `source_path`, `install_as`, and `exclude`
  are validated against traversal; the installer re-checks containment, rejects symlinks whose targets
  are absolute or escape the install root, and downloads are bounded by `--max-download-size` / `--max-extract-size`.

## Releases

Releases are cut by the `Release` workflow (`workflow_dispatch` with a `patch`/`minor`/`major`
bump, which must run from `main`). It tags, runs GoReleaser (binaries plus the Homebrew cask), then
snapshots the Docusaurus docs and opens an auto-merging PR — `main` is protected, so CI cannot push
to it. The version string is injected into `internal/cli.version` via ldflags and is `dev` locally.
