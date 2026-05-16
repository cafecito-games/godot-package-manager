# Godot Addon Manager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `gam`, a Go CLI that installs Godot addons declared in an `addons.toml` manifest into a project's `addons/` directory, with a committed lockfile for reproducible installs.

**Architecture:** A Cobra CLI orchestrates four internal layers: `manifest` (parse/validate `addons.toml` + `addons.lock`), `source` (a `Fetcher` interface with git/github-release/archive implementations that fetch into temp dirs), `installer` (copy the resolved subtree into `addons/<name>/`), and `project` (locate the Godot project root). Output is text or `--json`, with typed errors mapped to exit codes.

**Tech Stack:** Go 1.26, Cobra (CLI), Bubble Tea (TUI), `github.com/BurntSushi/toml` (manifest), testify (tests). Shells out to the system `git` binary; `net/http` for archives; GitHub REST API for releases.

---

## File Structure

```
go.mod
cmd/gam/main.go                       entry point
internal/cli/root.go                  root command, global flags
internal/cli/init.go                  `gam init`
internal/cli/add.go                   `gam add`
internal/cli/remove.go                `gam remove`
internal/cli/install.go               `gam install`
internal/cli/update.go                `gam update`
internal/cli/list.go                  `gam list`
internal/cli/run.go                   shared install orchestration
internal/manifest/spec.go             AddonSpec, Manifest, SourceType
internal/manifest/manifest.go         Load/Save addons.toml
internal/manifest/validate.go         Manifest.Validate
internal/manifest/lock.go             Lockfile, LockEntry, Load/Save, reconciliation
internal/project/project.go           Discover godot project root
internal/source/source.go             Fetcher interface, FetchResult, FetcherFor
internal/source/git.go                git fetcher
internal/source/archive.go            archive fetcher + shared extraction
internal/source/githubrelease.go      github release fetcher
internal/installer/installer.go       source_path resolution + copy
internal/output/output.go             exit codes, typed errors, text/JSON rendering
internal/tui/add.go                   Bubble Tea wizard for `add`
```

---

### Task 0: Project scaffold and Cobra root command

**Goal:** A buildable Go module with a Cobra root command and global flags.

**Files:**
- Create: `go.mod`
- Create: `cmd/gam/main.go`
- Create: `internal/cli/root.go`
- Create: `internal/output/output.go`
- Test: `internal/cli/root_test.go`

**Acceptance Criteria:**
- [ ] `go build ./...` succeeds.
- [ ] `gam --help` lists the command and global flags `--json`, `-v/--verbose`, `-q/--quiet`.
- [ ] Global flags are accessible to subcommands via a shared options struct.

**Verify:** `go build ./... && go run ./cmd/gam --help` → help text containing `--json`.

**Steps:**

- [ ] **Step 1: Initialize the module**

```bash
go mod init github.com/CafecitoGames/godot-addon-manager
go get github.com/spf13/cobra@latest github.com/stretchr/testify@latest
```
Edit `go.mod` so the `go` directive reads `go 1.26`.

- [ ] **Step 2: Create `internal/output/output.go`** with exit codes and typed errors:

```go
package output

import "errors"

// ExitCode is a process exit status with a defined meaning.
type ExitCode int

const (
	ExitOK       ExitCode = 0
	ExitGeneric  ExitCode = 1
	ExitUsage    ExitCode = 2
	ExitManifest ExitCode = 3
	ExitFetch    ExitCode = 4
	ExitInstall  ExitCode = 5
)

// ManifestError wraps a manifest or lockfile failure (exit code 3).
type ManifestError struct{ Err error }

func (e *ManifestError) Error() string { return e.Err.Error() }
func (e *ManifestError) Unwrap() error { return e.Err }

// FetchError wraps a network/auth/source-resolution failure (exit code 4).
type FetchError struct{ Err error }

func (e *FetchError) Error() string { return e.Err.Error() }
func (e *FetchError) Unwrap() error { return e.Err }

// InstallError wraps a filesystem/extraction failure (exit code 5).
type InstallError struct{ Err error }

func (e *InstallError) Error() string { return e.Err.Error() }
func (e *InstallError) Unwrap() error { return e.Err }

// CodeFor maps an error to the exit code the process should return.
func CodeFor(err error) ExitCode {
	if err == nil {
		return ExitOK
	}
	var me *ManifestError
	var fe *FetchError
	var ie *InstallError
	switch {
	case errors.As(err, &me):
		return ExitManifest
	case errors.As(err, &fe):
		return ExitFetch
	case errors.As(err, &ie):
		return ExitInstall
	default:
		return ExitGeneric
	}
}
```

- [ ] **Step 3: Write the failing test `internal/cli/root_test.go`**

```go
package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootHelpListsGlobalFlags(t *testing.T) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), "--json")
	require.Contains(t, out.String(), "--verbose")
}
```

- [ ] **Step 4: Run the test, expect FAIL** (`NewRootCommand` undefined).

Run: `go test ./internal/cli/ -run TestRootHelp -v`

- [ ] **Step 5: Create `internal/cli/root.go`**

```go
package cli

import "github.com/spf13/cobra"

// Options holds global flags shared by all subcommands.
type Options struct {
	JSON    bool
	Verbose bool
	Quiet   bool
}

// NewRootCommand builds the `gam` root command with global flags wired into a
// shared Options value that subcommands read.
func NewRootCommand() *cobra.Command {
	opts := &Options{}
	root := &cobra.Command{
		Use:           "gam",
		Short:         "Manage Godot project addons declared in addons.toml",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&opts.JSON, "json", false, "emit machine-readable JSON output")
	root.PersistentFlags().BoolVarP(&opts.Verbose, "verbose", "v", false, "enable verbose logging")
	root.PersistentFlags().BoolVarP(&opts.Quiet, "quiet", "q", false, "suppress non-error output")
	// Subcommands are registered here as later tasks add them, e.g.:
	//   root.AddCommand(newInitCommand(opts))
	return root
}
```

- [ ] **Step 6: Create `cmd/gam/main.go`**

```go
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/CafecitoGames/godot-addon-manager/internal/cli"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

func main() {
	if err := cli.NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "gam:", err)
		code := output.CodeFor(err)
		var ue *cli.UsageError
		if errors.As(err, &ue) {
			code = output.ExitUsage
		}
		os.Exit(int(code))
	}
}
```

- [ ] **Step 7: Add `UsageError` to `internal/cli/root.go`**

```go
// UsageError marks an error caused by bad flags or arguments (exit code 2).
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }
```

- [ ] **Step 8: Run the test, expect PASS.** Run: `go test ./internal/cli/ -run TestRootHelp -v`

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum cmd internal
git commit -m "feat: scaffold gam CLI with cobra root command"
```

---

### Task 1: Manifest spec types and addons.toml parsing

**Goal:** Parse and write `addons.toml` into typed `AddonSpec`/`Manifest` values.

**Files:**
- Create: `internal/manifest/spec.go`
- Create: `internal/manifest/manifest.go`
- Test: `internal/manifest/manifest_test.go`

**Acceptance Criteria:**
- [ ] `Load` parses a multi-addon `addons.toml`, populating `AddonSpec.Name` from the table key.
- [ ] `Save` writes a manifest that `Load` round-trips to an equal value.
- [ ] `InstallName()` returns `InstallAs` when set, else `Name`.
- [ ] `Hash()` is stable across runs and changes when any field changes.

**Verify:** `go test ./internal/manifest/ -run TestManifest -v` → PASS

**Steps:**

- [ ] **Step 1: Create `internal/manifest/spec.go`**

```go
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// SourceType identifies how an addon is obtained.
type SourceType string

const (
	SourceGit           SourceType = "git"
	SourceGitHubRelease SourceType = "github-release"
	SourceArchive       SourceType = "archive"
)

// AddonSpec is one addon entry declared in addons.toml.
type AddonSpec struct {
	// Name is the TOML table key. It is set during Load and is not a TOML field.
	Name string `toml:"-"`

	Source     SourceType `toml:"source"`
	URL        string     `toml:"url,omitempty"`
	Repo       string     `toml:"repo,omitempty"`
	Version    string     `toml:"version,omitempty"`
	Asset      string     `toml:"asset,omitempty"`
	SourcePath string     `toml:"source_path,omitempty"`
	InstallAs  string     `toml:"install_as,omitempty"`
}

// Manifest is the parsed contents of addons.toml.
type Manifest struct {
	Addons map[string]AddonSpec `toml:"addons"`
}

// InstallName returns the directory name under addons/ for this addon.
func (s AddonSpec) InstallName() string {
	if s.InstallAs != "" {
		return s.InstallAs
	}
	return s.Name
}

// Hash returns a stable hash of the spec's resolvable fields, used to detect
// drift between addons.toml and addons.lock.
func (s AddonSpec) Hash() string {
	repr := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		s.Source, s.URL, s.Repo, s.Version, s.Asset, s.SourcePath, s.InstallAs)
	sum := sha256.Sum256([]byte(repr))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 2: Write the failing test `internal/manifest/manifest_test.go`**

```go
package manifest

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManifestRoundTrip(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"dialogue": {Source: SourceGit, URL: "https://example.com/d.git", Version: "v1.0", SourcePath: "addons/dialogue"},
		"thing":    {Source: SourceArchive, URL: "https://example.com/t.zip"},
	}}
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, m.Save(path))

	loaded, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "dialogue", loaded.Addons["dialogue"].Name)
	require.Equal(t, SourceGit, loaded.Addons["dialogue"].Source)
	require.Equal(t, "v1.0", loaded.Addons["dialogue"].Version)
	require.Equal(t, m.Addons["dialogue"].Hash(), loaded.Addons["dialogue"].Hash())
}

func TestInstallNameDefaultsToKey(t *testing.T) {
	require.Equal(t, "foo", AddonSpec{Name: "foo"}.InstallName())
	require.Equal(t, "bar", AddonSpec{Name: "foo", InstallAs: "bar"}.InstallName())
}

func TestHashChangesWithFields(t *testing.T) {
	a := AddonSpec{Source: SourceGit, URL: "u", Version: "v1"}
	b := a
	b.Version = "v2"
	require.NotEqual(t, a.Hash(), b.Hash())
}
```

