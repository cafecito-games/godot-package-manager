package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-addon-manager/internal/manifest"
	"github.com/cafecito-games/godot-addon-manager/internal/source"
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
	results, err := r.InstallAddons(context.Background(), m, nil, ModeInstall)
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

func TestInstallAddonsNamedSubset(t *testing.T) {
	projectRoot := t.TempDir()
	addonsDir := filepath.Join(projectRoot, "addons")
	lockPath := filepath.Join(projectRoot, "addons.lock")

	m := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"addon-a": {Name: "addon-a", Source: manifest.SourceArchive, URL: "u-a"},
		"addon-b": {Name: "addon-b", Source: manifest.SourceArchive, URL: "u-b"},
	}}

	r := &Runner{
		AddonsDir: addonsDir,
		LockPath:  lockPath,
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return fakeFetcher{version: "1.0", checksum: "abc123"}, nil
		},
	}

	results, err := r.InstallAddons(context.Background(), m, []string{"addon-a"}, ModeInstall)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "addon-a", results[0].Name)

	_, err = os.Stat(filepath.Join(addonsDir, "addon-a"))
	require.NoError(t, err, "addon-a directory should exist")

	_, err = os.Stat(filepath.Join(addonsDir, "addon-b"))
	require.True(t, os.IsNotExist(err), "addon-b directory should not exist")
}

func TestInstallAddonsFetcherError(t *testing.T) {
	projectRoot := t.TempDir()
	addonsDir := filepath.Join(projectRoot, "addons")
	lockPath := filepath.Join(projectRoot, "addons.lock")

	m := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}

	fetcherError := errors.New("boom")
	r := &Runner{
		AddonsDir: addonsDir,
		LockPath:  lockPath,
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return nil, fetcherError
		},
	}

	_, err := r.InstallAddons(context.Background(), m, nil, ModeInstall)
	require.ErrorIs(t, err, fetcherError)
}
