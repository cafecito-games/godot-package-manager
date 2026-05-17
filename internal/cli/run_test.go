package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/source"
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

func TestInstallAddonsPersistsSuccessfulLockBeforeLaterFailure(t *testing.T) {
	projectRoot := t.TempDir()
	addonsDir := filepath.Join(projectRoot, "addons")
	lockPath := filepath.Join(projectRoot, "addons.lock")

	m := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"ok":  {Name: "ok", Source: manifest.SourceArchive, URL: "u-ok"},
		"bad": {Name: "bad", Source: manifest.SourceArchive, URL: "u-bad"},
	}}

	fetcherError := errors.New("fetch failed")
	r := &Runner{
		AddonsDir: addonsDir,
		LockPath:  lockPath,
		FetcherFor: func(spec manifest.AddonSpec) (source.Fetcher, error) {
			if spec.Name == "bad" {
				return nil, &output.FetchError{Err: fetcherError}
			}
			return fakeFetcher{version: "1.0", checksum: "abc123"}, nil
		},
	}

	_, err := r.InstallAddons(context.Background(), m, []string{"ok", "bad"}, ModeUpdate)
	require.ErrorIs(t, err, fetcherError)

	_, err = os.Stat(filepath.Join(addonsDir, "ok", "plugin.cfg"))
	require.NoError(t, err, "successful addon should remain installed")

	lock, err := manifest.LoadLock(lockPath)
	require.NoError(t, err)
	require.Equal(t, "1.0", lock.Addons["ok"].ResolvedVersion)
	require.Equal(t, m.Addons["ok"].Hash(), lock.Addons["ok"].SpecHash)
	_, badLocked := lock.Addons["bad"]
	require.False(t, badLocked, "failed addon should not be added to the lockfile")
}

func TestInstallAddonsSelectsAllAddonsInNameOrder(t *testing.T) {
	m := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"zeta":  {Name: "zeta", Source: manifest.SourceArchive, URL: "u-z"},
		"alpha": {Name: "alpha", Source: manifest.SourceArchive, URL: "u-a"},
		"mid":   {Name: "mid", Source: manifest.SourceArchive, URL: "u-m"},
	}}

	selected := selectAddons(m, nil)
	require.Equal(t, []string{"alpha", "mid", "zeta"}, []string{
		selected[0].Name,
		selected[1].Name,
		selected[2].Name,
	})
}
