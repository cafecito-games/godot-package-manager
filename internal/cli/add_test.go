package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
	"github.com/cafecito-games/godot-package-manager/internal/source"
	"github.com/stretchr/testify/require"
)

// fixedSpecFetcher lets an end-to-end command test use the real archive slice
// fetcher while preserving the github-release command shape users invoke. The
// release API transport is already covered in internal/source; this test owns
// the add transaction, selection policy, and installed tree instead.
type fixedSpecFetcher struct {
	fetcher source.Fetcher
	spec    manifest.AddonSpec
}

func (f fixedSpecFetcher) Fetch(ctx context.Context, _ manifest.AddonSpec) (source.FetchResult, error) {
	return f.fetcher.Fetch(ctx, f.spec)
}

func TestAddHelpDocumentsHostToolTransaction(t *testing.T) {
	t.Parallel()
	cmd := newAddCommand(&Options{})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, output.String(), "--host-tool")
	require.Contains(t, output.String(), "platforms = []")
	require.Contains(t, output.String(), "current host slice")
}

func TestAddNonInteractive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))

	cmd := newAddCommand(&Options{})
	testFetcherFor = func(manifest.AddonSpec) (source.Fetcher, error) {
		return fakeFetcher{version: "1.0"}, nil
	}
	defer func() { testFetcherFor = nil }()

	cmd.SetArgs([]string{"--name", "x", "--source", "archive", "--url", "https://example.com/x.zip", "--dir", dir})
	require.NoError(t, cmd.Execute())

	m, err := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Equal(t, manifest.SourceArchive, m.Addons["x"].Source)
	_, err = os.Stat(filepath.Join(dir, "addons", "x", "plugin.cfg"))
	require.NoError(t, err)
}

func TestAddRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"),
		[]byte("[addons]\n[addons.x]\nsource=\"archive\"\nurl=\"https://example.com/x.zip\"\n"), 0o644))
	cmd := newAddCommand(&Options{})
	cmd.SetArgs([]string{"--name", "x", "--source", "archive", "--url", "https://example.com/x.zip", "--dir", dir})
	err := cmd.Execute()
	require.Error(t, err)
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
}

func TestAddNoFlagsInvokesTUI(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))

	original := runTUI
	runTUI = func() (manifest.AddonSpec, error) {
		return manifest.AddonSpec{}, &UsageError{Err: errors.New("tui disabled in test")}
	}
	defer func() { runTUI = original }()

	cmd := newAddCommand(&Options{})
	cmd.SetArgs([]string{"--dir", dir})
	err := cmd.Execute()
	require.Error(t, err)
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
}

func TestAddRejectsBadSourceCombo(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))
	cmd := newAddCommand(&Options{})
	// git source missing url and version
	cmd.SetArgs([]string{"--name", "x", "--source", "git", "--dir", dir})
	err := cmd.Execute()
	require.Error(t, err)
	var manifestErr *output.ManifestError
	require.ErrorAs(t, err, &manifestErr)
}

func TestAddFetchFailureDoesNotPersistManifestEntry(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))

	fetchErr := errors.New("fetch failed")
	testFetcherFor = func(manifest.AddonSpec) (source.Fetcher, error) {
		return nil, fetchErr
	}
	defer func() { testFetcherFor = nil }()

	cmd := newAddCommand(&Options{})
	cmd.SetArgs([]string{"--name", "x", "--source", "archive", "--url", "https://example.com/x.zip", "--dir", dir})
	err := cmd.Execute()
	require.ErrorIs(t, err, fetchErr)

	m, loadErr := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, loadErr)
	require.NotContains(t, m.Addons, "x")
	_, statErr := os.Stat(filepath.Join(dir, "addons", "x"))
	require.True(t, os.IsNotExist(statErr))
}

// TestAddDoesNotChurnTheLockUnderProjectPlatforms pins that the spec_hash `gpm
// add` writes matches the hash computed when the manifest is loaded afresh. An
// entry inserted without its resolved platform list would hash differently and
// force a spurious re-resolve on the very next install.
func TestAddDoesNotChurnTheLockUnderProjectPlatforms(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"),
		[]byte("[project]\nplatforms = [\"ios.arm64\"]\n\n[addons]\n"), 0o644))

	cmd := newAddCommand(&Options{})
	testFetcherFor = func(manifest.AddonSpec) (source.Fetcher, error) {
		return fakeFetcher{version: "1.0"}, nil
	}
	defer func() { testFetcherFor = nil }()

	cmd.SetArgs([]string{"--name", "x", "--source", "archive", "--url", "https://example.com/x.zip", "--dir", dir})
	require.NoError(t, cmd.Execute())

	m, err := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Equal(t, []string{"ios.arm64"}, m.Addons["x"].Platforms)

	lock, err := manifest.LoadLock(filepath.Join(dir, "addons.lock"))
	require.NoError(t, err)
	require.Equal(t, m.Addons["x"].Hash(), lock.Addons["x"].SpecHash)
	require.False(t, manifest.NeedsResolve(m.Addons["x"], lock))
}

