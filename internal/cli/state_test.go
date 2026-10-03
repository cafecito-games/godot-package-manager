package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/project"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
	"github.com/cafecito-games/godot-package-manager/internal/source"
)

// countingFetcher wraps fakeFetcher with a call count, so a test can assert
// that a satisfied install fetched nothing at all.
type countingFetcher struct {
	version string
	calls   *int
}

func (f countingFetcher) Fetch(ctx context.Context, spec manifest.AddonSpec) (source.FetchResult, error) {
	*f.calls++
	return fakeFetcher{version: f.version, checksum: "deadbeef"}.Fetch(ctx, spec)
}

// TestUnslicedAddonsAreUnaffectedByTheSelectionMode pins that the modes are
// no-ops for an addon that publishes no index: the lock keeps its single archive
// checksum and no slice pins, and the state entry records only the version.
func TestUnslicedAddonsAreUnaffectedByTheSelectionMode(t *testing.T) {
	for name, mode := range map[string]slice.SelectionMode{
		"the declared-platforms default": slice.SelectDeclaredPlatforms,
		"all published slices":           slice.SelectAllPublishedSlices,
		"host only":                      slice.SelectHostOnly,
	} {
		t.Run(name, func(t *testing.T) {
			projectRoot := t.TempDir()
			lockPath := filepath.Join(projectRoot, "addons.lock")
			statePath := filepath.Join(projectRoot, project.StateFileName)
			calls := 0

			addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
				"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u", Platforms: []string{"ios.arm64"}},
			}}
			runner := &Runner{
				AddonsDir:     filepath.Join(projectRoot, "addons"),
				LockPath:      lockPath,
				StatePath:     statePath,
				SelectionMode: mode,
				FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
					return countingFetcher{version: "1.0", calls: &calls}, nil
				},
			}
			results, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Empty(t, results[0].Slices)
			require.Equal(t, 1, calls)

			lock, err := manifest.LoadLock(lockPath)
			require.NoError(t, err)
			require.Equal(t, "deadbeef", lock.Addons["dlg"].Checksum)
			require.Nil(t, lock.Addons["dlg"].Slices)
			require.Empty(t, lock.Addons["dlg"].IndexChecksum)

			state, err := manifest.LoadState(statePath)
			require.NoError(t, err)
			require.Equal(t, "1.0", state.Addons["dlg"].ResolvedVersion)
			require.Empty(t, state.Addons["dlg"].Slices)

			lockBefore := readFileBytes(t, lockPath)
			stateBefore := readFileBytes(t, statePath)
			_, err = runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
			require.NoError(t, err)
			require.Equal(t, 1, calls, "a satisfied unsliced install fetches nothing")
			require.Equal(t, lockBefore, readFileBytes(t, lockPath))
			require.Equal(t, stateBefore, readFileBytes(t, statePath))
		})
	}
}

// TestUnslicedAddonIsReMaterializedWhenTheDirectoryIsGone pins that a state
// entry never makes an absent addon look installed, which for an unsliced addon
// is the whole of the disk question.
func TestUnslicedAddonIsReMaterializedWhenTheDirectoryIsGone(t *testing.T) {
	projectRoot := t.TempDir()
	addonsDir := filepath.Join(projectRoot, "addons")
	calls := 0
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}
	runner := &Runner{
		AddonsDir: addonsDir,
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		StatePath: filepath.Join(projectRoot, project.StateFileName),
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return countingFetcher{version: "1.0", calls: &calls}, nil
		},
	}
	_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)
	require.Equal(t, 1, calls)

	require.NoError(t, os.RemoveAll(filepath.Join(addonsDir, "dlg")))
	_, err = runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	_, err = os.Stat(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
}