- [ ] **Step 3: Run the test, expect FAIL** (`Load`/`Save` undefined). Run: `go test ./internal/manifest/ -run TestManifest -v`

- [ ] **Step 4: Create `internal/manifest/manifest.go`**

```go
package manifest

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Load reads and parses addons.toml at path. Each AddonSpec.Name is set from
// its TOML table key.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading manifest %s: %w", path, err)
	}
	m := &Manifest{}
	if err := toml.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", path, err)
	}
	if m.Addons == nil {
		m.Addons = map[string]AddonSpec{}
	}
	for name, spec := range m.Addons {
		spec.Name = name
		m.Addons[name] = spec
	}
	return m, nil
}

// Save writes the manifest to path as TOML.
func (m *Manifest) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating manifest %s: %w", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(m); err != nil {
		return fmt.Errorf("encoding manifest %s: %w", path, err)
	}
	return nil
}
```

Run `go get github.com/BurntSushi/toml@latest` if not already present.

- [ ] **Step 5: Run the test, expect PASS.** Run: `go test ./internal/manifest/ -run TestManifest -v`

- [ ] **Step 6: Commit**

```bash
git add internal/manifest go.mod go.sum
git commit -m "feat: add addons.toml manifest parsing"
```

---

### Task 2: Manifest validation

**Goal:** Reject manifests with missing or inconsistent fields before any network access.

**Files:**
- Create: `internal/manifest/validate.go`
- Test: `internal/manifest/validate_test.go`

**Acceptance Criteria:**
- [ ] `Validate` rejects an unknown `source` value.
- [ ] `Validate` requires `url` + `version` for git, `repo` + `version` for github-release, `url` for archive.
- [ ] `Validate` returns an `*output.ManifestError`.
- [ ] A fully valid manifest passes.

**Verify:** `go test ./internal/manifest/ -run TestValidate -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/manifest/validate_test.go`**

```go
package manifest

import (
	"errors"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestValidateRejectsBadSource(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{"x": {Name: "x", Source: "ftp"}}}
	err := m.Validate()
	require.Error(t, err)
	var me *output.ManifestError
	require.True(t, errors.As(err, &me))
}

func TestValidateRequiresGitFields(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{"x": {Name: "x", Source: SourceGit, URL: "u"}}}
	require.Error(t, m.Validate()) // missing version
}

func TestValidateAcceptsValidManifest(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"g": {Name: "g", Source: SourceGit, URL: "u", Version: "v1"},
		"r": {Name: "r", Source: SourceGitHubRelease, Repo: "o/r", Version: "1.0"},
		"a": {Name: "a", Source: SourceArchive, URL: "u"},
	}}
	require.NoError(t, m.Validate())
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/manifest/ -run TestValidate -v`

- [ ] **Step 3: Create `internal/manifest/validate.go`**

```go
package manifest

import (
	"fmt"

	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

// Validate checks every addon entry for required and consistent fields.
// It returns an *output.ManifestError describing the first problem found.
func (m *Manifest) Validate() error {
	for name, s := range m.Addons {
		if err := validateSpec(name, s); err != nil {
			return &output.ManifestError{Err: err}
		}
	}
	return nil
}

func validateSpec(name string, s AddonSpec) error {
	switch s.Source {
	case SourceGit:
		if s.URL == "" || s.Version == "" {
			return fmt.Errorf("addon %q: git source requires url and version", name)
		}
	case SourceGitHubRelease:
		if s.Repo == "" || s.Version == "" {
			return fmt.Errorf("addon %q: github-release source requires repo and version", name)
		}
	case SourceArchive:
		if s.URL == "" {
			return fmt.Errorf("addon %q: archive source requires url", name)
		}
	default:
		return fmt.Errorf("addon %q: unknown source %q (want git, github-release, or archive)", name, s.Source)
	}
	return nil
}
```

- [ ] **Step 4: Run the test, expect PASS.** Run: `go test ./internal/manifest/ -run TestValidate -v`

- [ ] **Step 5: Commit**

```bash
git add internal/manifest
git commit -m "feat: add manifest validation"
```

---

### Task 3: Lockfile parsing and reconciliation

**Goal:** Load/save `addons.lock` and decide per addon whether the lock pin is reusable.

**Files:**
- Create: `internal/manifest/lock.go`
- Test: `internal/manifest/lock_test.go`

**Acceptance Criteria:**
- [ ] `LoadLock` returns an empty `Lockfile` (no error) when the file is absent.
- [ ] `Save`/`LoadLock` round-trip a multi-entry lockfile.
- [ ] `NeedsResolve` is true when no lock entry exists or the entry's `SpecHash` differs from the spec's `Hash()`.

**Verify:** `go test ./internal/manifest/ -run TestLock -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/manifest/lock_test.go`**

```go
package manifest

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLockLoadMissingIsEmpty(t *testing.T) {
	l, err := LoadLock(filepath.Join(t.TempDir(), "nope.lock"))
	require.NoError(t, err)
	require.Empty(t, l.Addons)
}

func TestLockRoundTrip(t *testing.T) {
	l := &Lockfile{Addons: map[string]LockEntry{
		"g": {ResolvedVersion: "abc123", SourcePath: "addons/g", SpecHash: "h1"},
	}}
	path := filepath.Join(t.TempDir(), "addons.lock")
	require.NoError(t, l.Save(path))
	got, err := LoadLock(path)
	require.NoError(t, err)
	require.Equal(t, "abc123", got.Addons["g"].ResolvedVersion)
}

func TestNeedsResolve(t *testing.T) {
	spec := AddonSpec{Name: "g", Source: SourceGit, URL: "u", Version: "v1"}
	empty := &Lockfile{Addons: map[string]LockEntry{}}
	require.True(t, NeedsResolve(spec, empty))

	matching := &Lockfile{Addons: map[string]LockEntry{"g": {SpecHash: spec.Hash()}}}
	require.False(t, NeedsResolve(spec, matching))

	stale := &Lockfile{Addons: map[string]LockEntry{"g": {SpecHash: "old"}}}
	require.True(t, NeedsResolve(spec, stale))
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/manifest/ -run TestLock -v`

- [ ] **Step 3: Create `internal/manifest/lock.go`**

```go
package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/BurntSushi/toml"
)

// LockEntry pins one resolved addon for reproducible installs.
type LockEntry struct {
	ResolvedVersion string `toml:"resolved_version"`        // commit SHA or release tag
	SourcePath      string `toml:"source_path"`             // subtree actually installed
	Checksum        string `toml:"checksum,omitempty"`      // SHA-256 for archive/release; empty for git
	SpecHash        string `toml:"spec_hash"`               // AddonSpec.Hash() it was resolved from
}

// Lockfile is the parsed contents of addons.lock.
type Lockfile struct {
	Addons map[string]LockEntry `toml:"addons"`
}

// LoadLock reads addons.lock at path. A missing file yields an empty Lockfile
// and no error.
func LoadLock(path string) (*Lockfile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Lockfile{Addons: map[string]LockEntry{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading lockfile %s: %w", path, err)
	}
	l := &Lockfile{}
	if err := toml.Unmarshal(data, l); err != nil {
		return nil, fmt.Errorf("parsing lockfile %s: %w", path, err)
	}
	if l.Addons == nil {
		l.Addons = map[string]LockEntry{}
	}
	return l, nil
}

// Save writes the lockfile to path as TOML.
func (l *Lockfile) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating lockfile %s: %w", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(l); err != nil {
		return fmt.Errorf("encoding lockfile %s: %w", path, err)
	}
	return nil
}

// NeedsResolve reports whether spec must be re-fetched rather than installed
// from its existing lock pin.
func NeedsResolve(spec AddonSpec, lock *Lockfile) bool {
	e, ok := lock.Addons[spec.Name]
	if !ok {
		return true
	}
	return e.SpecHash != spec.Hash()
}
```

- [ ] **Step 4: Run the test, expect PASS.** Run: `go test ./internal/manifest/ -run TestLock -v`

- [ ] **Step 5: Commit**

```bash
git add internal/manifest
git commit -m "feat: add addons.lock parsing and reconciliation"
```

---

### Task 4: Godot project discovery

**Goal:** Locate the Godot project root by walking up for `project.godot`.

**Files:**
- Create: `internal/project/project.go`
- Test: `internal/project/project_test.go`

**Acceptance Criteria:**
- [ ] `Discover` walks up from a start dir and returns the dir containing `project.godot`.
- [ ] `Project` exposes `Root`, `ManifestPath`, `LockPath`, `AddonsDir`.
- [ ] `Discover` returns an `*output.ManifestError` when no `project.godot` is found.

**Verify:** `go test ./internal/project/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/project/project_test.go`**

```go
package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoverWalksUp(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), nil, 0o644))
	nested := filepath.Join(root, "scenes", "ui")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	p, err := Discover(nested)
	require.NoError(t, err)
	require.Equal(t, root, p.Root)
	require.Equal(t, filepath.Join(root, "addons.toml"), p.ManifestPath)
	require.Equal(t, filepath.Join(root, "addons.lock"), p.LockPath)
	require.Equal(t, filepath.Join(root, "addons"), p.AddonsDir)
}

func TestDiscoverNotFound(t *testing.T) {
	_, err := Discover(t.TempDir())
	require.Error(t, err)
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/project/ -v`

