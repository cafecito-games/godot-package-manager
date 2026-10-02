package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
	"github.com/cafecito-games/godot-package-manager/internal/source"
)

// checksumOf builds a distinct, well-formed SHA-256 for a test pin.
func checksumOf(marker byte) string {
	return strings.Repeat(string(marker), 64)
}

func sliceIDsOf(t *testing.T, ids ...string) []slice.SliceID {
	t.Helper()
	parsed := make([]slice.SliceID, 0, len(ids))
	for _, id := range ids {
		value, err := slice.ParseSliceID(id)
		require.NoError(t, err)
		parsed = append(parsed, value)
	}
	return parsed
}

func sliceIDTags(ids []slice.SliceID) []string {
	tags := make([]string, 0, len(ids))
	for _, id := range ids {
		tags = append(tags, id.String())
	}
	return tags
}

// reconcileScenario is one machine's worth of state: a lock pin, a state record,
// and whether the install directory exists.
type reconcileScenario struct {
	addonsDir string
	spec      manifest.AddonSpec
	lock      *manifest.Lockfile
	state     *manifest.State
}

func newReconcileScenario(t *testing.T) *reconcileScenario {
	t.Helper()
	addonsDir := filepath.Join(t.TempDir(), "addons")
	require.NoError(t, os.MkdirAll(filepath.Join(addonsDir, "sliced"), 0o755))
	return &reconcileScenario{
		addonsDir: addonsDir,
		spec:      manifest.AddonSpec{Name: "sliced", Source: manifest.SourceArchive, URL: "u"},
		lock: &manifest.Lockfile{Addons: map[string]manifest.LockEntry{
			"sliced": {ResolvedVersion: "1.2.3", SpecHash: "hash", IndexChecksum: checksumOf('a'), Slices: map[string]string{
				"core":           checksumOf('b'),
				"macos":          checksumOf('c'),
				"linux.x86_64":   checksumOf('d'),
				"windows.x86_64": checksumOf('e'),
			}},
		}},
		state: &manifest.State{Addons: map[string]manifest.StateEntry{
			"sliced": {ResolvedVersion: "1.2.3", Slices: []string{"core", "macos"}},
		}},
	}
}

func (scenario *reconcileScenario) missing(t *testing.T, needed ...string) []string {
	t.Helper()
	return sliceIDTags(missingSlicesOnDisk(
		scenario.spec, scenario.lock, scenario.state, sliceIDsOf(t, needed...), scenario.addonsDir))
}

// TestMissingSlicesOnDiskReportsNothingWhenStateCoversTheNeededSet is the
// baseline the four "everything is missing" triggers are measured against.
func TestMissingSlicesOnDiskReportsNothingWhenStateCoversTheNeededSet(t *testing.T) {
	scenario := newReconcileScenario(t)
	require.Empty(t, scenario.missing(t, "core", "macos"))
}

func TestMissingSlicesOnDiskReportsThePartialMiss(t *testing.T) {
	scenario := newReconcileScenario(t)
	require.Equal(t, []string{"linux.x86_64"}, scenario.missing(t, "core", "macos", "linux.x86_64"),
		"only the needed slices this machine has not materialized are reported")
}

func TestMissingSlicesOnDiskReportsEverythingWhenTheInstallDirectoryIsAbsent(t *testing.T) {
	scenario := newReconcileScenario(t)
	require.NoError(t, os.RemoveAll(filepath.Join(scenario.addonsDir, "sliced")))
	require.Equal(t, []string{"core", "macos"}, scenario.missing(t, "core", "macos"),
		"a state entry never makes an absent addon look installed")
}

func TestMissingSlicesOnDiskReportsEverythingWhenStateHasNoEntry(t *testing.T) {
	scenario := newReconcileScenario(t)
	scenario.state = &manifest.State{Addons: map[string]manifest.StateEntry{}}
	require.Equal(t, []string{"core", "macos"}, scenario.missing(t, "core", "macos"))
}