// TestUnslicedAddonIsReMaterializedWhenAnInstalledFileIsGone covers the
// intra-addon damage case: an intact top-level directory must not let a deleted
// file satisfy a locked install.
func TestUnslicedAddonIsReMaterializedWhenAnInstalledFileIsGone(t *testing.T) {
	projectRoot := t.TempDir()
	addonsDir := filepath.Join(projectRoot, "addons")
	calls := 0
	var diagnostics string
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}
	runner := &Runner{
		AddonsDir: addonsDir,
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		StatePath: filepath.Join(projectRoot, project.StateFileName),
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return countingFetcher{version: "1.0", calls: &calls}, nil
		},
		Diagnosef: func(format string, args ...any) {
			diagnostics += fmt.Sprintf(format, args...)
		},
	}

	_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)
	require.Equal(t, 1, calls)

	pluginPath := filepath.Join(addonsDir, "dlg", "plugin.cfg")
	require.NoError(t, os.Remove(pluginPath))
	_, err = runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "the deleted file must invalidate the on-disk fast path")
	require.FileExists(t, pluginPath)
	require.Contains(t, diagnostics, `addon "dlg"`)
	require.Contains(t, diagnostics, "plugin.cfg")
}

// TestInPlaceFileEditsDoNotInvalidateAnInstall pins the chosen completeness
// granularity. The state records required paths, not content digests or a ban on
// additional files, so addon development in the game checkout stays untouched.
func TestInPlaceFileEditsDoNotInvalidateAnInstall(t *testing.T) {
	projectRoot := t.TempDir()
	addonsDir := filepath.Join(projectRoot, "addons")
	calls := 0
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}
	runner := &Runner{
		AddonsDir: addonsDir,
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		StatePath: filepath.Join(projectRoot, project.StateFileName),
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return countingFetcher{version: "1.0", calls: &calls}, nil
		},
	}
	_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)

	pluginPath := filepath.Join(addonsDir, "dlg", "plugin.cfg")
	require.NoError(t, os.WriteFile(pluginPath, []byte("intentional edit"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(addonsDir, "dlg", "local.gd"), []byte("local addition"), 0o644))
	for range 2 {
		_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
		require.NoError(t, err)
	}
	require.Equal(t, 1, calls, "content edits and extra files must not cause a re-fetch loop")
	require.Equal(t, "intentional edit", string(readFileBytes(t, pluginPath)))
	require.FileExists(t, filepath.Join(addonsDir, "dlg", "local.gd"))
}

func TestLegacyStateWithoutAFileManifestReMaterializesOnce(t *testing.T) {
	projectRoot := t.TempDir()
	statePath := filepath.Join(projectRoot, project.StateFileName)
	calls := 0
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}
	runner := &Runner{
		AddonsDir: filepath.Join(projectRoot, "addons"),
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		StatePath: statePath,
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return countingFetcher{version: "1.0", calls: &calls}, nil
		},
	}
	_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)

	state, err := manifest.LoadState(statePath)
	require.NoError(t, err)
	entry := state.Addons["dlg"]
	entry.FileManifestVersion = 0
	entry.Files = nil
	state.Addons["dlg"] = entry
	require.NoError(t, state.Save(statePath))

	_, err = runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "a legacy state entry must be refreshed to record installed files")
	_, err = runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)
	require.Equal(t, 2, calls, "the migrated state entry must satisfy later installs")
}

// TestStateIsWrittenOnlyForAddonsThatCompleted pins that an aborted run leaves
// state consistent with what is on disk: state never claims an addon whose
// install never happened.
func TestStateIsWrittenOnlyForAddonsThatCompleted(t *testing.T) {
	projectRoot := t.TempDir()
	statePath := filepath.Join(projectRoot, project.StateFileName)
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"ok":  {Name: "ok", Source: manifest.SourceArchive, URL: "u-ok"},
		"bad": {Name: "bad", Source: manifest.SourceArchive, URL: "u-bad"},
	}}
	fetchError := errors.New("fetch failed")
	runner := &Runner{
		AddonsDir: filepath.Join(projectRoot, "addons"),
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		StatePath: statePath,
		FetcherFor: func(spec manifest.AddonSpec) (source.Fetcher, error) {
			if spec.Name == "bad" {
				return nil, &output.FetchError{Err: fetchError}
			}
			return fakeFetcher{version: "1.0", checksum: "abc123"}, nil
		},
	}
	_, err := runner.InstallAddons(context.Background(), addonManifest, []string{"ok", "bad"}, ModeUpdate)
	require.ErrorIs(t, err, fetchError)

	state, err := manifest.LoadState(statePath)
	require.NoError(t, err)
	require.Contains(t, state.Addons, "ok")
	require.NotContains(t, state.Addons, "bad")
}