- [ ] **Step 3: Create `internal/project/project.go`**

```go
package project

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

// Project describes a located Godot project and its gam-managed paths.
type Project struct {
	Root         string // directory containing project.godot
	ManifestPath string // <Root>/addons.toml
	LockPath     string // <Root>/addons.lock
	AddonsDir    string // <Root>/addons
}

// Discover walks up from startDir until it finds a directory containing
// project.godot. It returns an *output.ManifestError if none is found.
func Discover(startDir string) (*Project, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, &output.ManifestError{Err: err}
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "project.godot")); statErr == nil {
			return forRoot(dir), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, &output.ManifestError{
				Err: fmt.Errorf("no project.godot found in %s or any parent directory", startDir),
			}
		}
		dir = parent
	}
}

// forRoot builds a Project for a known project root directory.
func forRoot(root string) *Project {
	return &Project{
		Root:         root,
		ManifestPath: filepath.Join(root, "addons.toml"),
		LockPath:     filepath.Join(root, "addons.lock"),
		AddonsDir:    filepath.Join(root, "addons"),
	}
}
```

- [ ] **Step 4: Run the test, expect PASS.** Run: `go test ./internal/project/ -v`

- [ ] **Step 5: Commit**

```bash
git add internal/project
git commit -m "feat: add godot project discovery"
```

---

### Task 5: Source layer interface and dispatch

**Goal:** Define the `Fetcher` interface, `FetchResult`, and `FetcherFor` dispatch.

**Files:**
- Create: `internal/source/source.go`
- Test: `internal/source/source_test.go`

**Acceptance Criteria:**
- [ ] `Fetcher` interface and `FetchResult` struct are defined.
- [ ] `FetcherFor` returns the correct fetcher per `SourceType` and an `*output.FetchError` for unknown types.

**Verify:** `go test ./internal/source/ -run TestFetcherFor -v` → PASS

**Steps:**

- [ ] **Step 1: Create `internal/source/source.go`** (interface only; concrete fetchers added in Tasks 6-8)

```go
package source

import (
	"context"
	"fmt"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

// FetchResult is the outcome of fetching an addon source into a temp directory.
type FetchResult struct {
	Dir             string // local path to the fetched tree
	ResolvedVersion string // commit SHA (git) or release tag actually obtained
	Checksum        string // SHA-256 of the archive/asset; empty for git sources
}

// Fetcher retrieves an addon source into a local temporary directory.
// Callers are responsible for removing FetchResult.Dir when done.
type Fetcher interface {
	Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error)
}

// FetcherFor returns the Fetcher matching the spec's source type.
func FetcherFor(spec manifest.AddonSpec) (Fetcher, error) {
	switch spec.Source {
	case manifest.SourceGit:
		return &GitFetcher{}, nil
	case manifest.SourceArchive:
		return &ArchiveFetcher{}, nil
	case manifest.SourceGitHubRelease:
		return &GitHubReleaseFetcher{}, nil
	default:
		return nil, &output.FetchError{Err: fmt.Errorf("no fetcher for source %q", spec.Source)}
	}
}
```

- [ ] **Step 2: Write `internal/source/source_test.go`** — this test compiles only after Tasks 6-8 add the concrete types, so write it now but expect to run it at the end of Task 8:

```go
package source

import (
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/stretchr/testify/require"
)

func TestFetcherForKnownTypes(t *testing.T) {
	for _, st := range []manifest.SourceType{
		manifest.SourceGit, manifest.SourceArchive, manifest.SourceGitHubRelease,
	} {
		f, err := FetcherFor(manifest.AddonSpec{Source: st})
		require.NoError(t, err)
		require.NotNil(t, f)
	}
}

func TestFetcherForUnknownType(t *testing.T) {
	_, err := FetcherFor(manifest.AddonSpec{Source: "ftp"})
	require.Error(t, err)
}
```

- [ ] **Step 3: Temporarily stub the concrete types** at the bottom of `source.go` so the package compiles now; Tasks 6-8 will move each into its own file and replace the stub:

```go
// Stubs replaced by Tasks 6-8.
type GitFetcher struct{}
type ArchiveFetcher struct{}
type GitHubReleaseFetcher struct{}
```

- [ ] **Step 4: Run `go build ./internal/source/`**, expect success. Defer `go test` to Task 8.

- [ ] **Step 5: Commit**

```bash
git add internal/source
git commit -m "feat: add source Fetcher interface and dispatch"
```

---

### Task 6: Archive fetcher and shared extraction

**Goal:** Download a zip/tarball, checksum it, and extract it to a temp dir.

**Files:**
- Create: `internal/source/archive.go` (replaces the `ArchiveFetcher` stub from Task 5)
- Test: `internal/source/archive_test.go`

**Acceptance Criteria:**
- [ ] `ArchiveFetcher.Fetch` downloads a `.zip` URL, extracts it, and reports the SHA-256 in `FetchResult.Checksum`.
- [ ] The same logic handles `.tar.gz`.
- [ ] A non-200 HTTP response returns an `*output.FetchError`.
- [ ] `extractArchive` is exported within the package for reuse by the github-release fetcher.

**Verify:** `go test ./internal/source/ -run TestArchive -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/source/archive_test.go`**

```go
package source

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/stretchr/testify/require"
)

func zipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestArchiveFetchExtractsZip(t *testing.T) {
	payload := zipBytes(t, map[string]string{"addons/x/plugin.cfg": "[plugin]"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	f := &ArchiveFetcher{}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/x.zip",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.NotEmpty(t, res.Checksum)
	got, err := os.ReadFile(filepath.Join(res.Dir, "addons", "x", "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "[plugin]", string(got))
}

func TestArchiveFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	f := &ArchiveFetcher{}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceArchive, URL: srv.URL + "/missing.zip",
	})
	require.Error(t, err)
}
```

- [ ] **Step 2: Remove the `ArchiveFetcher` stub** from `source.go` (delete the `type ArchiveFetcher struct{}` line).

- [ ] **Step 3: Run the test, expect FAIL (build error).** Run: `go test ./internal/source/ -run TestArchive -v`

- [ ] **Step 4: Create `internal/source/archive.go`**

```go
package source

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

// ArchiveFetcher downloads and extracts a plain zip or tarball URL.
type ArchiveFetcher struct{}

// Fetch downloads spec.URL, extracts it into a new temp directory, and reports
// the archive's SHA-256.
func (f *ArchiveFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error) {
	data, err := download(ctx, spec.URL, nil)
	if err != nil {
		return FetchResult{}, err
	}
	dir, err := os.MkdirTemp("", "gam-archive-*")
	if err != nil {
		return FetchResult{}, &output.InstallError{Err: err}
	}
	if err := extractArchive(spec.URL, data, dir); err != nil {
		os.RemoveAll(dir)
		return FetchResult{}, err
	}
	sum := sha256.Sum256(data)
	return FetchResult{
		Dir:             dir,
		ResolvedVersion: spec.Version,
		Checksum:        hex.EncodeToString(sum[:]),
	}, nil
}

// download performs an HTTP GET and returns the body. header, if non-nil, is
// applied to the request (used for GitHub auth).
func download(ctx context.Context, url string, header http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, &output.FetchError{Err: err}
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, &output.FetchError{Err: fmt.Errorf("downloading %s: %w", url, err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &output.FetchError{Err: fmt.Errorf("downloading %s: HTTP %d", url, resp.StatusCode)}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &output.FetchError{Err: err}
	}
	return body, nil
}

// extractArchive extracts data into dir, choosing zip vs tar.gz from nameHint's
// extension.
func extractArchive(nameHint string, data []byte, dir string) error {
	switch {
	case strings.HasSuffix(nameHint, ".zip"):
		return extractZip(data, dir)
	case strings.HasSuffix(nameHint, ".tar.gz"), strings.HasSuffix(nameHint, ".tgz"):
		return extractTarGz(data, dir)
	default:
		return &output.FetchError{Err: fmt.Errorf("unsupported archive type: %s", nameHint)}
	}
}

func extractZip(data []byte, dir string) error {
	zr, err := zip.NewReader(strings.NewReader(string(data)), int64(len(data)))
	if err != nil {
		return &output.InstallError{Err: err}
	}
	for _, zf := range zr.File {
		dest, err := safeJoin(dir, zf.Name)
		if err != nil {
			return err
		}
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return &output.InstallError{Err: err}
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return &output.InstallError{Err: err}
		}
		rc, err := zf.Open()
		if err != nil {
			return &output.InstallError{Err: err}
		}
		err = writeFile(dest, rc, zf.Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(data []byte, dir string) error {
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return &output.InstallError{Err: err}
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return &output.InstallError{Err: err}
		}
		dest, err := safeJoin(dir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return &output.InstallError{Err: err}
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return &output.InstallError{Err: err}
			}
			if err := writeFile(dest, tr, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		}
	}
}

// safeJoin joins base and name, rejecting paths that escape base (zip-slip).
func safeJoin(base, name string) (string, error) {
	dest := filepath.Join(base, name)
	if !strings.HasPrefix(dest, filepath.Clean(base)+string(os.PathSeparator)) && dest != filepath.Clean(base) {
		return "", &output.InstallError{Err: fmt.Errorf("archive entry escapes target dir: %s", name)}
	}
	return dest, nil
}

func writeFile(dest string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o200)
	if err != nil {
		return &output.InstallError{Err: err}
	}
	defer out.Close()
	if _, err := io.Copy(out, r); err != nil {
		return &output.InstallError{Err: err}
	}
	return nil
}
```

- [ ] **Step 5: Run the test, expect PASS.** Run: `go test ./internal/source/ -run TestArchive -v`

- [ ] **Step 6: Commit**

```bash
git add internal/source
git commit -m "feat: add archive fetcher with zip/tar.gz extraction"
```

