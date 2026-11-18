---
title: Troubleshooting
description: Common gpm setup and install failures.
---

# Troubleshooting

## No Godot Project Was Found

Most commands discover the project by walking upward from the current directory
until they find `project.godot`.

Run from inside your Godot project:

```bash
cd path/to/game
gpm install
```

Or pass the start directory explicitly:

```bash
gpm install --dir path/to/game
```

`gpm init` creates `addons.toml` in the current directory unless `--dir` is
provided.

## addons.toml Is Missing

Create a starter manifest:

```bash
gpm init
```

Then add addons with `gpm add` or edit `addons.toml` by hand.

## Multiple Addon Directories Were Detected

When `source_path` is omitted, `gpm` can auto-detect a single
`addons/<name>/` directory. If a source contains multiple addon directories,
set `source_path`:

```toml
[addons.dialogue_manager]
source = "git"
url = "https://github.com/nathanhoad/godot_dialogue_manager.git"
version = "v2.1.0"
source_path = "addons/dialogue_manager"
```

## GitHub Release Returns HTTP 404

GitHub returns 404 for private repositories that the request cannot see. For
private release assets, set a token:

```bash
export GITHUB_TOKEN=$(gh auth token)
gpm install
```

Check that the token can read the repository contents.

## No Release Asset Matched

For `github-release` sources, `asset` is matched against release asset names.
If the release has more than one asset, set an exact name or a narrower glob:

```toml
asset = "my-addon-v1.4.0.zip"
```

## Checksum Mismatch

For archive and GitHub release sources, a checksum mismatch means the bytes
downloaded by `gpm` do not match the expected SHA-256. Check whether the
upstream asset was republished, the URL changed, or the manifest checksum was
copied incorrectly.

Use `gpm update <name>` only after you have confirmed the new asset is the one
you want.

## Unknown Addon

Commands that target a specific addon require the addon name from the
`addons.toml` table key:

```toml
[addons.dialogue_manager]
```

The command name is therefore:

```bash
gpm update dialogue_manager
gpm remove dialogue_manager
```

## No Slice Is Published For A Declared Platform

The project declares a platform in `addons.toml` that the addon does not publish
a [slice](slices.md) for:

```text
gpm: the addon publishes no slice for declared platform "web.wasm32"; published slices are core, android.arm64, ios.arm64, linux.x86_64, macos, windows.x86_64
```

Exit code 4. The message lists the slices the addon does publish; remove the
platform from `[project] platforms` or declare a per-addon `platforms` list for
the addon that does not support it. The failure is identical under `--host-only`
and `--all-platforms`, because a selection mode changes what is installed and not
whether the manifest is valid.

An invalid platform tag, or a manifest declaring `core`, is a different failure
and is exit code 3:

```text
gpm: [project]: invalid platforms entry: invalid platform tag "MacOS": unknown platform "MacOS"; known platforms are android, ios, linux, macos, web, windows
gpm: [project]: invalid platforms entry: platform "core" is implicit and may not be declared; every project receives the core slice
```

## A Shared Artifact Is Missing Or Does Not Match The Index

Every format-2 shared archive must be published beside `gpm-index.toml` and the
slice archives. A release that omits one reports:

```text
gpm: artifact "android" names archive "sentry-2.3.0-shared-android.zip", which the addon's publisher does not offer
```

A failed download is reported as `downloading artifact "android": ...`. Bytes
that disagree with the index fail before extraction:

```text
gpm: artifact "android": checksum mismatch (index: 0000000a..., downloaded: 8ffda2f9...)
gpm: artifact "android": size mismatch (index: 76001 bytes, downloaded: 76000 bytes)
```

All are exit code 4 and install nothing. The publisher must upload the named
`<name>-<version>-shared-<platform>.zip` from the same packaging run. A shared
artifact is also checked separately against `--max-download-size`, just like
each slice archive; raise that limit only after confirming the expected size.

## A Slice Or Shared-Artifact Checksum Does Not Match

A slice archive's bytes do not match the SHA-256 that `addons.lock` pins for it:

```text
gpm: addon "limboai": slice "macos" checksum mismatch (lock: 0000000a..., fetched: 2a3441a0...)
gpm: addon "sentry": artifact "android" checksum mismatch (lock: 0000000b..., fetched: 8ffda2f9...)
```

Exit code 4, and nothing is installed. Either the publisher republished the
release under the same tag, or the lock entry was edited. Confirm the new
artifacts are the ones you want, then run `gpm update <name>` to re-resolve the
pins and review the `addons.lock` diff.

