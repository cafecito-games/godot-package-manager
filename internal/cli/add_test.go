package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/source"
	"github.com/stretchr/testify/require"
)

func TestAddNonInteractive(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))

	cmd := newAddCommand(&Options{})
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
}

func TestAddRejectsDuplicate(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"),
		[]byte("[addons]\n[addons.x]\nsource=\"archive\"\nurl=\"u\"\n"), 0o644))
	cmd := newAddCommand(&Options{})
	cmd.SetArgs([]string{"--name", "x", "--source", "archive", "--url", "u", "--dir", dir})
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
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
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
	cmd.SetArgs([]string{"--name", "x", "--source", "archive", "--url", "u", "--dir", dir})
	err := cmd.Execute()
	require.ErrorIs(t, err, fetchErr)

	m, loadErr := manifest.Load(filepath.Join(dir, "addons.toml"))
	require.NoError(t, loadErr)
	require.NotContains(t, m.Addons, "x")
	_, statErr := os.Stat(filepath.Join(dir, "addons", "x"))
	require.True(t, os.IsNotExist(statErr))
}