// TestAnEmptyStatePathDisablesTheRecord pins the degradation direction of a
// Runner built without a state path: nothing is read or written, so every addon
// is re-materialized. That costs work and can never produce an unverified
// install.
func TestAnEmptyStatePathDisablesTheRecord(t *testing.T) {
	projectRoot := t.TempDir()
	calls := 0
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}
	runner := &Runner{
		AddonsDir: filepath.Join(projectRoot, "addons"),
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return countingFetcher{version: "1.0", calls: &calls}, nil
		},
	}
	for range 2 {
		_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
		require.NoError(t, err)
	}
	require.Equal(t, 2, calls)
	entries, err := os.ReadDir(projectRoot)
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotEqual(t, project.StateFileName, entry.Name())
	}
}

// TestAFailedLockWriteLeavesNothingVouchedFor pins the recovery direction after
// a partial write. An install that put new bytes on disk and then could not
// persist the pin must not leave a state record that the old pin still matches:
// the next run would skip fetching and exit successfully with contents the lock
// does not describe.
func TestAFailedLockWriteLeavesNothingVouchedFor(t *testing.T) {
	projectRoot := t.TempDir()
	statePath := filepath.Join(projectRoot, project.StateFileName)
	addonsDir := filepath.Join(projectRoot, "addons")
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}

	// A healthy first install, so there is a state record to go stale.
	calls := 0
	runner := &Runner{
		AddonsDir: addonsDir,
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		StatePath: statePath,
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return countingFetcher{version: "1.0", calls: &calls}, nil
		},
	}
	_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)
	state, err := manifest.LoadState(statePath)
	require.NoError(t, err)
	require.Contains(t, state.Addons, "dlg")

	// An update installs new contents and then cannot persist the new pin. A
	// lock path inside a directory that does not exist loads as an absent
	// lockfile and fails to save, without depending on the suite's effective
	// user as a mode-based test would.
	broken := &Runner{
		AddonsDir: addonsDir,
		LockPath:  filepath.Join(projectRoot, "absent", "addons.lock"),
		StatePath: statePath,
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return fakeFetcher{version: "2.0", checksum: "cafebabe"}, nil
		},
	}
	_, err = broken.InstallAddons(context.Background(), addonManifest, nil, ModeUpdate)
	require.Error(t, err)

	state, err = manifest.LoadState(statePath)
	require.NoError(t, err)
	require.NotContains(t, state.Addons, "dlg",
		"a run that changed the disk and could not persist the pin must vouch for nothing")

	// With the filesystem healthy again, the addon is re-materialized rather
	// than assumed current: the lock still pins the old version while the disk
	// holds the new bytes, and only the dropped state record forces the fetch.
	callsBefore := calls
	_, err = runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.NoError(t, err)
	require.Equal(t, callsBefore+1, calls)
}

// TestAFailedStateInvalidationRemovesTheFetchedTree pins that the fetched tree
// is removed on every path out of the loop, as the Fetcher contract requires of
// its caller. A merged slice set or a git checkout can be large, and leaving one
// in the system temporary directory is a leak the user never sees.
func TestAFailedStateInvalidationRemovesTheFetchedTree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not constrain root")
	}
	projectRoot := t.TempDir()
	stateDir := filepath.Join(projectRoot, "state")
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	statePath := filepath.Join(stateDir, project.StateFileName)

	// A record to invalidate, and then a directory the atomic state write
	// cannot create its temporary file in.
	seed := &manifest.State{Addons: map[string]manifest.StateEntry{
		"dlg": {ResolvedVersion: "1.0", Pin: "deadbeef"},
	}}
	require.NoError(t, seed.Save(statePath))
	require.NoError(t, os.Chmod(stateDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o755) })

	var dirs []string
	runner := &Runner{
		AddonsDir: filepath.Join(projectRoot, "addons"),
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		StatePath: statePath,
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return slicePinFetcher{dirs: &dirs, result: source.FetchResult{
				ResolvedVersion: "2.0",
				Checksum:        "cafebabe",
			}}, nil
		},
	}
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}
	_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeUpdate)
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.ErrorAs(t, err, &manifestError)
	require.Len(t, dirs, 1)
	_, statErr := os.Stat(dirs[0])
	require.True(t, os.IsNotExist(statErr), "the fetched tree must not survive a failed state write")
}