func TestAddHostToolAtomicallyDeclaresEmptyPlatformsAndInstalls(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte(
		"[project]\nplatforms = [\"ios.arm64\", \"web.wasm32\"]\n\n[addons]\n"), 0o644))

	cmd := newAddCommand(&Options{})
	originalRunner := newAddRunner
	var selectedMode slice.SelectionMode
	newAddRunner = func(addonsDir, lockPath, statePath string, limits source.Limits, mode slice.SelectionMode) *Runner {
		selectedMode = mode
		return NewRunner(addonsDir, lockPath, statePath, limits, mode)
	}
	testFetcherFor = func(manifest.AddonSpec) (source.Fetcher, error) {
		return fakeFetcher{version: "1.0"}, nil
	}
	t.Cleanup(func() { testFetcherFor = nil; newAddRunner = originalRunner })
	cmd.SetArgs([]string{
		"--name", "gdtest", "--source", "archive", "--url", "https://example.com/gdtest.zip",
		"--host-tool", "--dir", dir,
	})
	require.NoError(t, cmd.Execute())
	require.Equal(t, slice.SelectHostOnly, selectedMode)

	contents, err := os.ReadFile(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Contains(t, string(contents), "[addons.gdtest]")
	require.Equal(t, 1, strings.Count(string(contents), "platforms = []"),
		"only the host-tool addon should carry the explicit empty override:\n%s", contents)

	m, err := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.NotNil(t, m.Addons["gdtest"].Platforms)
	require.Empty(t, m.Addons["gdtest"].Platforms)
	lock, err := manifest.LoadLock(filepath.Join(dir, "addons.lock"))
	require.NoError(t, err)
	require.Equal(t, m.Addons["gdtest"].Hash(), lock.Addons["gdtest"].SpecHash)
	_, err = os.Stat(filepath.Join(dir, "addons", "gdtest", "plugin.cfg"))
	require.NoError(t, err)
}

func TestAddHostToolInstallsOnlyCoreAndCurrentHostFromRealSlices(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte(
		"[project]\nplatforms = [\"ios.arm64\", \"web.wasm32\"]\n\n[addons]\n"), 0o644))

	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "host-tool transaction"))
	testFetcherFor = func(spec manifest.AddonSpec) (source.Fetcher, error) {
		require.Equal(t, manifest.SourceGitHubRelease, spec.Source)
		require.Equal(t, "cafecito-games/gdtest", spec.Repo)
		require.Equal(t, slicedVersion, spec.Version)
		require.NotNil(t, spec.Platforms)
		require.Empty(t, spec.Platforms)

		archiveSpec := manifest.AddonSpec{
			Name:      slicedAddonName,
			Source:    manifest.SourceArchive,
			URL:       publisher.archiveURL(),
			Index:     publisher.indexURL(),
			Version:   slicedVersion,
			Platforms: spec.Platforms,
		}
		fetcher, err := source.FetcherForWithLimits(source.Limits{}, slice.SelectHostOnly)(archiveSpec)
		if err != nil {
			return nil, err
		}
		return fixedSpecFetcher{fetcher: fetcher, spec: archiveSpec}, nil
	}
	t.Cleanup(func() { testFetcherFor = nil })

	cmd := newAddCommand(&Options{})
	cmd.SetArgs([]string{
		"--name", "gdtest",
		"--source", "github-release",
		"--repo", "cafecito-games/gdtest",
		"--version", slicedVersion,
		"--host-tool",
		"--dir", dir,
	})
	require.NoError(t, cmd.Execute())

	contents, err := os.ReadFile(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Contains(t, string(contents), "[addons.gdtest]")
	require.Contains(t, string(contents), "platforms = []")
	require.Contains(t, string(contents), `repo = "cafecito-games/gdtest"`)

	installed := installedFilesForAddon(t, dir, "gdtest")
	require.Contains(t, installed, "plugin.cfg", "core must be installed")
	require.Contains(t, installed, "bin/addon_macos.dylib", "current host slice must be installed")
	require.NotContains(t, installed, "bin/addon_ios.dylib", "declared export slice must not be installed")
	require.NotContains(t, installed, "bin/addon_windows.dll")
	require.NotContains(t, installed, "bin/addon_linux.so")

	lock, err := manifest.LoadLock(filepath.Join(dir, "addons.lock"))
	require.NoError(t, err)
	require.Len(t, lock.Addons["gdtest"].Slices, 5, "lock must pin every published slice")
	for _, id := range []string{"core", "ios.arm64", "linux.x86_64", "macos", "windows.x86_64"} {
		require.Contains(t, lock.Addons["gdtest"].Slices, id)
	}
	state, err := manifest.LoadState(filepath.Join(dir, ".gpm-state.toml"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"core", "macos"}, state.Addons["gdtest"].Slices,
		"machine state must record only core plus the current host slice")
}

func TestAddHostToolFailureRollsBackExplicitPlatformOverride(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))

	fetchErr := errors.New("fetch failed")
	testFetcherFor = func(manifest.AddonSpec) (source.Fetcher, error) { return nil, fetchErr }
	t.Cleanup(func() { testFetcherFor = nil })
	cmd := newAddCommand(&Options{})
	cmd.SetArgs([]string{
		"--name", "gdtest", "--source", "archive", "--url", "https://example.com/gdtest.zip",
		"--host-tool", "--dir", dir,
	})
	require.ErrorIs(t, cmd.Execute(), fetchErr)

	contents, err := os.ReadFile(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.NotContains(t, string(contents), "gdtest")
	require.NotContains(t, string(contents), "platforms = []")
}
