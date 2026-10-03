---
title: Platform slices
description: Publish and consume addons split into per-platform archives.
---

# Platform Slices

A GDExtension addon ships a library for every platform it supports, and a
project usually needs very few of them. **Platform slices** split a published
addon into one platform-independent archive plus one archive per platform, so a
project downloads only the platforms it targets and the machine it is being
installed on.

There are two halves to the feature:

- An addon author runs `gpm package` to turn one addon directory into slice
  archives plus a `gpm-index.toml` that describes them, and publishes both.
- A project declares the platforms it targets in `addons.toml`. `gpm install`
  reads the index, downloads only the needed slices, merges them into
  `addons/<name>/`, and rewrites the installed `.gdextension` so it lists
  exactly the binaries that are on disk.

Nothing about an unsliced addon changes. An addon is sliced only when its
publisher offers an index, and `git` sources are never sliced: slices are a
property of published release artifacts, not of a source checkout.

## What A Slice Is

A slice is one archive carrying the part of an addon that belongs to one Godot
platform. Every published addon has a `core` slice, which carries everything
that is not platform-specific — scripts, scenes, resources, and the
`.gdextension` body with its platform-tagged entries removed.

Each platform slice carries that platform's library binaries, the files shipped
beside them, and the `.gdextension` entries that name them. In index format 1,
installing one platform slice beside `core` is complete. Format 2 may additionally
name non-selectable shared artifacts; there, a slice is independently installable
as the closure of `core`, that slice, and its automatically downloaded artifacts.

## Slice IDs

A slice ID is a Godot platform tag of **at most two components**: a platform,
and optionally an architecture, joined with `.`.

```text
core
linux
linux.x86_64
macos.universal
ios.arm64
```

Known platforms:

| Platform |
| --- |
| `android` |
| `ios` |
| `linux` |
| `macos` |
| `web` |
| `windows` |

Known architectures:

| Architecture |
| --- |
| `arm32` |
| `arm64` |
| `rv64` |
| `universal` |
| `wasm32` |
| `x86_32` |
| `x86_64` |

Tags are matched exactly. A tag that is not lowercase is **rejected rather than
normalized**, because Godot tags are lowercase and silently correcting one would
hide a typo instead of reporting it. A tag may not be empty, may not contain
whitespace, and may not have an empty component, which is what a leading,
trailing, or doubled `.` produces.

### Only The Architecture Survives Into A Slice ID

A Godot `.gdextension` key carries more axes than a slice ID does. Besides the
platform and the architecture, a key may name:

| Axis | Values | Example key |
| --- | --- | --- |
| Build target | `debug`, `editor`, `release`, `template_debug`, `template_release` | `macos.template_release` |
| Float precision | `single`, `double` | `windows.x86_64.double.release` |
| Platform variant | `simulator`, `threads` | `ios.simulator.release`, `web.debug.threads.wasm32` |

A slice carries *every* value of each of those axes for its platform, so when
`gpm` reduces a library key to the slice that owns it, all three are dropped and
only the architecture survives. `macos.debug` and `macos.template_release` both
belong to `macos`; `windows.x86_64.single.debug` and
`windows.x86_64.double.release` both belong to `windows.x86_64`; and
`ios.release` and `ios.simulator.release` both belong to `ios`; similarly,
`web.debug.wasm32` and `web.debug.threads.wasm32` both belong to `web.wasm32`.

The iOS simulator is the one worth spelling out. Godot writes it as a variant of
the `ios` platform rather than as an architecture, and the device and simulator
libraries land in the **same** slice deliberately: a project targeting iOS needs
both — the simulator to run the game on a development Mac, the device library to
export — and splitting them would let a project install one and silently lack
the other.

Float precision appears in every library key of an addon built with godot-cpp's
SCons setup, which emits `<platform>.<architecture>.<precision>.<build target>`.

Web thread support is also a platform variant. Threaded and non-threaded web
libraries stay together because a project targeting `web.wasm32` may need
either build, and both entries belong in that one slice.

A component that belongs to none of those axes is rejected rather than dropped,
because silently ignoring it would file the binary under the wrong slice and
turn a packaging mistake into an export failure. So is a key that names one axis
twice, such as `macos.single.double.debug`.

