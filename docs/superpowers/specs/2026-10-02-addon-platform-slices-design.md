# Addon Platform Slices

Date: 2026-10-02
Status: approved for implementation

## Problem

A GDExtension addon ships prebuilt binaries for every platform its author supports: Linux,
Windows, macOS, Android, iOS, and web. A project that targets only iOS downloads and stores all of
them. For addons like LimboAI, Jolt, or Rapier the native binaries dominate the download, so the
waste is most of the bytes.

A project should declare the platforms it targets and receive only the artifacts those platforms
need, plus the platform-independent GDScript and resources.

## Solution overview

An addon is published as **slices**: one `core` slice holding everything platform-independent, and
one slice per platform the addon supports. `gpm package` produces slices and an index describing
them; `gpm install` reads the index and downloads only the slices the project needs.

Two sides, one format:

- **Producer** — `gpm package` reads `gpm-package.toml` in the addon repository, partitions the
  addon tree into slices, and writes `dist/<name>-<version>-<slice>.zip` plus `dist/gpm-index.toml`.
  Publishing the artifacts remains the author's job (`gh release upload dist/*`).
- **Consumer** — `addons.toml` declares `[project] platforms`. On install, gpm fetches the index,
  computes the needed slice set, downloads only those, merges them into one tree, reassembles the
  `.gdextension`, and installs it through the existing installer.

### Scope boundary

Slices are a property of release artifacts, not of source checkouts. `source = "git"` addons install
whole, exactly as today. Slicing applies to `github-release` and `archive` sources only.

## Slice identity

A slice ID is a Godot platform tag as it appears in a `.gdextension` `[libraries]` key, reduced to
its platform and optional architecture components:

| `.gdextension` key | Slice ID |
| --- | --- |
| `macos.debug` | `macos` |
| `ios.template_release` | `ios` |
| `ios.template_release.arm64` | `ios.arm64` |
| `windows.debug.x86_64` | `windows.x86_64` |
| `android.template_debug.arm64` | `android.arm64` |
| `web.template_release.wasm32` | `web.wasm32` |

Rationale: the `.gdextension` file is already a per-platform index of exactly the binaries to split,
so packaging derives the partition mechanically and install-time reassembly is exact rather than
heuristic.

Known platforms: `linux`, `macos`, `windows`, `android`, `ios`, `web`.
Known architectures: `x86_32`, `x86_64`, `arm32`, `arm64`, `rv64`, `wasm32`, `universal`.

The build target axis (`editor` / `template_debug` / `template_release`) is **not** part of a slice
ID. A slice carries every target for its platform. A project that ships a platform needs both debug
and release binaries for it, and the size win comes from dropping whole platforms. Because slice IDs
are structured, a future `format = 2` may offer target-level slices as a compatible extension.

## Package format

### Author config — `gpm-package.toml`, committed in the addon repository

```toml
[package]
name       = "limboai"
addon_path = "addons/limboai"   # the subtree that becomes addons/<name>/
version    = "1.4.0"            # --version overrides

[package.slices]
# Optional extras: paths the .gdextension does not reference.
"android.arm64" = ["android/limboai.aar"]
"ios.arm64"     = ["ios/LimboAI.xcframework/**"]
```

Partitioning rule: every file referenced by a platform-tagged `.gdextension` key goes to that key's
slice; every path matched by a `[package.slices]` glob goes to the named slice; everything else goes
to `core`. Extras are required in practice — Android plugins ship `.aar` files and iOS ships
`.xcframework` bundles that no `[libraries]` key references.

### Generated index — `gpm-index.toml`, published beside the slice archives

```toml
format  = 1
name    = "limboai"
version = "1.4.0"

[slices.core]
file   = "limboai-1.4.0-core.zip"
sha256 = "9f2a..."
size   = 182344

[slices."ios.arm64"]
file   = "limboai-1.4.0-ios.arm64.zip"
sha256 = "41bc..."
size   = 4821001

  [slices."ios.arm64".libraries."limboai.gdextension"]
  "ios.template_debug"   = "res://addons/limboai/bin/liblimboai.ios.template_debug.xcframework"
  "ios.template_release" = "res://addons/limboai/bin/liblimboai.ios.template_release.xcframework"
```

- `format` is validated first. An index whose `format` exceeds the version gpm understands is a
  `FetchError`, never a best-effort parse.
- Each archive holds the addon subtree **unprefixed**, so merging selected slices is "extract all
  into one staging root" with no path rewriting.
- Split `.gdextension` entries live in the index, not inside the slice archives, because gpm already
  downloads the index first. Reassembly needs no reserved paths inside archives.
- The partition applies to any platform-tagged `.gdextension` section, which today means
  `[libraries]` and `[dependencies]`, keyed by the `.gdextension` file's path relative to the addon
  root so multi-extension addons work.
- `core` ships each `.gdextension` with its platform-tagged sections emptied and every other section
  (`[configuration]`, `entry_symbol`, `compatibility_minimum`) byte-preserved.

