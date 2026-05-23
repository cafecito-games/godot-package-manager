---
title: Installation
description: Install the gpm CLI.
---

# Installation

`gpm` is distributed as a single Go binary. It also shells out to the system
`git` binary for Git sources, so keep `git` on `PATH`.

## Homebrew

On macOS:

```bash
brew install --cask cafecito-games/tap/gpm
```

If a new release is not visible yet, refresh the tap:

```bash
brew untap cafecito-games/tap
brew tap cafecito-games/tap
brew install --cask cafecito-games/tap/gpm
```

Verify:

```bash
gpm --version
```

## Go Install

Requires Go 1.26.2 or newer:

```bash
go install github.com/cafecito-games/godot-package-manager/cmd/gpm@latest
```

Go installs the binary into `$GOPATH/bin`. Add that directory to `PATH` if
needed:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
which gpm
```

## Build From A Checkout

```bash
git clone https://github.com/cafecito-games/godot-package-manager
cd godot-package-manager
go build ./cmd/gpm
```

The local binary is written to the current directory as `gpm`.

## Required Tools By Source

| Workflow | Required tools |
| --- | --- |
| Git source | `gpm`, `git` |
| GitHub release source | `gpm`, network access to the GitHub API |
| Archive source | `gpm`, network access to the archive URL |
| Repository development | Go 1.26.2+, golangci-lint for local linting |