---

### Task 7: Git fetcher

**Goal:** Clone a git ref into a temp dir using the system `git` binary and report the resolved commit SHA.

**Files:**
- Create: `internal/source/git.go` (replaces the `GitFetcher` stub from Task 5)
- Test: `internal/source/git_test.go`

**Acceptance Criteria:**
- [ ] `GitFetcher.Fetch` clones a local on-disk repo at a given tag and reports the commit SHA in `FetchResult.ResolvedVersion`.
- [ ] `Fetch` returns an `*output.FetchError` when `git` is missing or the ref does not exist.
- [ ] The fetched tree contains the repo's files (no `.git` requirement; presence is acceptable).

**Verify:** `go test ./internal/source/ -run TestGit -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/source/git_test.go`** (builds a real local repo with `git`):

```go
package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/stretchr/testify/require"
)

func makeLocalRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.cfg"), []byte("[plugin]"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	run("tag", "v1.0")
	return dir
}

func TestGitFetchCheckoutsTag(t *testing.T) {
	repo := makeLocalRepo(t)
	f := &GitFetcher{}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGit, URL: repo, Version: "v1.0",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.Len(t, res.ResolvedVersion, 40) // full SHA
	_, err = os.Stat(filepath.Join(res.Dir, "plugin.cfg"))
	require.NoError(t, err)
}

func TestGitFetchBadRef(t *testing.T) {
	repo := makeLocalRepo(t)
	f := &GitFetcher{}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGit, URL: repo, Version: "v9.9",
	})
	require.Error(t, err)
}
```

- [ ] **Step 2: Remove the `GitFetcher` stub** from `source.go`.

- [ ] **Step 3: Run the test, expect FAIL.** Run: `go test ./internal/source/ -run TestGit -v`

- [ ] **Step 4: Create `internal/source/git.go`**

```go
package source

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

// GitFetcher clones a git ref using the system `git` binary, inheriting the
// user's existing SSH and credential-helper configuration.
type GitFetcher struct{}

// Fetch clones spec.URL at spec.Version into a temp directory and reports the
// resolved commit SHA.
func (f *GitFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return FetchResult{}, &output.FetchError{Err: fmt.Errorf("git binary not found on PATH")}
	}
	dir, err := os.MkdirTemp("", "gam-git-*")
	if err != nil {
		return FetchResult{}, &output.InstallError{Err: err}
	}
	steps := [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", spec.URL},
		{"fetch", "-q", "--depth", "1", "origin", spec.Version},
		{"-c", "advice.detachedHead=false", "checkout", "-q", "FETCH_HEAD"},
	}
	for _, args := range steps {
		if err := runGit(ctx, dir, args...); err != nil {
			os.RemoveAll(dir)
			return FetchResult{}, err
		}
	}
	sha, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		os.RemoveAll(dir)
		return FetchResult{}, err
	}
	return FetchResult{Dir: dir, ResolvedVersion: strings.TrimSpace(sha)}, nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return &output.FetchError{Err: fmt.Errorf("git %s: %v: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))}
	}
	return nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", &output.FetchError{Err: fmt.Errorf("git %s: %w", strings.Join(args, " "), err)}
	}
	return string(out), nil
}
```

- [ ] **Step 5: Run the test, expect PASS.** Run: `go test ./internal/source/ -run TestGit -v`

- [ ] **Step 6: Commit**

```bash
git add internal/source
git commit -m "feat: add git fetcher shelling out to system git"
```

---

### Task 8: GitHub release fetcher

**Goal:** Resolve a GitHub release tag, pick the matching asset, download and extract it.

**Files:**
- Create: `internal/source/githubrelease.go` (replaces the `GitHubReleaseFetcher` stub from Task 5)
- Test: `internal/source/githubrelease_test.go`

**Acceptance Criteria:**
- [ ] `GitHubReleaseFetcher.Fetch` calls `releases/tags/<version>`, selects the asset matching `spec.Asset` (glob; sole asset if `Asset` empty), downloads and extracts it.
- [ ] When `GITHUB_TOKEN` (or `GH_TOKEN`) is set, an `Authorization` header is sent.
- [ ] Ambiguous asset selection (multiple match, `Asset` empty) returns an `*output.FetchError`.
- [ ] A configurable API base URL allows testing against `httptest`.

