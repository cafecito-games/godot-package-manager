package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
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

func TestHashChangesWithManifestExclusions(t *testing.T) {
	dir := t.TempDir()
	withoutExclude := filepath.Join(dir, "without.toml")
	require.NoError(t, os.WriteFile(withoutExclude, []byte(`
[addons]
[addons.dialogue]
source = "archive"
url = "https://example.com/dialogue.zip"
`), 0o644))
	withExclude := filepath.Join(dir, "with.toml")
	require.NoError(t, os.WriteFile(withExclude, []byte(`
[addons]
[addons.dialogue]
source = "archive"
url = "https://example.com/dialogue.zip"
exclude = ["dotnet"]
`), 0o644))

	a, err := Load(withoutExclude)
	require.NoError(t, err)
	b, err := Load(withExclude)
	require.NoError(t, err)

	require.NotEqual(t, a.Addons["dialogue"].Hash(), b.Addons["dialogue"].Hash())
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	require.Error(t, err)
}

func TestLoadBadTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	require.NoError(t, os.WriteFile(path, []byte("not = = valid"), 0o644))
	_, err := Load(path)
	require.Error(t, err)
}

func TestLoadResolvesProjectPlatformsOntoEachAddon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[project]
platforms = ["ios.arm64", "android.arm64"]

[addons.inherits]
source = "archive"
url = "https://example.com/a.zip"

[addons.overrides]
source    = "archive"
url       = "https://example.com/b.zip"
platforms = ["macos"]

[addons.opts_out]
source    = "archive"
url       = "https://example.com/c.zip"
platforms = []
`), 0o644))

	m, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"ios.arm64", "android.arm64"}, m.Project.Platforms)
	require.Equal(t, []string{"ios.arm64", "android.arm64"}, m.Addons["inherits"].Platforms)
	require.Equal(t, []string{"macos"}, m.Addons["overrides"].Platforms, "a per-addon list replaces the project list")
	require.NotNil(t, m.Addons["opts_out"].Platforms, "an explicit empty list is not an absent list")
	require.Empty(t, m.Addons["opts_out"].Platforms)
}

func TestLoadWithEmptyProjectPlatformsLeavesAddonsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[project]
platforms = []

[addons.thing]
source = "archive"
url    = "https://example.com/a.zip"
`), 0o644))

	m, err := Load(path)
	require.NoError(t, err)
	require.Empty(t, m.Addons["thing"].Platforms)
	require.NoError(t, m.Validate())
}

func TestLoadWithoutProjectTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[addons.thing]
source = "archive"
url    = "https://example.com/a.zip"
`), 0o644))

	m, err := Load(path)
	require.NoError(t, err)
	require.Nil(t, m.Project.Platforms)
	require.Nil(t, m.Addons["thing"].Platforms)
	require.NoError(t, m.Validate())
}

func TestLoadRejectsUnknownProjectKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[project]
platform = ["macos"]

[addons.thing]
source = "archive"
url    = "https://example.com/a.zip"
`), 0o644))

	_, err := Load(path)
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.True(t, errors.As(err, &manifestError))
	require.Equal(t, output.ExitCode(3), output.CodeFor(err))
	require.Contains(t, err.Error(), "project.platform")
}

// TestLoadIgnoresUnknownKeysOutsideProject pins a deliberate compatibility
// decision: strict decoding is scoped to [project], which is new in this
// release, so manifests carrying a stray addon key keep installing.
func TestLoadIgnoresUnknownKeysOutsideProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[project]
platforms = ["macos"]

[addons.thing]
source = "archive"
url    = "https://example.com/a.zip"
bogus  = 1