// TestMissingSlicesOnDiskReportsEverythingWithoutAStateFile covers the
// teammate who clones a repository whose lock is perfectly consistent: LoadState
// hands back an empty State for a file that is not there.
func TestMissingSlicesOnDiskReportsEverythingWithoutAStateFile(t *testing.T) {
	scenario := newReconcileScenario(t)
	loaded, err := manifest.LoadState(filepath.Join(t.TempDir(), "absent.toml"))
	require.NoError(t, err)
	scenario.state = loaded
	require.Equal(t, []string{"core", "macos"}, scenario.missing(t, "core", "macos"))
}

func TestMissingSlicesOnDiskReportsEverythingWhenStateRecordsAnotherVersion(t *testing.T) {
	scenario := newReconcileScenario(t)
	scenario.state.Addons["sliced"] = manifest.StateEntry{
		ResolvedVersion: "1.0.0",
		Slices:          []string{"core", "macos"},
	}
	require.Equal(t, []string{"core", "macos"}, scenario.missing(t, "core", "macos"),
		"a state entry recording a different version records nothing usable")
}

// TestVerifyChecksumComparesTheIndexPin is the comparison this issue exists for.
// A Fetcher never sees the lockfile, so without it a retagged release could
// serve a different index naming different archives and install successfully
// with addons.toml and addons.lock both unchanged.
func TestVerifyChecksumComparesTheIndexPin(t *testing.T) {
	spec := manifest.AddonSpec{Name: "sliced", Source: manifest.SourceArchive, URL: "u"}
	lock := &manifest.Lockfile{Addons: map[string]manifest.LockEntry{
		"sliced": {IndexChecksum: checksumOf('a'), Slices: map[string]string{"core": checksumOf('b')}},
	}}
	fetched := source.FetchResult{
		IndexChecksum: checksumOf('f'),
		PublishedSlices: []source.SliceResult{
			{ID: slice.CoreSliceID(), Checksum: checksumOf('b')},
		},
	}

	err := verifyChecksum(spec, lock, fetched, true)
	require.Error(t, err)
	var fetchError *output.FetchError
	require.True(t, errors.As(err, &fetchError))
	require.Equal(t, output.ExitFetch, output.CodeFor(err))
	require.Contains(t, err.Error(), slice.IndexFileName)

	require.NoError(t, verifyChecksum(spec, lock, fetched, false),
		"with no honored pin there is nothing to compare against")
}

func TestVerifyChecksumComparesEveryPublishedSlicePin(t *testing.T) {
	spec := manifest.AddonSpec{Name: "sliced", Source: manifest.SourceArchive, URL: "u"}
	pinned := map[string]string{"core": checksumOf('b'), "macos": checksumOf('c')}
	lock := &manifest.Lockfile{Addons: map[string]manifest.LockEntry{
		"sliced": {IndexChecksum: checksumOf('a'), Slices: pinned},
	}}

	rows := map[string]struct {
		published []source.SliceResult
		expected  string
	}{
		"a slice whose checksum changed": {
			published: []source.SliceResult{
				{ID: slice.CoreSliceID(), Checksum: checksumOf('b')},
				{ID: slice.SliceID{Platform: "macos"}, Checksum: checksumOf('9')},
			},
			expected: `slice "macos" checksum mismatch`,
		},
		"a slice the lock does not pin": {
			published: []source.SliceResult{
				{ID: slice.CoreSliceID(), Checksum: checksumOf('b')},
				{ID: slice.SliceID{Platform: "macos"}, Checksum: checksumOf('c')},
				{ID: slice.SliceID{Platform: "linux", Architecture: "x86_64"}, Checksum: checksumOf('d')},
			},
			expected: "publishes slices the lock does not pin: linux.x86_64",
		},
		"a pinned slice that is gone": {
			published: []source.SliceResult{
				{ID: slice.CoreSliceID(), Checksum: checksumOf('b')},
			},
			expected: "no longer publishes slices the lock pins: macos",
		},
	}
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			fetched := source.FetchResult{IndexChecksum: checksumOf('a'), PublishedSlices: row.published}
			err := verifyChecksum(spec, lock, fetched, true)
			require.Error(t, err)
			var fetchError *output.FetchError
			require.True(t, errors.As(err, &fetchError))
			require.Equal(t, output.ExitFetch, output.CodeFor(err))
			require.Contains(t, err.Error(), row.expected)
		})
	}

	t.Run("a matching published set", func(t *testing.T) {
		fetched := source.FetchResult{IndexChecksum: checksumOf('a'), PublishedSlices: []source.SliceResult{
			{ID: slice.CoreSliceID(), Checksum: checksumOf('b')},
			{ID: slice.SliceID{Platform: "macos"}, Checksum: checksumOf('c')},
		}}
		require.NoError(t, verifyChecksum(spec, lock, fetched, true))
	})
}