The alternative — shipping the full `[libraries]` table in `core` and leaving entries pointing at
absent files — mostly works, because Godot resolves only the current platform. It was rejected
because it converts "I forgot to list `android.arm64`" into a confusing Godot export failure instead
of a clean gpm error. After reassembly the installed `.gdextension` describes exactly what is on
disk.

## Consumer configuration

### `addons.toml`

```toml
[project]
platforms = ["ios.arm64", "android.arm64"]

[addons.limboai]
source  = "github-release"
repo    = "limbonaut/limboai"
version = "v1.4.0"

[addons.editor_only_tool]
source    = "github-release"
repo      = "owner/tool"
version   = "v1.0.0"
platforms = ["macos"]           # per-addon override replaces the project list
```

`Manifest.Load` resolves the effective platform list onto each `AddonSpec`, the same way it already
stamps `Name` from the table key, so `AddonSpec.Hash()` keeps its signature while covering the
declared list for drift detection.

### The needed slice set

```
needed = {core} ∪ declared ∪ {host}
```

The **host slice is implicit** and is **excluded from `Hash()`**. A team shipping iOS still needs
macOS or Windows binaries for the addon to load in the editor, and CI needs the runner's host
binaries because Godot's headless export uses the editor binary. Making the host implicit means a
Linux teammate is not broken by a manifest written on a Mac. Excluding it from `Hash()` is what keeps
`spec_hash` identical across machines so `addons.lock` does not churn.

Host resolution maps `runtime.GOOS`/`GOARCH` to a candidate chain and picks the first slice the
index publishes, because Godot tags carry an architecture only sometimes and macOS libraries are
usually universal:

| Host | Candidate chain |
| --- | --- |
| `darwin/arm64` | `macos.arm64`, `macos.universal`, `macos` |
| `darwin/amd64` | `macos.x86_64`, `macos.universal`, `macos` |
| `linux/amd64` | `linux.x86_64`, `linux` |
| `linux/arm64` | `linux.arm64`, `linux` |
| `windows/amd64` | `windows.x86_64`, `windows` |
| `windows/arm64` | `windows.arm64`, `windows` |

A host with no published slice is not an error: the addon may simply not support it, and that is the
author's statement, not a project misconfiguration. It is reported as a diagnostic through
`verbosef`.

A **declared** platform the index does not publish **is** an error (`FetchError`), with a message
listing the slices that are published.

`--all-platforms` on `install` and `update` selects every slice in the index, for projects that
vendor `addons/` into git.

### `addons.lock`

```toml
[addons.limboai]
resolved_version = "v1.4.0"
source_path      = ""
spec_hash        = "7d1e..."
index_sha256     = "a004..."

  [addons.limboai.slices]
  core             = "9f2a..."
  "ios.arm64"      = "41bc..."
  "android.arm64"  = "cc70..."
  "macos"          = "1182..."
  "windows.x86_64" = "77ae..."
```

Every slice in the index is recorded, not only the installed ones, so the lock stays
machine-independent while materialization stays local. `index_sha256` pins the index itself, so a
retagged release cannot quietly repoint slices. Unsliced addons keep using `checksum` and gain
neither field; an existing lockfile remains valid and takes the unsliced path.

### `.gpm-state.toml` — machine-local, gitignored

```toml
[addons.limboai]
resolved_version = "v1.4.0"
slices           = ["core", "ios.arm64", "android.arm64", "macos"]
```

Needed because `spec_hash` is machine-independent by design: a teammate cloning a repository with a
consistent lock would otherwise be told nothing is to do while their host slice is absent from disk.
Install compares needed against present and materializes the difference. A missing or stale state
file is safe — it means re-download. State is never authoritative over the lock.

## Architecture

Request flow for a sliced install:

```
cmd/gpm → internal/cli (cobra) → internal/cli.Runner
        → internal/source (fetch index, fetch needed slices, merge into one temp dir)
            └── internal/slice (tag parsing, selection, index, .gdextension reassembly)
        → internal/installer (unchanged: atomic copy into addons/)
        → internal/manifest (write addons.lock and .gpm-state.toml)
```

### `internal/slice` (new)

Pure logic, no network and no disk beyond paths handed to it:

- platform tag parse and validate against the known platform × architecture sets
- host detection and the candidate chain
- the `gpm-index.toml` type: load, save, and `SelectSlices(declared, host, allPlatforms)`
- `.gdextension` partition (packaging) and reassembly (install), a round-trippable pair

This package exists so the `.gdextension` surgery — the fiddliest logic in the feature — is testable
exhaustively without HTTP or the atomic swap.

### `internal/packager` (new)

`gpm package`: read `gpm-package.toml`, partition the addon tree, write slice archives and the
index. No network. Publishing is out of scope; the author's existing release tooling uploads
`dist/*`.

### `internal/source` (changed)

`Fetch` keeps returning a single directory. The `github-release` and `archive` fetchers gain index
discovery and multi-slice download, extracting every selected slice into one staging directory.
`FetchResult` grows `Slices []SliceResult` (ID, checksum) and `IndexChecksum string` purely so the
lock and state can be written.

This placement leaves the installer, `exclude`, symlink rejection, containment re-checks, and the
backup/rename swap byte-identical, and preserves both the `Fetcher` contract and the "caller owns
`FetchResult.Dir`" rule.