[unrelated]
whatever = true
`), 0o644))

	m, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"macos"}, m.Addons["thing"].Platforms)
	require.NoError(t, m.Validate())
}

func TestManifestRoundTripsPlatformsAndIndex(t *testing.T) {
	m := &Manifest{
		Project: ProjectConfig{Platforms: []string{"ios.arm64", "macos"}},
		Addons: map[string]AddonSpec{
			"inherits": {Source: SourceArchive, URL: "https://example.com/a.zip", Platforms: []string{"ios.arm64", "macos"}},
			"sliced": {
				Source:    SourceArchive,
				URL:       "https://example.com/b.zip",
				Index:     "https://example.com/gpm-index.toml",
				Platforms: []string{"windows.x86_64"},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, m.Save(path))

	loaded, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"ios.arm64", "macos"}, loaded.Project.Platforms)
	require.Equal(t, []string{"windows.x86_64"}, loaded.Addons["sliced"].Platforms)
	require.Equal(t, "https://example.com/gpm-index.toml", loaded.Addons["sliced"].Index)
	require.Equal(t, m.Addons["sliced"].Hash(), loaded.Addons["sliced"].Hash())
	require.Equal(t, m.Addons["inherits"].Hash(), loaded.Addons["inherits"].Hash())
}

// TestLoadAcceptsInitStarterManifest loads the exact bytes `gpm init` writes.
// The fixture was produced by running the real producer, the starterManifest
// template at internal/cli/init.go:14 written by newInitCommand at
// internal/cli/init.go:27, rather than hand-written, so a change to what gpm
// emits cannot pass this test while breaking a freshly initialized project.
func TestLoadAcceptsInitStarterManifest(t *testing.T) {
	m, err := Load(filepath.Join("testdata", "init-starter-addons.toml"))
	require.NoError(t, err)
	require.Empty(t, m.Addons)
	require.Nil(t, m.Project.Platforms)
	require.NoError(t, m.Validate())
}

// TestLoadAcceptsInitStarterManifestWithProjectPlatforms loads the same producer
// bytes after a user has added a [project] platforms table and addons, which is
// the shape this change is for.
func TestLoadAcceptsInitStarterManifestWithProjectPlatforms(t *testing.T) {
	m, err := Load(filepath.Join("testdata", "init-starter-with-project-addons.toml"))
	require.NoError(t, err)
	require.NoError(t, m.Validate())
	require.Equal(t, []string{"ios.arm64", "android.arm64"}, m.Project.Platforms)
	require.Equal(t, []string{"ios.arm64", "android.arm64"}, m.Addons["limboai"].Platforms)
	require.Equal(t, []string{"macos"}, m.Addons["sliced_tool"].Platforms)
	require.Equal(t, "https://example.com/gpm-index.toml", m.Addons["sliced_tool"].Index)
}

// TestLoadDoesNotAliasTheProjectPlatformList pins that an inheriting addon holds
// its own copy, so mutating one addon's list cannot reach the project table or
// another addon.
func TestLoadDoesNotAliasTheProjectPlatformList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[project]
platforms = ["macos", "windows"]

[addons.one]
source = "archive"
url    = "https://example.com/a.zip"

[addons.two]
source = "archive"
url    = "https://example.com/b.zip"
`), 0o644))

	m, err := Load(path)
	require.NoError(t, err)
	m.Addons["one"].Platforms[0] = "mutated"
	require.Equal(t, []string{"macos", "windows"}, m.Project.Platforms)
	require.Equal(t, []string{"macos", "windows"}, m.Addons["two"].Platforms)
}

// TestSaveDoesNotFreezeInheritedPlatformsIntoOverrides pins the load-modify-save
// cycle that `gpm add`, `gpm remove`, and the AssetLib wizard all perform. An
// inherited platform list must not be written back as a per-addon key: that
// would silently convert every addon into a permanent override, so a later edit
// to [project] platforms would stop reaching it.
func TestSaveDoesNotFreezeInheritedPlatformsIntoOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[project]
platforms = ["ios.arm64"]

[addons.inherits]
source = "archive"
url    = "https://example.com/a.zip"

[addons.overrides]
source    = "archive"
url       = "https://example.com/b.zip"
platforms = ["macos"]
`), 0o644))

	m, err := Load(path)
	require.NoError(t, err)
	m.Addons["added"] = AddonSpec{Name: "added", Source: SourceArchive, URL: "https://example.com/c.zip"}
	require.NoError(t, m.Save(path))

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(saved), `platforms = ["ios.arm64"]`),
		"the project list must appear once, under [project], and never as a per-addon override:\n%s", saved)

	m.Project.Platforms = append(m.Project.Platforms, "android.arm64")
	require.NoError(t, m.Save(path))
	reloaded, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"ios.arm64", "android.arm64"}, reloaded.Addons["inherits"].Platforms,
		"an inheriting addon must keep tracking the project list across a save")
	require.Equal(t, []string{"ios.arm64", "android.arm64"}, reloaded.Addons["added"].Platforms)
	require.Equal(t, []string{"macos"}, reloaded.Addons["overrides"].Platforms,
		"a declared override must survive a save")
}

// TestSetAddonResolvesPlatformsLikeLoad pins that an addon inserted in-process is
// indistinguishable from one read from disk. `gpm add` inserts a spec and then
// installs from the same in-memory manifest, so an unresolved entry would both
// hash differently from its on-disk form and be selected for the wrong slices.
func TestSetAddonResolvesPlatformsLikeLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[project]
platforms = ["ios.arm64"]
`), 0o644))

	m, err := Load(path)
	require.NoError(t, err)
	m.SetAddon(AddonSpec{Name: "added", Source: SourceArchive, URL: "https://example.com/c.zip"})
	require.Equal(t, []string{"ios.arm64"}, m.Addons["added"].Platforms)

	require.NoError(t, m.Save(path))
	reloaded, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, m.Addons["added"].Hash(), reloaded.Addons["added"].Hash(),
		"a spec_hash written during add must match the hash computed on the next load")
}

func TestSetAddonKeepsAnExplicitOverride(t *testing.T) {
	m := &Manifest{Project: ProjectConfig{Platforms: []string{"ios.arm64"}}}
	m.SetAddon(AddonSpec{Name: "x", Source: SourceArchive, URL: "https://example.com/a.zip", Platforms: []string{"macos"}})
	require.Equal(t, []string{"macos"}, m.Addons["x"].Platforms)
}