An `index_sha256` mismatch, a published slice or artifact set that gained or lost
an entry, and a release that stopped publishing a `gpm-index.toml` altogether all
report in the same way and have the same remedy. Artifact-set drift is reported
as `the fetch publishes artifacts the lock does not pin` or `the fetch no longer
publishes artifacts the lock pins`.

## Two Slices Ship The Same Path

Two of an addon's slice archives both carry one path, so the merged tree would
have to take it from both:

```text
gpm: slices "core" and "linux" both ship "shared.txt"; one path in the merged tree cannot come from two slices
gpm: artifact "android" and slice "android.arm64" both ship "plugin.aar"; one path in the merged tree cannot come from two archives
```

Exit code 4. This is a packaging mistake on the publisher's side and cannot be
worked around in the project; report it to the addon's author. The comparison is
case-insensitive, so two differently-cased paths are also reported — on a
case-insensitive filesystem they are one file. Format 1 keeps its narrow legacy
exception for identical fan-out files in two architecture slices of one
platform. Format 2 never allows that duplication: shared bytes belong in the
platform's artifact archive.

## The Index Format Is Newer Than This gpm

```text
gpm: unsupported index format 3: this gpm understands index format 2 at most, so upgrade gpm to install this addon
```

Exit code 4. The index is rejected before any other key is read, because a newer
format may give an existing key a new meaning. Upgrade `gpm`, or pin the addon to
a version whose index this `gpm` understands.

A gpm release that understands only format 1 reports the corresponding message
for a format-2 package with shared artifacts; it does not ignore the dependency.

## An Addon Does Not Load In The Editor

The editor reports that a GDExtension library is missing on this machine, and the
installed `.gdextension` lists no entry for the host's platform. The addon
publishes no [slice](slices.md) for this host, which is the author's statement
about what the addon supports rather than a project error. `gpm install
--verbose` says so:

```text
addon "b" publishes no slice for this host (darwin/arm64); published slices are core, linux
```

The addon's GDScript still installs, and exit code 0 is correct. A pure-GDScript
addon publishes `core` alone, so it reports the same note on every host and
needs nothing else — in that case the diagnostic carries no action.

## A Platform's Binaries Are Missing From A Checkout

An export, or a build for a platform other than the host's, cannot find a library
that `addons.lock` pins. The checkout was most likely populated with
[`--host-only`](slices.md#selection-modes) or with `GPM_HOST_ONLY` set, which
installs `core` and the host's slice only.

Check what is actually on disk and whether the variable is set:

```bash
gpm list
echo "$GPM_HOST_ONLY"
```

`gpm list` prints the installed slice IDs after each addon's version. A host-only
checkout shows `core` and one platform. The repository is not broken:
`addons.lock` is byte-identical under host-only, so nothing was committed
differently. Restore the declared platforms with a plain install:

```bash
unset GPM_HOST_ONLY
gpm install
```

Nothing has to be deleted first; `gpm install` recomputes the needed slice set
every run and re-materializes the addon.

A `GPM_HOST_ONLY` value that is not a boolean is a usage error, exit code 2:

```text
gpm: GPM_HOST_ONLY="yes" is not a boolean; use one of true, false, 1 or 0
```

So is passing both selection flags:

```text
gpm: --all-platforms and --host-only contradict each other; pass whichever one of the two sets you want installed
```

## .gpm-state.toml Could Not Be Parsed

```text
ignoring unparseable state file /path/to/game/.gpm-state.toml: toml: line 1: expected '.' or '=', but got 't' instead
```

The file is reported and treated as empty, and the run continues: every addon is
then re-materialized, which costs a download and nothing else. It is never fatal
and never produces an unverified install, because `addons.lock` is the only
verification authority. The message is printed through diagnostics, so pass
`--verbose` to see it.

A state file that exists but cannot be *read* is different — an environment fault
rather than a cache miss — and fails with exit code 3.

## A File Deleted From Inside An Addon Is Not Restored

`gpm install` reconciles whether an addon's install directory exists and which
slices this machine materialized. It does not compare the directory's contents
against the archives, so a single file deleted from inside an otherwise-installed
addon is not noticed and not restored:

```bash
rm addons/limboai/plugin.gd
gpm install        # succeeds, and plugin.gd is still missing
```

Remove the whole addon directory and install again:

```bash
rm -rf addons/limboai
gpm install
```

Changing the selection mode *is* reconciled, in both directions: widening to
`--all-platforms` fetches the newly needed slices, and narrowing to `--host-only`
re-materializes the addon without the others. Only a change inside an addon
directory that `gpm` still considers complete goes unnoticed.

## Git Is Not Found

Git sources require `git` on `PATH`. Verify:

```bash
git --version
```

Install Git with your OS package manager, then retry `gpm install`.