### The core Slice Is Asymmetric

`core` is mandatory in a published index and may **not** be declared in a
manifest:

- An index that publishes no `core` slice is rejected, because every project
  needs it.
- `platforms = ["core"]` in `addons.toml` is a manifest error, because every
  project receives `core` implicitly and declaring it adds nothing.

```text
gpm: [project]: invalid platforms entry: platform "core" is implicit and may not
be declared; every project receives the core slice
```

That failure is exit code 3.

## Which Slices A Project Installs

By default, a project installs:

1. `core`, always.
2. Every platform the project declares in `addons.toml`.
3. **The host's own slice**, implicitly.

The host slice is implicit so that a manifest written on one machine does not
break a teammate on another. A project that only exports to iOS still needs a
macOS library to open the addon in the editor on a Mac and a Windows library to
open it on Windows — those are editor-time needs that the project's export
targets say nothing about.

### Host Candidate Chains

A Godot tag carries an architecture only sometimes, and macOS libraries are
usually universal, so each host has a chain of candidate slice IDs. **The first
one the addon publishes wins**, and the rest are ignored:

| Host | Candidates, most specific first |
| --- | --- |
| macOS on Apple silicon | `macos.arm64`, `macos.universal`, `macos` |
| macOS on Intel | `macos.x86_64`, `macos.universal`, `macos` |
| Linux x86-64 | `linux.x86_64`, `linux` |
| Linux arm64 | `linux.arm64`, `linux` |
| Windows x86-64 | `windows.x86_64`, `windows` |
| Windows arm64 | `windows.arm64`, `windows` |

Both macOS chains end at `macos` by way of `macos.universal`. The chain is
fixed per host, so repeated runs on one machine select the same slice.

A host that is not in the table has no chain, and so does an addon that
publishes nothing in the host's chain.

### An Unpublished Host Is A Diagnostic, Not A Failure

An addon that publishes no slice for the host is the author's statement about
what the addon supports, not a project misconfiguration. `gpm install` reports
it and continues:

```text
$ gpm install --verbose
addon "b" publishes no slice for this host (darwin/arm64); published slices are core, linux
```

A pure-GDScript addon publishes `core` alone, so it reports the same diagnostic
on every host. The addon installs and the command succeeds; the note is
informational, and it is only printed with `--verbose`.

### A Declared Platform Is Validated In Every Mode

A platform the project declares but the addon does not publish is a manifest
mistake, and it fails however little of the addon this machine wants:

```text
$ gpm install
gpm: the addon publishes no slice for declared platform "web.wasm32"; published slices are core, android.arm64, ios.arm64, linux.x86_64, macos, windows.x86_64
```

That failure is exit code 4, and it is identical under `--host-only` and
`--all-platforms`: a selection mode changes what is installed, never whether the
manifest is valid.

## Declaring Platforms

The project-wide list lives in the `[project]` table of `addons.toml`:

```toml
[project]
platforms = ["windows.x86_64", "linux.x86_64"]
```