**Verify:** `go test ./internal/source/ -v` → PASS (whole package, including Task 5's `source_test.go`)

**Steps:**

- [ ] **Step 1: Write the failing test `internal/source/githubrelease_test.go`**

```go
package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/stretchr/testify/require"
)

func TestGitHubReleaseFetch(t *testing.T) {
	payload := zipBytes(t, map[string]string{"plugin.cfg": "[plugin]"})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "addon.zip", "browser_download_url": "ASSETURL"},
			},
		})
	})
	mux.HandleFunc("/asset.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f := &GitHubReleaseFetcher{APIBase: srv.URL, assetURLRewrite: func(string) string { return srv.URL + "/asset.zip" }}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "owner/repo", Version: "1.0", Asset: "addon.zip",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.NotEmpty(t, res.Checksum)
	require.Equal(t, "1.0", res.ResolvedVersion)
}

func TestGitHubReleaseAmbiguousAsset(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "a.zip", "browser_download_url": "u1"},
				{"name": "b.zip", "browser_download_url": "u2"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := &GitHubReleaseFetcher{APIBase: srv.URL}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGitHubRelease, Repo: "o/r", Version: "1.0",
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "asset"))
}
```

- [ ] **Step 2: Remove the `GitHubReleaseFetcher` stub** from `source.go`.

- [ ] **Step 3: Run the test, expect FAIL.** Run: `go test ./internal/source/ -run TestGitHubRelease -v`

- [ ] **Step 4: Create `internal/source/githubrelease.go`**

```go
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
)

const defaultGitHubAPIBase = "https://api.github.com"

// GitHubReleaseFetcher downloads an asset from a GitHub release.
type GitHubReleaseFetcher struct {
	// APIBase overrides the GitHub API root; empty means the public API.
	APIBase string
	// assetURLRewrite, if set, rewrites an asset download URL (used in tests).
	assetURLRewrite func(string) string
}

type ghAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	Assets []ghAsset `json:"assets"`
}

// Fetch resolves the release tag, selects the matching asset, and extracts it.
func (f *GitHubReleaseFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (FetchResult, error) {
	base := f.APIBase
	if base == "" {
		base = defaultGitHubAPIBase
	}
	apiURL := fmt.Sprintf("%s/repos/%s/releases/tags/%s", base, spec.Repo, spec.Version)
	body, err := download(ctx, apiURL, githubHeader())
	if err != nil {
		return FetchResult{}, err
	}
	var rel ghRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return FetchResult{}, &output.FetchError{Err: fmt.Errorf("parsing release JSON: %w", err)}
	}
	asset, err := selectAsset(rel.Assets, spec.Asset)
	if err != nil {
		return FetchResult{}, err
	}
	dlURL := asset.DownloadURL
	if f.assetURLRewrite != nil {
		dlURL = f.assetURLRewrite(dlURL)
	}
	data, err := download(ctx, dlURL, githubHeader())
	if err != nil {
		return FetchResult{}, err
	}
	dir, err := os.MkdirTemp("", "gam-ghrel-*")
	if err != nil {
		return FetchResult{}, &output.InstallError{Err: err}
	}
	if err := extractArchive(asset.Name, data, dir); err != nil {
		os.RemoveAll(dir)
		return FetchResult{}, err
	}
	sum := sha256.Sum256(data)
	return FetchResult{
		Dir:             dir,
		ResolvedVersion: spec.Version,
		Checksum:        hex.EncodeToString(sum[:]),
	}, nil
}

// selectAsset chooses the asset matching pattern. An empty pattern requires
// exactly one asset. A pattern uses path.Match glob semantics.
func selectAsset(assets []ghAsset, pattern string) (ghAsset, error) {
	var matches []ghAsset
	for _, a := range assets {
		if pattern == "" {
			matches = append(matches, a)
			continue
		}
		if ok, _ := path.Match(pattern, a.Name); ok {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return ghAsset{}, &output.FetchError{Err: fmt.Errorf("no release asset matched %q", pattern)}
	default:
		return ghAsset{}, &output.FetchError{Err: fmt.Errorf("multiple release assets matched %q; set `asset` to disambiguate", pattern)}
	}
}

// githubHeader returns request headers including auth when a token is in env.
func githubHeader() http.Header {
	h := http.Header{}
	h.Set("Accept", "application/vnd.github+json")
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token != "" {
		h.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	return h
}
```

- [ ] **Step 5: Run the whole package test, expect PASS** (this also runs `source_test.go` from Task 5).

Run: `go test ./internal/source/ -v`

- [ ] **Step 6: Commit**

```bash
git add internal/source
git commit -m "feat: add github release fetcher"
```

---

### Task 9: Installer

**Goal:** Resolve `source_path` within a fetched tree and copy it into `addons/<install_as>/`, replacing any existing directory.

**Files:**
- Create: `internal/installer/installer.go`
- Test: `internal/installer/installer_test.go`

**Acceptance Criteria:**
- [ ] With explicit `source_path`, the installer copies that subtree.
- [ ] With empty `source_path` and a single `addons/<name>/` dir present, that dir is auto-detected.
- [ ] With empty `source_path` and no `addons/` dir, the fetched root is used.
- [ ] Ambiguous auto-detection (multiple dirs under `addons/`) returns an `*output.InstallError`.
- [ ] Installing over an existing directory fully replaces it (stale files removed).

**Verify:** `go test ./internal/installer/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/installer/installer_test.go`**

```go
package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
	"github.com/stretchr/testify/require"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
}

func TestInstallExplicitSourcePath(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"addons/dlg/plugin.cfg": "[plugin]"})
	addonsDir := t.TempDir()

	err := Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{
		Name: "dlg", SourcePath: "addons/dlg",
	}, addonsDir)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "[plugin]", string(got))
}

func TestInstallAutoDetectsSingleAddonsDir(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"addons/dlg/plugin.cfg": "x"})
	addonsDir := t.TempDir()
	require.NoError(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))
	_, err := os.Stat(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
}

func TestInstallUsesRootWhenNoAddonsDir(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"plugin.cfg": "x"})
	addonsDir := t.TempDir()
	require.NoError(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))
	_, err := os.Stat(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
}

func TestInstallAmbiguousFails(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"addons/a/x": "1", "addons/b/y": "2"})
	require.Error(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, t.TempDir()))
}

func TestInstallReplacesStaleFiles(t *testing.T) {
	addonsDir := t.TempDir()
	stale := filepath.Join(addonsDir, "dlg", "old.gd")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o755))
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o644))

	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"plugin.cfg": "new"})
	require.NoError(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))
	_, err := os.Stat(stale)
	require.True(t, os.IsNotExist(err))
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/installer/ -v`

- [ ] **Step 3: Create `internal/installer/installer.go`**

```go
package installer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
)

// Install resolves the source subtree within fetched.Dir and copies it into
// <addonsDir>/<spec.InstallName()>, replacing any existing directory.
func Install(fetched source.FetchResult, spec manifest.AddonSpec, addonsDir string) error {
	srcRoot, err := resolveSourcePath(fetched.Dir, spec.SourcePath)
	if err != nil {
		return err
	}
	dest := filepath.Join(addonsDir, spec.InstallName())
	if err := os.RemoveAll(dest); err != nil {
		return &output.InstallError{Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return &output.InstallError{Err: err}
	}
	if err := copyTree(srcRoot, dest); err != nil {
		return &output.InstallError{Err: err}
	}
	return nil
}

// resolveSourcePath returns the directory within root to install. When
// sourcePath is set it is used directly. Otherwise: a single addons/<name>/
// directory is auto-detected; if no addons/ dir exists, root is used.
func resolveSourcePath(root, sourcePath string) (string, error) {
	if sourcePath != "" {
		p := filepath.Join(root, sourcePath)
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			return "", &output.InstallError{Err: fmt.Errorf("source_path %q not found in fetched source", sourcePath)}
		}
		return p, nil
	}
	addonsDir := filepath.Join(root, "addons")
	entries, err := os.ReadDir(addonsDir)
	if err != nil {
		return root, nil // no addons/ dir: install the root
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	switch len(dirs) {
	case 1:
		return filepath.Join(addonsDir, dirs[0]), nil
	case 0:
		return root, nil
	default:
		return "", &output.InstallError{Err: fmt.Errorf(
			"fetched source has multiple directories under addons/; set source_path explicitly")}
	}
}

// copyTree recursively copies the directory src to dst.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(p, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o200)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
```

- [ ] **Step 4: Run the test, expect PASS.** Run: `go test ./internal/installer/ -v`

- [ ] **Step 5: Commit**

```bash
git add internal/installer
git commit -m "feat: add installer with source_path resolution"
```

---

### Task 10: Install orchestration

**Goal:** A reusable function that installs a set of addons: reconcile against the lockfile, fetch, install, update the lockfile.

**Files:**
- Create: `internal/cli/run.go`
- Test: `internal/cli/run_test.go`

**Acceptance Criteria:**
- [ ] `InstallAddons` installs every addon, writing the resulting `addons.lock`.
- [ ] When a lock entry's `SpecHash` still matches, the addon is still fetched (v1 always fetches; the lock records pins but is not yet a fetch shortcut) — see note below.
- [ ] Each installed addon produces a `LockEntry` with `ResolvedVersion`, `SourcePath`, `Checksum`, and `SpecHash`.
- [ ] Temp directories from `FetchResult.Dir` are removed after install.
- [ ] A `fetcherFor` function field allows tests to inject fake fetchers.

**Verify:** `go test ./internal/cli/ -run TestInstallAddons -v` → PASS

> **Note on lock semantics:** In v1 the lockfile guarantees the *recorded* pin (commit SHA / checksum) is what gets written, and `install` always performs the fetch. `git` fetches are pinned by re-fetching the recorded SHA when present; archives/releases re-download. A future iteration may add checksum-verified caching. Implement exactly the behavior below.

**Steps:**

- [ ] **Step 1: Write the failing test `internal/cli/run_test.go`**

```go
package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
	"github.com/stretchr/testify/require"
)

// fakeFetcher writes a fixed tree and returns canned metadata.
type fakeFetcher struct{ version, checksum string }

func (f fakeFetcher) Fetch(_ context.Context, _ manifest.AddonSpec) (source.FetchResult, error) {
	dir, _ := os.MkdirTemp("", "fake-*")
	_ = os.WriteFile(filepath.Join(dir, "plugin.cfg"), []byte("[plugin]"), 0o644)
	return source.FetchResult{Dir: dir, ResolvedVersion: f.version, Checksum: f.checksum}, nil
}

func TestInstallAddons(t *testing.T) {
	projectRoot := t.TempDir()
	addonsDir := filepath.Join(projectRoot, "addons")
	lockPath := filepath.Join(projectRoot, "addons.lock")

	m := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}

	r := &Runner{
		AddonsDir: addonsDir,
		LockPath:  lockPath,
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return fakeFetcher{version: "1.0", checksum: "deadbeef"}, nil
		},
	}
	results, err := r.InstallAddons(context.Background(), m, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)

	_, err = os.Stat(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)

	lock, err := manifest.LoadLock(lockPath)
	require.NoError(t, err)
	require.Equal(t, "1.0", lock.Addons["dlg"].ResolvedVersion)
	require.Equal(t, "deadbeef", lock.Addons["dlg"].Checksum)
	require.Equal(t, m.Addons["dlg"].Hash(), lock.Addons["dlg"].SpecHash)
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/cli/ -run TestInstallAddons -v`

- [ ] **Step 3: Create `internal/cli/run.go`**

```go
package cli

import (
	"context"
	"os"

	"github.com/CafecitoGames/godot-addon-manager/internal/installer"
	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
)

// AddonResult reports the outcome of installing a single addon.
type AddonResult struct {
	Name            string `json:"name"`
	ResolvedVersion string `json:"resolved_version"`
	InstallPath     string `json:"install_path"`
}

// Runner performs install orchestration. FetcherFor is injectable for testing;
// production code sets it to source.FetcherFor.
type Runner struct {
	AddonsDir  string
	LockPath   string
	FetcherFor func(manifest.AddonSpec) (source.Fetcher, error)
}

// NewRunner builds a Runner wired to the real source layer.
func NewRunner(addonsDir, lockPath string) *Runner {
	return &Runner{AddonsDir: addonsDir, LockPath: lockPath, FetcherFor: source.FetcherFor}
}

// InstallAddons fetches and installs the named addons (all addons when names is
// nil/empty), then writes addons.lock. It returns one AddonResult per addon.
func (r *Runner) InstallAddons(ctx context.Context, m *manifest.Manifest, names []string) ([]AddonResult, error) {
	lock, err := manifest.LoadLock(r.LockPath)
	if err != nil {
		return nil, err
	}
	targets := selectAddons(m, names)
	var results []AddonResult
	for _, spec := range targets {
		fetcher, err := r.FetcherFor(spec)
		if err != nil {
			return nil, err
		}
		fetched, err := fetcher.Fetch(ctx, spec)
		if err != nil {
			return nil, err
		}
		err = installer.Install(fetched, spec, r.AddonsDir)
		os.RemoveAll(fetched.Dir)
		if err != nil {
			return nil, err
		}
		lock.Addons[spec.Name] = manifest.LockEntry{
			ResolvedVersion: fetched.ResolvedVersion,
			SourcePath:      spec.SourcePath,
			Checksum:        fetched.Checksum,
			SpecHash:        spec.Hash(),
		}
		results = append(results, AddonResult{
			Name:            spec.Name,
			ResolvedVersion: fetched.ResolvedVersion,
			InstallPath:     spec.InstallName(),
		})
	}
	if err := lock.Save(r.LockPath); err != nil {
		return nil, err
	}
	return results, nil
}

// selectAddons returns the specs to operate on. An empty names slice selects
// all addons.
func selectAddons(m *manifest.Manifest, names []string) []manifest.AddonSpec {
	if len(names) == 0 {
		out := make([]manifest.AddonSpec, 0, len(m.Addons))
		for _, s := range m.Addons {
			out = append(out, s)
		}
		return out
	}
	var out []manifest.AddonSpec
	for _, n := range names {
		if s, ok := m.Addons[n]; ok {
			out = append(out, s)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the test, expect PASS.** Run: `go test ./internal/cli/ -run TestInstallAddons -v`

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat: add install orchestration runner"
```

---

### Task 11: Output rendering helpers

**Goal:** Render command results as text or JSON consistently across commands.

**Files:**
- Modify: `internal/output/output.go`
- Test: `internal/output/render_test.go`

**Acceptance Criteria:**
- [ ] `Render(w, jsonMode, payload, textFn)` writes `payload` as indented JSON when `jsonMode`, else calls `textFn`.
- [ ] JSON output ends with a newline.

**Verify:** `go test ./internal/output/ -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/output/render_test.go`**

```go
package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	err := Render(&buf, true, map[string]string{"k": "v"}, func() { buf.WriteString("TEXT") })
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"k": "v"`)
	require.NotContains(t, buf.String(), "TEXT")
}