// TestVerifyChecksumRejectsSlicedAgainstUnslicedPins covers the exclusivity the
// lockfile already validates: an addon is either sliced or it is not, and a pin
// of one kind must never be silently satisfied by a fetch of the other.
func TestVerifyChecksumRejectsSlicedAgainstUnslicedPins(t *testing.T) {
	spec := manifest.AddonSpec{Name: "sliced", Source: manifest.SourceArchive, URL: "u"}

	t.Run("the lock pins an archive and the fetch published slices", func(t *testing.T) {
		lock := &manifest.Lockfile{Addons: map[string]manifest.LockEntry{
			"sliced": {Checksum: checksumOf('b')},
		}}
		fetched := source.FetchResult{IndexChecksum: checksumOf('a'), PublishedSlices: []source.SliceResult{
			{ID: slice.CoreSliceID(), Checksum: checksumOf('b')},
		}}
		err := verifyChecksum(spec, lock, fetched, true)
		require.Equal(t, output.ExitFetch, output.CodeFor(err))
		require.Contains(t, err.Error(), "the lock pins a single archive")
	})

	t.Run("the lock pins slices and the fetch published none", func(t *testing.T) {
		lock := &manifest.Lockfile{Addons: map[string]manifest.LockEntry{
			"sliced": {IndexChecksum: checksumOf('a'), Slices: map[string]string{"core": checksumOf('b')}},
		}}
		fetched := source.FetchResult{Checksum: checksumOf('b')}
		err := verifyChecksum(spec, lock, fetched, true)
		require.Equal(t, output.ExitFetch, output.CodeFor(err))
		require.Contains(t, err.Error(), "published no slices")
	})

	t.Run("the fetch reports both an archive checksum and a slice set", func(t *testing.T) {
		lock := &manifest.Lockfile{Addons: map[string]manifest.LockEntry{}}
		fetched := source.FetchResult{Checksum: checksumOf('b'), PublishedSlices: []source.SliceResult{
			{ID: slice.CoreSliceID(), Checksum: checksumOf('b')},
		}}
		err := verifyChecksum(spec, lock, fetched, false)
		require.Equal(t, output.ExitFetch, output.CodeFor(err))
		require.Contains(t, err.Error(), "either sliced or it is not")
	})
}

// slicePinFetcher reports a fixed sliced FetchResult over a real temp directory,
// so a test can assert that a verification failure leaves nothing behind.
type slicePinFetcher struct {
	result source.FetchResult
	dirs   *[]string
}