An addon may declare its own list, which **replaces** the project list for that
addon rather than merging with it. See
[Manifest](manifest.md#platform-declarations) for the field reference.

For the `[project]` table, an absent `platforms` key and `platforms = []` are
equivalent: both mean the project declares nothing beyond `core` and the host
slice. On an addon the two differ, because there is a list to inherit: an absent
key takes the project's list, while `platforms = []` declares nothing for that
addon.

## Selection Modes

Three modes decide how wide a set `gpm install` and `gpm update` materialize.
All three validate the declared platforms, and all three write a byte-identical
`addons.lock`.

| Mode | Installs |
| --- | --- |
| default | `core`, every declared platform, and the host's slice |
| `--all-platforms` | every slice the addon publishes |
| `--host-only` | `core` and the host's slice only |

Switching modes is self-healing in both directions. `gpm install` recomputes the
needed set on every run and compares it for equality rather than coverage, so
widening re-fetches the newly needed platforms and narrowing re-materializes the
addon without them. Nothing has to be deleted first.

### --all-platforms

Use `--all-platforms` when the project vendors `addons/` into git and every
contributor's checkout has to carry every platform's binaries regardless of the
machine it was populated on.

```bash
gpm install --all-platforms
gpm update --all-platforms
```

### --host-only

`--host-only` installs `core` and the host's own slice, **ignoring the
project's declared platforms**:

```bash
gpm install --host-only
gpm update --host-only
```

It is a **development convenience for a local checkout** — a git worktree that
only ever runs the host's editor, for instance — and not a shipping or CI mode.
A checkout populated with `--host-only` cannot export to the project's other
platforms, because their libraries were never downloaded.

What host-only does *not* change is the point:

- `addons.lock` is **byte-identical** to a normal install. The lock records the
  slice set the addon *publishes*, not the set this machine took, so host-only
  never shows up in a diff and is safe to use in a shared repository.
- `.gpm-state.toml` is the only file that differs, and it is machine-local and
  gitignored.
- A plain `gpm install` afterwards re-materializes the declared platforms. There
  is nothing to delete and nothing to reset.

An addon that publishes no slice for the host installs `core` alone under
`--host-only` and the command still succeeds:

```text
$ gpm install --host-only
installed b @ 1.0
1 addon(s) installed
$ gpm list
[x] b                    archive          1.0  core
```

### GPM_HOST_ONLY

`GPM_HOST_ONLY` selects host-only mode without passing the flag, which is what a
worktree bootstrap script or a per-checkout shell profile needs:

```bash
export GPM_HOST_ONLY=true
gpm install
```

The value is parsed as a boolean. The accepted values are exactly `1`, `t`, `T`,
`TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false`, and `False`. An unset or
blank variable means the variable is absent.

Any other value is a usage error, exit code 2. `yes` and `on` are **not**
accepted:

```text
$ GPM_HOST_ONLY=yes gpm install
gpm: GPM_HOST_ONLY="yes" is not a boolean; use one of true, false, 1 or 0
```

A malformed value is refused rather than read as false, because reading
`GPM_HOST_ONLY=yes` as "off" would hand an unattended bootstrap a full
multi-platform download with no signal that the setting was misspelled.

An explicit flag always wins over the environment, and `--verbose` says so:

```text
$ GPM_HOST_ONLY=true gpm install --verbose --all-platforms
ignoring GPM_HOST_ONLY=true: the command line is explicit
```

`--host-only=false` is an explicit statement that overrides the environment; an
absent flag is not.

Passing both selection flags is a usage error, exit code 2:

```text
$ gpm install --host-only --all-platforms
gpm: --all-platforms and --host-only contradict each other; pass whichever one of the two sets you want installed
```

## What A Sliced Install Leaves On Disk

With `platforms = ["windows.x86_64", "linux.x86_64"]` on a macOS host, a default
install of a six-slice addon takes four slices:

```text
$ gpm list
[x] limboai              archive          1.4.0  core linux.x86_64 macos windows.x86_64
```

The installed `.gdextension` lists exactly those binaries:

```text
[configuration]
entry_symbol="limboai_init"
compatibility_minimum="4.3"

[libraries]
linux.template_release.x86_64="res://addons/limboai/bin/limboai_linux.so"
macos.template_release="res://addons/limboai/bin/limboai_macos.framework"
windows.template_release.x86_64="res://addons/limboai/bin/limboai_windows.dll"

[dependencies]
```

Reassembly is deterministic and idempotent: entries are emitted sorted by key
within their section, the sections keep the position the author gave them, and
the body is the config format's canonical spelling. Two machines installing the
same slice set write identical bytes, and a repeat install does not dirty the
working tree.

Installing the same addon under a different directory name with `install_as`
works: every entry value is re-rooted from the addon's published directory to
the installed one.

```text
[libraries]
macos.template_release="res://addons/limbo_ai/bin/limboai_macos.framework"
```

The slices this machine holds are recorded in `.gpm-state.toml`, which is
machine-local and gitignored. See
[Project layout](project-layout.md#machine-local-state) for its place in the
tree, and [Lockfile](lockfile.md#sliced-addons) for what `addons.lock` pins.

## Publishing A Sliced Addon

### gpm-package.toml

`gpm package` runs in the addon's own repository and reads `gpm-package.toml`
from it. The repository has no `project.godot`, so no project discovery applies.

```toml
[package]
name       = "limboai"
addon_path = "addons/limboai"
version    = "1.4.0"

[package.slices]
"android.arm64" = ["android/limboai.aar"]
```

| Key | Required | Meaning |
| --- | --- | --- |
| `name` | yes | The addon's directory name, which is also the index name and the archive name prefix. |
| `addon_path` | yes | The addon subtree inside the repository. It must be exactly `addons/<name>`. |
| `version` | yes | The version being cut. `--version` overrides it. |
| `[package.slices]` | no | Extra files, per slice ID, that no `.gdextension` key references. |

`addon_path` must be `addons/<name>` because a slice archive carries the addon
subtree unprefixed and a consumer extracts it into `addons/<name>`, while every
`res://` value in the author's `.gdextension` is written against where the
subtree sits in the author's own project. Those are the same directory only at
that path, and packaging from anywhere else would publish an index naming files
no project has.

`[package.slices]` exists because Android plugins ship `.aar` files and iOS ships
`.xcframework` bundles that no `[libraries]` key references. Keys are slice IDs,
so `core` may not be named — everything unclaimed falls into `core` already.
Values are globs matched against paths relative to `addon_path`; `**` matches
across directories. A slice declared with no patterns is an error.

A file belongs to one slice, so two keys may not claim the same path. The one
exception is a key that names a platform generically while the addon publishes
architecture slices for it — see
[Mixed Architecture Granularity](#mixed-architecture-granularity) below.

Decoding is strict in both directions: an unknown key anywhere in
`gpm-package.toml` is rejected, including one that differs only in case.

### What Partitioning Does To A .gdextension

`gpm package` splits each `.gdextension` into the body the `core` slice ships
and the per-slice entries the index publishes:

- Platform-tagged entries in `[libraries]` and `[dependencies]` are **removed**
  from the core body and recorded in the index under the slice each entry's key
  reduces to.
- Every other section, key, value, and comment is kept, in order —
  `[configuration]`, `[icons]`, anything the author invented.
- A UTF-8 BOM is stripped and CRLF line endings are normalized away. The emitted
  core body is the config format's canonical spelling, because it is a file
  `gpm` generates into an archive rather than an edit of the author's working
  file.

Godot accepts an entry path either as a `res://` path or as a path relative to
the `.gdextension` file's own location, and both are accepted here. A relative
value is resolved against that file's directory, and the resolved value must
then be a clean `res://` path naming a file inside the addon subtree.

A `..` component in a relative value is **rejected outright rather than
simplified away**: a normalized traversal would land inside or outside the addon
root depending only on how deep that root happens to be, so one authored value
would be accepted for one addon and refused for another. A `res://` path always
names the same file unambiguously and is accepted instead.

`res://` is the only form published. Whichever form the author wrote, the index
stores the resolved project-absolute path.

Ordinarily every `[libraries]` value must resolve to a file in the addon tree so
the slice cannot promise a binary its archive does not carry. Android AAR plugins
are the narrow exception: when `[configuration] android_aar_plugin = true`, an
`android.*` library may be absent because Godot resolves it from `jni/<abi>/`
inside the plugin `.aar` after export. `gpm package` records that entry in the
Android slice without claiming an on-disk `.so`; the `.aar` itself must be added
as a generic Android extra, for example:

```toml
[package.slices]
android = ["bin/android/*.aar"]
```

`gpm` does not inspect the `.aar` to prove it contains the named object—that is
the publisher's responsibility. It does require at least one `.aar` extra to
reach every Android slice that uses this exception, so omitting the plugin
payload still fails packaging. The normal file and containment checks apply to
non-Android libraries, `[dependencies]`, and Android libraries when the flag is
absent or false. An Android library that does exist in the addon tree is still
assigned to its Android slice normally.

### Mixed Architecture Granularity

One platform's entries can legitimately split across the platform's generic
slice and an architecture-specific slice of the same platform, because a Godot
tag carries an architecture only sometimes — an addon may write
`macos.editor` beside `macos.template_release.universal`.

Because a host installs only the *first* published match in its chain, a host
offered both `macos.universal` and `macos` would take `macos.universal` alone and
lose the editor library. `gpm package` resolves that on the producer side: every
architecture slice of such a platform receives that platform's generic entries
in addition to its own, and the platform's generic slice is then not published at
all. The shared files those entries name are stored once when at least two
architecture slices need them; each slice names that archive as an automatic
dependency and stays independently installable as a dependency closure.

A `[package.slices]` key that names such a platform generically fans out the same
way. This is how an addon ships a payload that is one file for every
architecture: the Android plugin `.aar`, which no `[libraries]` key references
and which is the same file for every ABI.

```toml
[package.slices]
android = ["bin/android/*.aar"]
```

On an addon whose `[libraries]` name `android.arm64`, `android.arm32`,
`android.x86_64` and `android.x86_32`, those files land once in a shared
`android` artifact. No generic `android` slice is published; all four architecture
slices reference the artifact automatically. Naming the architecture slices
individually instead would claim one file for four slices, which is refused;
naming just one would leave the other three ABIs with no plugin.

The fan-out only applies where the addon's own `.gdextension` names at least one
architecture for the platform. When it names none, nothing says which
architectures the platform's generic files are for, and inventing an answer would
drop every host that resolves to a different one, so the config is reported
instead:

```text
slice "android" would be published beside "android.arm64"; a host installs the
first slice of its platform it finds, so the files of "android" would never be
installed on a host that resolves to an architecture, and the extras naming
"android" must name those architecture slices instead
```

The shared artifact is downloaded and extracted once even when a project selects
several architectures of its platform. A fan-out with only one target needs no
shared archive and remains format 1. `gpm package` emits format 2 only when at
least two architecture slices actually share bytes, preserving compatibility for
ordinary packages.

### gpm package

```bash
gpm package
gpm package --dir path/to/addon-repo --out dist --version 1.4.1
```

```text
$ gpm package
Packaged limboai 1.4.0 into 6 slices
  core             limboai-1.4.0-core.zip  410 bytes
  android.arm64    limboai-1.4.0-android.arm64.zip  178 bytes
  ios.arm64        limboai-1.4.0-ios.arm64.zip  344 bytes
  linux.x86_64     limboai-1.4.0-linux.x86_64.zip  180 bytes
  macos            limboai-1.4.0-macos.zip  194 bytes
  windows.x86_64   limboai-1.4.0-windows.x86_64.zip  186 bytes
Index: /path/to/addon-repo/dist/gpm-index.toml
```

Slice archives are named `<name>-<version>-<slice>.zip`. A format-2 shared
artifact is named `<name>-<version>-shared-<platform>.zip` and appears after the
slice list in text output:

```text
  shared:android   limboai-1.4.0-shared-android.zip  76000 bytes
```

Both archive kinds are reproducible: entries are sorted, timestamps are fixed,
and file modes are normalized, so two runs over an unchanged tree write
identical bytes. `gpm package` performs no network access, only ever reads the
addon subtree, and writes nothing outside the output directory.

Publishing every slice archive, shared-artifact archive, and the index beside
one another remains the author's job:

```bash
gpm package --version 1.4.0
gh release create v1.4.0 --title "limboai 1.4.0"
gh release upload v1.4.0 dist/*
```

A GitHub release that carries a `gpm-index.toml` asset is discovered as sliced
automatically, with no manifest change on the consumer's side. For a bare archive
URL the consumer sets `index` explicitly; see
[Sources](sources.md#which-sources-can-be-sliced).

### gpm-index.toml

The index is the contract between the producer and the consumer. This is the
file the run above emitted:

```toml
format = 1
name = "limboai"
version = "1.4.0"

[slices]
  [slices."android.arm64"]
    file = "limboai-1.4.0-android.arm64.zip"
    sha256 = "6e211f210f816965760abe4efd1c80936b15c95acf3e8a771312aacb1f4ed400"
    size = 178
  [slices.core]
    file = "limboai-1.4.0-core.zip"
    sha256 = "e8de5ee7b224e7aa447885ec3c248f04151e111f5bd9f78bdbfc85aa25fbb8c4"
    size = 410
  [slices."ios.arm64"]
    file = "limboai-1.4.0-ios.arm64.zip"
    sha256 = "6dff4908a2f07678af6f24f5719b58f4ede151a5ca0054bce98b8ac5ff21bfcd"
    size = 344
    [slices."ios.arm64".libraries]
      [slices."ios.arm64".libraries."limboai.gdextension"]
        "ios.template_release.arm64" = "res://addons/limboai/bin/limboai_ios.dylib"
    [slices."ios.arm64".dependencies]
      [slices."ios.arm64".dependencies."limboai.gdextension"]
        [slices."ios.arm64".dependencies."limboai.gdextension"."ios.template_release.arm64"]
          "res://addons/limboai/bin/libgodot-cpp_ios.a" = ""
  [slices.macos]
    file = "limboai-1.4.0-macos.zip"
    sha256 = "2a3441a0f24f45b89d7ee14a7b4ddf4458bbbdf715e652155f8b6695b1d70514"
    size = 194
    [slices.macos.libraries]
      [slices.macos.libraries."limboai.gdextension"]
        "macos.template_release" = "res://addons/limboai/bin/limboai_macos.framework"
```

Top-level keys:

| Key | Meaning |
| --- | --- |
| `format` | Index format version. Mandatory and not defaulted. |
| `name` | The addon's published directory name. |
| `version` | The version these slices were cut from. |
| `[artifacts]` | Format 2 only. Non-selectable shared archives, keyed by generic platform name. |
| `[slices]` | One table per published slice, keyed by slice ID. At least `core`. |

Each slice table:

| Key | Meaning |
| --- | --- |
| `file` | The archive's bare asset name. No path separators, no colon. |
| `sha256` | The archive's SHA-256 as 64 lowercase hex digits. |
| `size` | The archive's size in bytes, a positive integer. |
| `artifacts` | Format 2 only. Sorted shared-artifact IDs downloaded automatically with this slice. |
| `[libraries]` | Partitioned `[libraries]` entries, by `.gdextension` path then platform tag. |
| `[dependencies]` | Partitioned `[dependencies]` entries, by `.gdextension` path then platform tag. |

A minimal format-2 fragment looks like this:

```toml
format = 2
name = "sentry"
version = "2.3.0"

[artifacts.android]
file = "sentry-2.3.0-shared-android.zip"
sha256 = "0000000000000000000000000000000000000000000000000000000000000000"
size = 76000

[slices."android.arm64"]
file = "sentry-2.3.0-android.arm64.zip"
sha256 = "1111111111111111111111111111111111111111111111111111111111111111"
size = 42000
artifacts = ["android"]
```

Every other architecture slice sharing those bytes repeats
`artifacts = ["android"]`; it does not repeat the bytes.

`[libraries]` and `[dependencies]` are the only partitioned sections in formats
1 and 2, and they are the only two that differ in their leaf:

- A `[libraries]` entry is **one quoted `res://` path**, naming that platform's
  library binary.
- A `[dependencies]` entry is a **nested Godot Dictionary**: each dependency's
  `res://` path mapped to the export subdirectory Godot copies it into. An empty
  destination means the dependency is copied beside the exported binary, which is
  what every published addon writes today.

An entry written in the other section's TOML type is rejected, so a
`[dependencies]` entry written as a bare string is reported as a shape mistake
rather than as an empty table.

The `core` slice may not declare either section: platform-tagged entries belong
to platform slices, and `core` carries none. A section that is declared but empty
is also rejected — an absent section is omitted instead.

An artifact table has the same `file`, `sha256`, and `size` fields as a slice,
but no platform entries. Its key is a generic platform name such as `android`.
It must be referenced by at least two architecture slices of that same platform,
and every published architecture slice of that platform must list it in its
`artifacts` array. Artifacts cannot be declared in `addons.toml` or selected on
their own.

A malformed dependency graph is rejected while the index loads, with exit code
4. For example:

```text
gpm: index format 2 declares no [artifacts]; format 2 is used only when slices share an archive
gpm: index artifact "android" is shared by its platform, but architecture slice "android.x86_64" does not reference it
```

Artifact dependency lists must also be unique and sorted, must name a published
artifact, and may appear only on architecture slices of that artifact's
platform. These are publisher errors; the project cannot repair them locally.

An archive is resolved by joining `file` to the directory the index was
downloaded from, so the index and its archives are published side by side. Two
slices or artifacts may not declare the same `file`.

#### The format Rule

`format` is read and validated **before any other key is interpreted**. Format 1
has only slices. Format 2 adds `[artifacts]` and per-slice `artifacts` dependency
lists and requires at least one artifact. Format 1 rejects both of those
format-2 keys. An index declaring a `format` higher than this `gpm` supports is
rejected outright rather than interpreted in part, because a future format may
give an existing key a new meaning:

```text
$ gpm install
gpm: unsupported index format 3: this gpm understands index format 2 at most, so upgrade gpm to install this addon
```

That failure is exit code 4. A format-1 gpm likewise rejects a format-2 package
with the same upgrade diagnosis; it never silently ignores shared dependencies.
An index that declares no `format` at all is also
rejected: a producer that forgot it is a bug, and guessing format 1 would hide it.

#### Unknown Keys Are Rejected

Decoding an index is strict. An unknown or misspelled key anywhere in
`gpm-index.toml` is an error, including one that differs from a schema key only
in case. A key ignored today could carry meaning in a future format, and ignoring
it would misread the index rather than report it.

This is **not** how `addons.toml` is decoded. There, only the `[project]` table
rejects unknown keys; a stray key elsewhere in the manifest is silently ignored.

## Integrity

Every byte of a sliced addon is verified against a pin:

- The index itself is pinned by `index_sha256` in `addons.lock`, computed over
  the raw `gpm-index.toml` bytes before parsing, so a change that only alters
  how the index parses cannot evade it.
- Each slice and shared-artifact archive is pinned by the `sha256` and `size` the
  index declares and by its matching table in `addons.lock`.
- The lock's published sets are compared as sets, so a retagged release that
  adds or drops a slice or shared artifact fails rather than installing quietly.

A mismatch is exit code 4 and installs nothing:

```text
$ gpm install
gpm: addon "limboai": slice "macos" checksum mismatch (lock: 0000000af24f45b89d7ee14a7b4ddf4458bbbdf715e652155f8b6695b1d70514, fetched: 2a3441a0f24f45b89d7ee14a7b4ddf4458bbbdf715e652155f8b6695b1d70514)
```

`--max-download-size` continues to cap each downloaded archive individually,
including each shared artifact. `--max-extract-size` caps the combined merged
tree across core, selected slices, and their artifact closure.

The slice pins are verified independently of the selection mode: the whole
published set is checked even when only `core` and the host slice were
materialized, because the pin is over what the index publishes rather than over
what this disk took.

A sliced addon has no single archive, so `checksum` in `addons.toml` has nothing
to pin and is rejected for a sliced addon rather than left to go inert.

Two archives may not ship the same path into the merged tree. The comparison is
case-folded, because on a case-insensitive filesystem two differently-cased paths
are one file:

```text
$ gpm install
gpm: slices "core" and "linux" both ship "shared.txt"; one path in the merged tree cannot come from two slices
gpm: artifact "android" and slice "android.arm64" both ship "plugin.aar"; one path in the merged tree cannot come from two archives
```

That failure is exit code 4.

An index entry value is checked for being a clean `res://` path when the index is
loaded, but `gpm-index.toml` declares no addon root, so whether the value points
inside the addon it belongs to is checked later, when the addon root is known —
immediately before the installed `.gdextension` is written. An entry naming a
file outside the addon is exit code 4 and nothing is installed.

## The Spec Hash And The Lockfile

The implicit host slice is excluded from the manifest spec hash, and nothing in
the hash reads the host. Together with the lock recording the **published** slice
set rather than the installed one, that means `addons.lock` is byte-identical:

- across machines, whatever each one's host slice resolves to;
- across all three selection modes, so a host-only install produces the same
  lock as `--all-platforms`.

An absent list and an empty one are encoded identically in the hash, so an addon
whose effective platform list is empty produces the same `spec_hash` whether that
came from an absent `[project] platforms` or from an explicit `platforms = []`.

`addons.lock` is the only verification authority. `.gpm-state.toml` answers only
"which slice IDs did this machine materialize"; it never answers "are these bytes
correct". Discarding it can cost a re-download but can never produce an
unverified install.