func TestRenderText(t *testing.T) {
	var buf bytes.Buffer
	err := Render(&buf, false, map[string]string{"k": "v"}, func() { buf.WriteString("TEXT") })
	require.NoError(t, err)
	require.Equal(t, "TEXT", buf.String())
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/output/ -v`

- [ ] **Step 3: Append to `internal/output/output.go`**

```go
import (
	"encoding/json"
	"io"
)

// Render writes payload as indented JSON to w when jsonMode is true; otherwise
// it invokes textFn to produce human-readable output.
func Render(w io.Writer, jsonMode bool, payload any, textFn func()) error {
	if !jsonMode {
		textFn()
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
```

Merge the new imports into the existing `import` block at the top of the file (the file already imports `errors`).

- [ ] **Step 4: Run the test, expect PASS.** Run: `go test ./internal/output/ -v`

- [ ] **Step 5: Commit**

```bash
git add internal/output
git commit -m "feat: add text/JSON output rendering"
```

---

### Task 12: `gam init` command

**Goal:** Create a starter `addons.toml` in the current directory.

**Files:**
- Create: `internal/cli/init.go`
- Modify: `internal/cli/root.go` (register the command)
- Test: `internal/cli/init_test.go`

**Acceptance Criteria:**
- [ ] `gam init` writes an `addons.toml` containing an empty `[addons]` table and a commented example.
- [ ] Running `init` when `addons.toml` already exists fails without overwriting.

**Verify:** `go test ./internal/cli/ -run TestInit -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/cli/init_test.go`**

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitCreatesManifest(t *testing.T) {
	dir := t.TempDir()
	cmd := newInitCommand(&Options{})
	cmd.SetArgs([]string{"--dir", dir})
	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "[addons]")
}

func TestInitDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("existing"), 0o644))
	cmd := newInitCommand(&Options{})
	cmd.SetArgs([]string{"--dir", dir})
	require.Error(t, cmd.Execute())
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/cli/ -run TestInit -v`

- [ ] **Step 3: Create `internal/cli/init.go`**

```go
package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/spf13/cobra"
)

const starterManifest = `# Godot addon manifest managed by gam.
# Add addons with ` + "`gam add`" + ` or by hand. Example:
#
# [addons.dialogue_manager]
# source      = "git"
# url         = "https://github.com/owner/dialogue.git"
# version     = "v2.1.0"
# source_path = "addons/dialogue_manager"

[addons]
`

// newInitCommand builds `gam init`.
func newInitCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a starter addons.toml in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = wd
			}
			path := filepath.Join(dir, "addons.toml")
			if _, err := os.Stat(path); err == nil {
				return &output.ManifestError{Err: fmt.Errorf("%s already exists", path)}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := os.WriteFile(path, []byte(starterManifest), 0o644); err != nil {
				return &output.ManifestError{Err: err}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "directory to create addons.toml in (default: current directory)")
	return cmd
}
```

- [ ] **Step 4: Register the command** in `internal/cli/root.go`, inside `NewRootCommand` before `return root`:

```go
	root.AddCommand(newInitCommand(opts))
```

- [ ] **Step 5: Run the test, expect PASS.** Run: `go test ./internal/cli/ -run TestInit -v`

- [ ] **Step 6: Commit**

```bash
git add internal/cli
git commit -m "feat: add gam init command"
```

---

### Task 13: `gam install` and `gam list` commands

**Goal:** Wire project discovery + manifest loading into the `install` and `list` commands.

**Files:**
- Create: `internal/cli/install.go`
- Create: `internal/cli/list.go`
- Modify: `internal/cli/root.go` (register both)
- Test: `internal/cli/install_test.go`

**Acceptance Criteria:**
- [ ] `gam install` discovers the project, validates the manifest, runs the `Runner`, and reports results.
- [ ] `gam list` prints each addon's name, source, version, and whether it is installed; honors `--json`.
- [ ] Both commands accept `--dir` to override the start directory (for testing and scripting).

**Verify:** `go test ./internal/cli/ -run TestInstallCommand -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/cli/install_test.go`**

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallCommandJSON(t *testing.T) {
	// A project with project.godot and a manifest is required; the archive
	// addon points at a local httptest-free fixture is out of scope here, so
	// this test asserts manifest-validation failure surfaces correctly.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"),
		[]byte("[addons]\n[addons.x]\nsource = \"git\"\nurl = \"u\"\n"), 0o644)) // missing version

	cmd := newInstallCommand(&Options{})
	cmd.SetArgs([]string{"--dir", dir})
	require.Error(t, cmd.Execute()) // validation failure
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/cli/ -run TestInstallCommand -v`

- [ ] **Step 3: Create `internal/cli/install.go`**

```go
package cli

import (
	"fmt"
	"os"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/CafecitoGames/godot-addon-manager/internal/project"
	"github.com/spf13/cobra"
)

// loadProject discovers the Godot project from dir (or cwd) and loads its
// validated manifest.
func loadProject(dir string) (*project.Project, *manifest.Manifest, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		dir = wd
	}
	proj, err := project.Discover(dir)
	if err != nil {
		return nil, nil, err
	}
	m, err := manifest.Load(proj.ManifestPath)
	if err != nil {
		return nil, nil, &output.ManifestError{Err: err}
	}
	if err := m.Validate(); err != nil {
		return nil, nil, err
	}
	return proj, m, nil
}

// newInstallCommand builds `gam install`.
func newInstallCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install all addons declared in addons.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, m, err := loadProject(dir)
			if err != nil {
				return err
			}
			runner := NewRunner(proj.AddonsDir, proj.LockPath)
			results, err := runner.InstallAddons(cmd.Context(), m, nil)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts.JSON, results, func() {
				for _, r := range results {
					fmt.Fprintf(cmd.OutOrStdout(), "installed %s @ %s\n", r.Name, r.ResolvedVersion)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%d addon(s) installed\n", len(results))
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}
```

- [ ] **Step 4: Create `internal/cli/list.go`**

```go
package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/spf13/cobra"
)

// addonListing is the per-addon record emitted by `gam list`.
type addonListing struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
}

// newListCommand builds `gam list`.
func newListCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List addons declared in addons.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, m, err := loadProject(dir)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(m.Addons))
			for n := range m.Addons {
				names = append(names, n)
			}
			sort.Strings(names)
			listings := make([]addonListing, 0, len(names))
			for _, n := range names {
				s := m.Addons[n]
				installed := false
				if info, statErr := os.Stat(filepathJoin(proj.AddonsDir, s.InstallName())); statErr == nil {
					installed = info.IsDir()
				}
				listings = append(listings, addonListing{
					Name: n, Source: string(s.Source), Version: s.Version, Installed: installed,
				})
			}
			return output.Render(cmd.OutOrStdout(), opts.JSON, listings, func() {
				for _, l := range listings {
					mark := " "
					if l.Installed {
						mark = "x"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "[%s] %-20s %-16s %s\n", mark, l.Name, l.Source, l.Version)
				}
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}

// filepathJoin is a tiny indirection so this file needs no extra import block
// churn; it simply forwards to path/filepath.Join.
func filepathJoin(parts ...string) string {
	return manifest.Join(parts...)
}
```

> **Correction:** do NOT add a `Join` helper to `manifest`. Instead import `path/filepath` directly in `list.go` and call `filepath.Join`. Replace the `filepathJoin` helper and its use with `filepath.Join`. The helper above is shown only to flag the wrong approach — use the standard library.

Final `list.go` import block and call site:

```go
import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/spf13/cobra"
)
```
and use `filepath.Join(proj.AddonsDir, s.InstallName())` directly; delete the `filepathJoin` function.

- [ ] **Step 5: Register both commands** in `NewRootCommand` (`internal/cli/root.go`):

```go
	root.AddCommand(newInstallCommand(opts))
	root.AddCommand(newListCommand(opts))
```

- [ ] **Step 6: Run the test, expect PASS.** Run: `go test ./internal/cli/ -run TestInstallCommand -v`

- [ ] **Step 7: Commit**

```bash
git add internal/cli
git commit -m "feat: add gam install and list commands"
```

---

### Task 14: `gam update` and `gam remove` commands

**Goal:** Add `update` (re-resolve named/all addons) and `remove` (delete an addon).

**Files:**
- Create: `internal/cli/update.go`
- Create: `internal/cli/remove.go`
- Modify: `internal/cli/root.go` (register both)
- Test: `internal/cli/remove_test.go`

**Acceptance Criteria:**
- [ ] `gam update [name...]` runs the `Runner` for the named addons (all when none named) and reports results.
- [ ] `gam remove <name>` deletes the addon from `addons.toml` and `addons.lock` and removes its installed directory.
- [ ] `gam remove` of an unknown addon returns a `*UsageError`.

**Verify:** `go test ./internal/cli/ -run TestRemove -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/cli/remove_test.go`**

```go
package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoveDeletesAddon(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"),
		[]byte("[addons]\n[addons.x]\nsource = \"archive\"\nurl = \"u\"\n"), 0o644))
	installed := filepath.Join(dir, "addons", "x")
	require.NoError(t, os.MkdirAll(installed, 0o755))

	cmd := newRemoveCommand(&Options{})
	cmd.SetArgs([]string{"x", "--dir", dir})
	require.NoError(t, cmd.Execute())

	_, err := os.Stat(installed)
	require.True(t, os.IsNotExist(err))
	data, _ := os.ReadFile(filepath.Join(dir, "addons.toml"))
	require.NotContains(t, string(data), "addons.x")
}

func TestRemoveUnknownAddon(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))
	cmd := newRemoveCommand(&Options{})
	cmd.SetArgs([]string{"nope", "--dir", dir})
	require.Error(t, cmd.Execute())
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/cli/ -run TestRemove -v`

- [ ] **Step 3: Create `internal/cli/update.go`**

```go
package cli

import (
	"fmt"

	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/spf13/cobra"
)