Slice detection:

- `github-release` — the asset list is already fetched; a release asset named `gpm-index.toml` means
  the addon is sliced. Consumers need no manifest change and `gpm add` works unmodified. The
  behavior depends on remote content, which is acceptable because the lock's `slices` table makes it
  visible in a git diff.
- `archive` — a bare URL has no listing, so an explicit `index = "<url>"` field on `AddonSpec` marks
  the addon as sliced.

Merging two slices that contain the same path is an author bug and fails with a `FetchError` naming
the path and both slices, rather than resolving by last-write-wins. `core` participates in that rule
and must contain no platform binaries.

Limits: `--max-download-size` applies per slice; `--max-extract-size` applies to the merged total.

### `internal/manifest` (changed)

- `Manifest` gains a `Project` table with `Platforms`.
- `AddonSpec` gains `Platforms []string` and `Index string`; `Hash()` covers both, with `Platforms`
  sorted, matching the existing treatment of `Exclude`.
- `LockEntry` gains `Slices map[string]string` and `IndexChecksum string`.
- A `State` type mirrors `Lockfile`, including the temp-file-plus-rename save.

### `internal/project` (changed)

`Project` gains `StatePath` (`<Root>/.gpm-state.toml`).

### `internal/cli` (changed)

`Runner` reconciles needed slices against `.gpm-state.toml`, passes the effective platform set into
the fetcher, writes state after each addon alongside the lock, and reports newly materialized slices
through `verbosef`. `gpm init` adds `.gpm-state.toml` to `.gitignore`. A new `gpm package` command.

## Errors

Following the existing exit-code taxonomy, with every failure wrapped so `output.CodeFor` unwraps it:

| Condition | Error type | Exit |
| --- | --- | --- |
| Unknown platform tag in `addons.toml` | `ManifestError` | 3 |
| Malformed or unreadable `gpm-package.toml` | `ManifestError` | 3 |
| Unrecognized platform key in a packaged `.gdextension` | `ManifestError` | 3 |
| Index missing, unparseable, or `format` newer than supported | `FetchError` | 4 |
| Declared platform not published by the addon | `FetchError` | 4 |
| Slice or index checksum mismatch | `FetchError` | 4 |
| Cross-slice path collision | `FetchError` | 4 |
| Extraction or filesystem failure | `InstallError` | 5 |

Malformed is never treated as absent: an index that exists but fails to parse is an error, not a
fallback to the unsliced path.

## JSON output

Every command keeps `--json`. `AddonResult` and `gpm list` gain the installed slice list.
`gpm package` gets its own payload: slices with file name, size, and checksum.

## Testing

The backbone is a publish-to-consume round trip with no network and no hand-maintained fixtures:
`internal/packager` writes real slice archives into `t.TempDir()`, an `httptest` server serves them
as a fake GitHub release, and the fetcher consumes them. This follows the existing convention that
`internal/source` tests are `httptest`-backed and that seams are package-level vars.

- `internal/slice` — tag parse and reject tables, host candidate chains, `SelectSlices` including
  the unpublished-declared-platform error, and `.gdextension` partition ↔ reassembly round trips
  including multi-extension addons and a platform-tagged `[dependencies]` section.
- `internal/packager` — partition from a synthetic addon tree, extras globs, collision detection,
  index contents and checksums.
- `internal/source` — index discovery for both source types, per-slice download and verification,
  collision failure, `format` rejection, per-slice and aggregate limits.
- `internal/cli` — an end-to-end `install` asserting an iOS-only project ends with no Windows binary
  on disk and a `.gdextension` listing exactly what is present; a second install on a different
  simulated host materializing the host slice from an unchanged lock; `--all-platforms`.

## Out of scope

- `gpm publish` (uploading to a GitHub release). Plausible later, once the format has users; it is a
  separate product with its own auth, idempotency, and partial-upload failure modes.
- Deriving the platform list from `export_presets.cfg` at install time. The presets are the natural
  source of truth but are commonly gitignored, so CI would not have them. A later
  `gpm platforms --from-presets` may *write* the list into `addons.toml`.
- Target-level slices (`ios.arm64.template_release`). Reserved for a future `format = 2`.
- Slicing addons whose authors have not adopted the format. Consumer-side pruning of a fat archive
  was considered and rejected: it saves disk rather than bandwidth and relies on filename
  heuristics.

## Implementation sequence

1. `internal/slice` — tags, host resolution, selection.
2. `internal/slice` — the `gpm-index.toml` loader and writer.
3. `internal/slice` — `.gdextension` partition and reassembly.
4. `internal/packager` and `gpm package`.
5. `internal/manifest` — `[project] platforms`, per-addon override, `Hash()`, validation.
6. `internal/manifest` and `internal/project` — lock slice fields, `State`, `StatePath`.
7. `internal/source` — sliced fetch and merge.
8. `internal/cli` — `Runner` integration, state reconciliation, `--all-platforms`, `list`, `init`.
9. Documentation.

Packager before consumer is deliberate: it generates the fixtures the consumer tests need, so the
dependency runs that direction anyway.