func (f slicePinFetcher) Fetch(_ context.Context, _ manifest.AddonSpec) (source.FetchResult, error) {
	dir, err := os.MkdirTemp("", "slice-pin-*")
	if err != nil {
		return source.FetchResult{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.cfg"), []byte("[plugin]"), 0o644); err != nil {
		return source.FetchResult{}, err
	}
	*f.dirs = append(*f.dirs, dir)
	result := f.result
	result.Dir = dir
	return result, nil
}

// TestRunnerRemovesTheFetchedTreeOnAPinMismatch pins that a failed slice
// verification leaves no staging directory behind, and that it does so in every
// selection mode: the whole published set is verified even when host-only
// materializes core alone.
func TestRunnerRemovesTheFetchedTreeOnAPinMismatch(t *testing.T) {
	for name, mode := range map[string]slice.SelectionMode{
		"the declared-platforms default": slice.SelectDeclaredPlatforms,
		"all published slices":           slice.SelectAllPublishedSlices,
		"host only":                      slice.SelectHostOnly,
	} {
		t.Run(name, func(t *testing.T) {
			withHost(t, hostA)
			projectRoot := t.TempDir()
			lockPath := filepath.Join(projectRoot, "addons.lock")
			statePath := filepath.Join(projectRoot, ".gpm-state.toml")

			addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
				"sliced": {Name: "sliced", Source: manifest.SourceArchive, URL: "u", Platforms: []string{"windows.x86_64"}},
			}}
			spec := addonManifest.Addons["sliced"]
			lock := &manifest.Lockfile{Addons: map[string]manifest.LockEntry{
				"sliced": {
					ResolvedVersion: "1.2.3",
					SpecHash:        spec.Hash(),
					IndexChecksum:   checksumOf('a'),
					Slices: map[string]string{
						"core":           checksumOf('b'),
						"macos":          checksumOf('c'),
						"windows.x86_64": checksumOf('d'),
					},
				},
			}}
			require.NoError(t, lock.Save(lockPath))

			var dirs []string
			runner := &Runner{
				AddonsDir:     filepath.Join(projectRoot, "addons"),
				LockPath:      lockPath,
				StatePath:     statePath,
				SelectionMode: mode,
				FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
					return slicePinFetcher{dirs: &dirs, result: source.FetchResult{
						ResolvedVersion: "1.2.3",
						IndexChecksum:   checksumOf('a'),
						PublishedSlices: []source.SliceResult{
							{ID: slice.CoreSliceID(), Checksum: checksumOf('b')},
							{ID: slice.SliceID{Platform: "macos"}, Checksum: checksumOf('c')},
							// The slice host-only never materializes, retagged.
							{ID: slice.SliceID{Platform: "windows", Architecture: "x86_64"}, Checksum: checksumOf('9')},
						},
						InstalledSlices: sliceIDsOf(t, "core", "macos"),
					}}, nil
				},
			}
			_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
			require.Error(t, err)
			require.Equal(t, output.ExitFetch, output.CodeFor(err))
			require.Contains(t, err.Error(), "windows.x86_64")
			require.Len(t, dirs, 1)
			_, statErr := os.Stat(dirs[0])
			require.True(t, os.IsNotExist(statErr), "the fetched tree must not survive a verification failure")
		})
	}
}

// TestRunnerReportsAnUnreadableStateFile pins that an environment fault is not
// treated as a cache miss: a state path that cannot be read is exit 3, while an
// unparseable one is merely reported.
func TestRunnerReportsAnUnreadableStateFile(t *testing.T) {
	projectRoot := t.TempDir()
	// A directory where the state file belongs is unreadable as a file without
	// depending on the suite's effective user, which a mode-based test would.
	statePath := filepath.Join(projectRoot, ".gpm-state.toml")
	require.NoError(t, os.MkdirAll(statePath, 0o755))

	runner := &Runner{
		AddonsDir: filepath.Join(projectRoot, "addons"),
		LockPath:  filepath.Join(projectRoot, "addons.lock"),
		StatePath: statePath,
		FetcherFor: func(manifest.AddonSpec) (source.Fetcher, error) {
			return nil, errors.New("must not be reached")
		},
	}
	addonManifest := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{
		"dlg": {Name: "dlg", Source: manifest.SourceArchive, URL: "u"},
	}}
	_, err := runner.InstallAddons(context.Background(), addonManifest, nil, ModeInstall)
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.True(t, errors.As(err, &manifestError))
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
}