// newUpdateCommand builds `gam update [name...]`.
func newUpdateCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "update [addon...]",
		Short: "Re-resolve and reinstall addons, rewriting addons.lock",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, m, err := loadProject(dir)
			if err != nil {
				return err
			}
			for _, name := range args {
				if _, ok := m.Addons[name]; !ok {
					return &UsageError{Err: fmt.Errorf("unknown addon %q", name)}
				}
			}
			runner := NewRunner(proj.AddonsDir, proj.LockPath)
			results, err := runner.InstallAddons(cmd.Context(), m, args)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts.JSON, results, func() {
				for _, r := range results {
					fmt.Fprintf(cmd.OutOrStdout(), "updated %s @ %s\n", r.Name, r.ResolvedVersion)
				}
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}
```

- [ ] **Step 4: Create `internal/cli/remove.go`**

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/spf13/cobra"
)

// newRemoveCommand builds `gam remove <addon>`.
func newRemoveCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "remove <addon>",
		Short: "Remove an addon from addons.toml, addons.lock, and disk",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			proj, m, err := loadProject(dir)
			if err != nil {
				return err
			}
			spec, ok := m.Addons[name]
			if !ok {
				return &UsageError{Err: fmt.Errorf("unknown addon %q", name)}
			}
			if err := os.RemoveAll(filepath.Join(proj.AddonsDir, spec.InstallName())); err != nil {
				return &output.InstallError{Err: err}
			}
			delete(m.Addons, name)
			if err := m.Save(proj.ManifestPath); err != nil {
				return err
			}
			lock, err := manifest.LoadLock(proj.LockPath)
			if err != nil {
				return err
			}
			delete(lock.Addons, name)
			if err := lock.Save(proj.LockPath); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}
```

- [ ] **Step 5: Register both commands** in `NewRootCommand`:

```go
	root.AddCommand(newUpdateCommand(opts))
	root.AddCommand(newRemoveCommand(opts))
```

- [ ] **Step 6: Run the test, expect PASS.** Run: `go test ./internal/cli/ -run TestRemove -v`

- [ ] **Step 7: Commit**

```bash
git add internal/cli
git commit -m "feat: add gam update and remove commands"
```

---

### Task 15: `gam add` command (non-interactive)

**Goal:** Add an addon to `addons.toml` from flags and install it; TUI path is wired in Task 16.

**Files:**
- Create: `internal/cli/add.go`
- Modify: `internal/cli/root.go` (register)
- Test: `internal/cli/add_test.go`

**Acceptance Criteria:**
- [ ] `gam add --name N --source archive --url U` appends the addon to `addons.toml` and installs it.
- [ ] Required-flag combinations are validated per source type; a bad combination returns a `*UsageError`.
- [ ] Adding a duplicate name returns a `*UsageError`.
- [ ] When no flags are given and stdout is a TTY, the command calls the TUI hook (a stub function in this task, replaced in Task 16).

**Verify:** `go test ./internal/cli/ -run TestAdd -v` → PASS

**Steps:**

- [ ] **Step 1: Write the failing test `internal/cli/add_test.go`** (uses a fake fetcher via a package hook)

```go
package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
	"github.com/stretchr/testify/require"
)

func TestAddNonInteractive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))

	cmd := newAddCommand(&Options{})
	// Inject a fake fetcher so no network is needed.
	testFetcherFor = func(manifest.AddonSpec) (source.Fetcher, error) {
		return fakeFetcher{version: "1.0"}, nil
	}
	defer func() { testFetcherFor = nil }()

	cmd.SetArgs([]string{"--name", "x", "--source", "archive", "--url", "u", "--dir", dir})
	require.NoError(t, cmd.Execute())

	m, err := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Equal(t, manifest.SourceArchive, m.Addons["x"].Source)
	_, err = os.Stat(filepath.Join(dir, "addons", "x", "plugin.cfg"))
	require.NoError(t, err)
	_ = context.Background()
}

func TestAddRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"),
		[]byte("[addons]\n[addons.x]\nsource=\"archive\"\nurl=\"u\"\n"), 0o644))
	cmd := newAddCommand(&Options{})
	cmd.SetArgs([]string{"--name", "x", "--source", "archive", "--url", "u", "--dir", dir})
	require.Error(t, cmd.Execute())
}
```

- [ ] **Step 2: Run the test, expect FAIL.** Run: `go test ./internal/cli/ -run TestAdd -v`

- [ ] **Step 3: Create `internal/cli/add.go`**

```go
package cli

import (
	"fmt"
	"os"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
	"github.com/spf13/cobra"
)

// testFetcherFor, when non-nil, overrides the source layer in tests.
var testFetcherFor func(manifest.AddonSpec) (source.Fetcher, error)

// runTUI is replaced by the real TUI wizard in Task 16. The default returns an
// error so the non-interactive path is exercised until then.
var runTUI = func() (manifest.AddonSpec, error) {
	return manifest.AddonSpec{}, fmt.Errorf("interactive add requires flags until the TUI is wired up")
}

// addFlags collects the flag values for `gam add`.
type addFlags struct {
	name, source, url, repo, version, asset, sourcePath, installAs, dir string
}

// newAddCommand builds `gam add`.
func newAddCommand(opts *Options) *cobra.Command {
	f := &addFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add an addon to addons.toml and install it",
		RunE: func(cmd *cobra.Command, args []string) error {
			proj, m, err := loadProject(f.dir)
			if err != nil {
				return err
			}
			var spec manifest.AddonSpec
			if f.name == "" && f.source == "" {
				spec, err = runTUI()
				if err != nil {
					return err
				}
			} else {
				spec, err = specFromFlags(f)
				if err != nil {
					return err
				}
			}
			if _, exists := m.Addons[spec.Name]; exists {
				return &UsageError{Err: fmt.Errorf("addon %q already exists", spec.Name)}
			}
			single := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{spec.Name: spec}}
			if err := single.Validate(); err != nil {
				return &UsageError{Err: err}
			}
			m.Addons[spec.Name] = spec
			if err := m.Save(proj.ManifestPath); err != nil {
				return err
			}
			runner := NewRunner(proj.AddonsDir, proj.LockPath)
			if testFetcherFor != nil {
				runner.FetcherFor = testFetcherFor
			}
			if _, err := runner.InstallAddons(cmd.Context(), m, []string{spec.Name}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added and installed %s\n", spec.Name)
			return nil
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&f.name, "name", "", "addon name (table key under [addons])")
	flags.StringVar(&f.source, "source", "", "source type: git, github-release, archive")
	flags.StringVar(&f.url, "url", "", "clone or archive URL")
	flags.StringVar(&f.repo, "repo", "", "GitHub owner/repo (github-release)")
	flags.StringVar(&f.version, "version", "", "git ref or release tag")
	flags.StringVar(&f.asset, "asset", "", "release asset name/glob (github-release)")
	flags.StringVar(&f.sourcePath, "source-path", "", "subdirectory within the source to install")
	flags.StringVar(&f.installAs, "install-as", "", "install directory name (default: addon name)")
	flags.StringVar(&f.dir, "dir", "", "start directory for project discovery")
	return cmd
}

// specFromFlags builds an AddonSpec from flag values.
func specFromFlags(f *addFlags) (manifest.AddonSpec, error) {
	if f.name == "" {
		return manifest.AddonSpec{}, &UsageError{Err: fmt.Errorf("--name is required")}
	}
	return manifest.AddonSpec{
		Name:       f.name,
		Source:     manifest.SourceType(f.source),
		URL:        f.url,
		Repo:       f.repo,
		Version:    f.version,
		Asset:      f.asset,
		SourcePath: f.sourcePath,
		InstallAs:  f.installAs,
	}, nil
}

var _ = os.Stdout // retained for future TTY detection in Task 16
```

- [ ] **Step 4: Register the command** in `NewRootCommand`:

```go
	root.AddCommand(newAddCommand(opts))
```

- [ ] **Step 5: Run the test, expect PASS.** Run: `go test ./internal/cli/ -run TestAdd -v`

- [ ] **Step 6: Commit**

```bash
git add internal/cli
git commit -m "feat: add gam add command (non-interactive)"
```

---

### Task 16: TUI wizard for `gam add`

**Goal:** A Bubble Tea wizard that collects an `AddonSpec` interactively when `gam add` is run with no flags.

**Files:**
- Create: `internal/tui/add.go`
- Modify: `internal/cli/add.go` (replace the `runTUI` stub)
- Test: `internal/tui/add_test.go`

**Acceptance Criteria:**
- [ ] `tui.RunAddWizard` returns a populated `manifest.AddonSpec`.
- [ ] The wizard prompts for source type first, then only the fields relevant to that source.
- [ ] The collected spec passes `Manifest.Validate`.
- [ ] The form's field-collection logic is unit-tested without driving the terminal (the model's update/transition functions are tested directly).

**Verify:** `go test ./internal/tui/ -v` → PASS

**Steps:**

- [ ] **Step 1: Add the Bubble Tea dependency**

```bash
go get github.com/charmbracelet/bubbletea@latest github.com/charmbracelet/bubbles@latest
```

- [ ] **Step 2: Write the failing test `internal/tui/add_test.go`**

The wizard's data model is a plain struct with pure transition methods, so it is testable without a terminal:

```go
package tui

import (
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/stretchr/testify/require"
)

func TestWizardBuildsGitSpec(t *testing.T) {
	w := newWizardState()
	w.setSource(manifest.SourceGit)
	require.Equal(t, []string{"name", "url", "version", "source_path", "install_as"}, w.fieldOrder())

	w.set("name", "dialogue")
	w.set("url", "https://example.com/d.git")
	w.set("version", "v1.0")

	spec := w.spec()
	require.Equal(t, manifest.SourceGit, spec.Source)
	require.Equal(t, "dialogue", spec.Name)

	m := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{spec.Name: spec}}
	require.NoError(t, m.Validate())
}

func TestWizardFieldsForArchive(t *testing.T) {
	w := newWizardState()
	w.setSource(manifest.SourceArchive)
	require.Equal(t, []string{"name", "url", "source_path", "install_as"}, w.fieldOrder())
}

func TestWizardFieldsForRelease(t *testing.T) {
	w := newWizardState()
	w.setSource(manifest.SourceGitHubRelease)
	require.Equal(t, []string{"name", "repo", "version", "asset", "source_path", "install_as"}, w.fieldOrder())
}
```

- [ ] **Step 3: Run the test, expect FAIL.** Run: `go test ./internal/tui/ -v`

- [ ] **Step 4: Create `internal/tui/add.go`** — the `wizardState` value type holds the testable logic; the Bubble Tea model wraps it:

```go
package tui

import (
	"fmt"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
)

// wizardState holds the addon fields collected by the wizard. Its methods are
// pure so they can be unit-tested without a terminal.
type wizardState struct {
	source manifest.SourceType
	values map[string]string
}

func newWizardState() *wizardState {
	return &wizardState{values: map[string]string{}}
}

// setSource records the chosen source type.
func (w *wizardState) setSource(s manifest.SourceType) { w.source = s }

// set records a field value.
func (w *wizardState) set(field, value string) { w.values[field] = value }

// fieldOrder returns the input fields to prompt for, given the source type.
func (w *wizardState) fieldOrder() []string {
	switch w.source {
	case manifest.SourceGit:
		return []string{"name", "url", "version", "source_path", "install_as"}
	case manifest.SourceArchive:
		return []string{"name", "url", "source_path", "install_as"}
	case manifest.SourceGitHubRelease:
		return []string{"name", "repo", "version", "asset", "source_path", "install_as"}
	default:
		return nil
	}
}

// spec assembles a manifest.AddonSpec from the collected values.
func (w *wizardState) spec() manifest.AddonSpec {
	return manifest.AddonSpec{
		Name:       w.values["name"],
		Source:     w.source,
		URL:        w.values["url"],
		Repo:       w.values["repo"],
		Version:    w.values["version"],
		Asset:      w.values["asset"],
		SourcePath: w.values["source_path"],
		InstallAs:  w.values["install_as"],
	}
}

// RunAddWizard runs the interactive Bubble Tea wizard and returns the spec the
// user assembled.
func RunAddWizard() (manifest.AddonSpec, error) {
	state, err := runProgram()
	if err != nil {
		return manifest.AddonSpec{}, err
	}
	spec := state.spec()
	m := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{spec.Name: spec}}
	if err := m.Validate(); err != nil {
		return manifest.AddonSpec{}, fmt.Errorf("wizard produced invalid addon: %w", err)
	}
	return spec, nil
}
```

- [ ] **Step 5: Create the Bubble Tea program** in the same file (`internal/tui/add.go`), appended below — a two-stage flow: a source-type list, then a sequence of `textinput` fields driven by `fieldOrder()`:

```go
import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// model is the Bubble Tea model wrapping wizardState.
type model struct {
	state     *wizardState
	stage     int // 0 = pick source, 1 = fill fields, 2 = done
	sourceIdx int
	fieldIdx  int
	input     textinput.Model
	err       error
}

var sourceChoices = []manifest.SourceType{
	manifest.SourceGit, manifest.SourceGitHubRelease, manifest.SourceArchive,
}

func initialModel() model {
	ti := textinput.New()
	ti.Focus()
	return model{state: newWizardState(), input: ti}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	switch key.String() {
	case "ctrl+c", "esc":
		m.err = fmt.Errorf("cancelled")
		return m, tea.Quit
	case "up":
		if m.stage == 0 && m.sourceIdx > 0 {
			m.sourceIdx--
		}
	case "down":
		if m.stage == 0 && m.sourceIdx < len(sourceChoices)-1 {
			m.sourceIdx++
		}
	case "enter":
		return m.advance()
	}
	if m.stage == 1 {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// advance moves the wizard forward when the user presses enter.
func (m model) advance() (tea.Model, tea.Cmd) {
	switch m.stage {
	case 0:
		m.state.setSource(sourceChoices[m.sourceIdx])
		m.stage = 1
		m.input.SetValue("")
		return m, nil
	case 1:
		fields := m.state.fieldOrder()
		m.state.set(fields[m.fieldIdx], m.input.Value())
		m.fieldIdx++
		if m.fieldIdx >= len(fields) {
			m.stage = 2
			return m, tea.Quit
		}
		m.input.SetValue("")
		return m, nil
	}
	return m, tea.Quit
}

func (m model) View() string {
	switch m.stage {
	case 0:
		s := "Select addon source type:\n\n"
		for i, c := range sourceChoices {
			cursor := "  "
			if i == m.sourceIdx {
				cursor = "> "
			}
			s += cursor + string(c) + "\n"
		}
		return s + "\n(↑/↓ to move, enter to select, esc to cancel)\n"
	case 1:
		field := m.state.fieldOrder()[m.fieldIdx]
		return fmt.Sprintf("Enter %s:\n\n%s\n\n(enter to confirm, esc to cancel)\n", field, m.input.View())
	default:
		return "Done.\n"
	}
}

// runProgram runs the Bubble Tea program and returns the collected state.
func runProgram() (*wizardState, error) {
	final, err := tea.NewProgram(initialModel()).Run()
	if err != nil {
		return nil, err
	}
	fm := final.(model)
	if fm.err != nil {
		return nil, fm.err
	}
	return fm.state, nil
}
```

Merge the two `import` blocks in the file into one.

- [ ] **Step 6: Wire the TUI into `gam add`** — in `internal/cli/add.go`, replace the `runTUI` stub variable:

```go
import "github.com/CafecitoGames/godot-addon-manager/internal/tui"

// runTUI launches the interactive add wizard.
var runTUI = tui.RunAddWizard
```

Remove the old placeholder `runTUI` definition and the now-unused `var _ = os.Stdout` line plus the `os` import if it is otherwise unused.

- [ ] **Step 7: Run the tests, expect PASS.** Run: `go test ./internal/tui/ ./internal/cli/ -v`

- [ ] **Step 8: Commit**

```bash
git add internal/tui internal/cli go.mod go.sum
git commit -m "feat: add interactive TUI wizard for gam add"
```

---

### Task 17: End-to-end wiring and README

**Goal:** Verify the whole tool builds and works end to end against a real local git repo, and document usage.

**Files:**
- Create: `internal/cli/e2e_test.go`
- Create: `README.md`

**Acceptance Criteria:**
- [ ] An end-to-end test creates a Godot project, an `addons.toml` with a git addon pointing at a local repo, runs `install`, and asserts the addon files land under `addons/` and `addons.lock` is written.
- [ ] `go build ./...` and `go vet ./...` pass.
- [ ] `README.md` documents installation, `addons.toml` format, every command, the `--json` flag, exit codes, and the `GITHUB_TOKEN` env var.

**Verify:** `go test ./... && go vet ./...` → all PASS

**Steps:**

- [ ] **Step 1: Write `internal/cli/e2e_test.go`**

```go
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func gitInitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "plugin.cfg"), []byte("[plugin]"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	run("tag", "v1.0")
	return repo
}

func TestEndToEndInstallGitAddon(t *testing.T) {
	repo := gitInitRepo(t)
	proj := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(proj, "project.godot"), nil, 0o644))
	manifestBody := "[addons]\n[addons.dlg]\nsource = \"git\"\nurl = \"" + repo + "\"\nversion = \"v1.0\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(proj, "addons.toml"), []byte(manifestBody), 0o644))

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"install", "--dir", proj})
	require.NoError(t, cmd.Execute())

	_, err := os.Stat(filepath.Join(proj, "addons", "dlg", "plugin.cfg"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(proj, "addons.lock"))
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run the e2e test, expect PASS.** Run: `go test ./internal/cli/ -run TestEndToEnd -v`

- [ ] **Step 3: Write `README.md`** covering: what `gam` is; install (`go install ./cmd/gam`); the `addons.toml` format with one example per source type; the field reference table; every command (`init`, `add`, `remove`, `install`, `update`, `list`, `completion`); the global `--json`, `-v`, `-q` flags; the exit-code table (0–5); and the `GITHUB_TOKEN`/`GH_TOKEN` env vars for private GitHub releases. Use the design spec (`docs/superpowers/specs/2026-05-16-godot-addon-manager-design.md`) as the source of truth.

- [ ] **Step 4: Run the full suite and vet.** Run: `go test ./... && go vet ./...` — expect all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/e2e_test.go README.md
git commit -m "test: add end-to-end install test and README"
```

---

## Self-Review Notes

- **Spec coverage:** manifest (Tasks 1-3), validation (Task 2), project discovery (Task 4), three source types (Tasks 6-8), installer with `source_path` auto-detection (Task 9), lockfile (Task 3, written in Task 10), all six commands + completion (Tasks 12-16; `completion` is provided automatically by Cobra and needs no task), `--json` and exit codes (Tasks 0, 11), auth via system git and `GITHUB_TOKEN` (Tasks 7-8), TUI (Task 16). All spec sections map to tasks.
- **`completion` command:** Cobra registers `completion` automatically on any root command; no dedicated task is required. The README (Task 17) documents it.
- **Type consistency:** `AddonSpec`, `Manifest`, `Lockfile`, `LockEntry`, `FetchResult`, `Fetcher`, `Runner`, `AddonResult`, and `Options` keep identical signatures across all tasks that reference them.
- **Lock semantics:** Task 10 explicitly scopes v1 behavior — the lockfile records resolved pins and is always rewritten on install; it is not yet a fetch-skipping cache. This is intentional and called out.
