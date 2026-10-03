---
title: Development
description: Build, test, and validate gpm locally.
---

# Development

Clone the repository:

```bash
git clone https://github.com/cafecito-games/godot-package-manager
cd godot-package-manager
```

## Build

```bash
go build ./cmd/gpm
```

## Test

Run the full Go test suite:

```bash
go test ./...
```

Run tests with the race detector, matching CI:

```bash
go test -race ./...
```

## Formatting And Static Checks

```bash
gofmt -w .
go vet ./...
golangci-lint run
```

CI also verifies that `go.mod` and `go.sum` are tidy:

```bash
go mod tidy
git diff --exit-code go.mod go.sum
```

## Documentation Site

The docs site lives in `website/`.

```bash
cd website
npm ci
npm run build
npm run start
```

`npm run build` is the command used by the GitHub Pages workflow.
