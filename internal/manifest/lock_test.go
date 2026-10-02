package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestLockLoadMissingIsEmpty(t *testing.T) {
	lockfile, err := LoadLock(filepath.Join(t.TempDir(), "nope.lock"))
	require.NoError(t, err)
	require.Empty(t, lockfile.Addons)
}

func TestLockRoundTrip(t *testing.T) {
	lockfile := &Lockfile{Addons: map[string]LockEntry{
		"g": {ResolvedVersion: "abc123", SourcePath: "addons/g", SpecHash: "h1"},
	}}
	path := filepath.Join(t.TempDir(), "addons.lock")
	require.NoError(t, lockfile.Save(path))
	got, err := LoadLock(path)
	require.NoError(t, err)
	require.Equal(t, lockfile.Addons, got.Addons)
}

func TestLoadLockBadTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.lock")
	require.NoError(t, os.WriteFile(path, []byte("not = = valid"), 0o644))
	_, err := LoadLock(path)
	require.Error(t, err)
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

const (
	coreDigest     = "9f2a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90"
	iosDigest      = "41bc0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90"
	windowsDigest  = "77ae0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90"
	indexDigest    = "a0040b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f90"
	archiveDigest  = "3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b"
	specHashDigest = "7d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e"
)

func slicedEntry() LockEntry {
	return LockEntry{
		ResolvedVersion: "v1.4.0",
		SourcePath:      "",
		SpecHash:        specHashDigest,
		IndexChecksum:   indexDigest,
		Slices: map[string]string{
			"core":           coreDigest,
			"ios.arm64":      iosDigest,
			"windows.x86_64": windowsDigest,
		},
	}
}

func TestLockRoundTripSlicedEntry(t *testing.T) {
	lockfile := &Lockfile{Addons: map[string]LockEntry{"limboai": slicedEntry()}}
	path := filepath.Join(t.TempDir(), "addons.lock")
	require.NoError(t, lockfile.Save(path))

	got, err := LoadLock(path)
	require.NoError(t, err)
	require.Equal(t, lockfile.Addons, got.Addons)
}

func TestLockRoundTripUnslicedEntryOmitsSliceKeys(t *testing.T) {
	lockfile := &Lockfile{Addons: map[string]LockEntry{"dialogue_manager": {
		ResolvedVersion: "v2.44.0",
		SourcePath:      "addons/dialogue_manager",
		Checksum:        archiveDigest,
		SpecHash:        specHashDigest,
	}}}
	path := filepath.Join(t.TempDir(), "addons.lock")
	require.NoError(t, lockfile.Save(path))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "slices")
	require.NotContains(t, string(data), "index_sha256")

	got, err := LoadLock(path)
	require.NoError(t, err)
	require.Equal(t, lockfile.Addons, got.Addons)
	require.Nil(t, got.Addons["dialogue_manager"].Slices)
	require.Empty(t, got.Addons["dialogue_manager"].IndexChecksum)
	require.NoError(t, got.Validate())
}

// TestLoadLockAcceptsRealProducerFixture guards compatibility with lockfiles
// written before slice support existed. testdata/unsliced-addons.lock was
// produced by Lockfile.Save itself, so a change to the schema that would reject
// or rewrite an existing lockfile fails here.
func TestLoadLockAcceptsRealProducerFixture(t *testing.T) {
	original, err := os.ReadFile(filepath.Join("testdata", "unsliced-addons.lock"))
	require.NoError(t, err)

	lockfile, err := LoadLock(filepath.Join("testdata", "unsliced-addons.lock"))
	require.NoError(t, err)
	require.Len(t, lockfile.Addons, 2)
	require.Nil(t, lockfile.Addons["dialogue_manager"].Slices)
	require.Empty(t, lockfile.Addons["gut"].IndexChecksum)

	resaved := filepath.Join(t.TempDir(), "addons.lock")
	require.NoError(t, lockfile.Save(resaved))
	data, err := os.ReadFile(resaved)
	require.NoError(t, err)
	require.Equal(t, string(original), string(data))
}

func TestLockfileValidate(t *testing.T) {
	tests := []struct {
		name    string
		entry   LockEntry
		wantErr string
	}{
		{
			name:  "unsliced entry with checksum",
			entry: LockEntry{ResolvedVersion: "v1", SourcePath: "addons/x", Checksum: archiveDigest, SpecHash: specHashDigest},
		},
		{
			name:  "unsliced git entry without checksum",
			entry: LockEntry{ResolvedVersion: "abc123", SourcePath: "addons/x", SpecHash: specHashDigest},
		},
		{
			name:  "sliced entry with core and platform slices",
			entry: slicedEntry(),
		},
		{
			name: "sliced entry without index checksum",
			entry: LockEntry{
				ResolvedVersion: "v1",
				SpecHash:        specHashDigest,
				Slices:          map[string]string{"core": coreDigest},
			},
			wantErr: "index_sha256",
		},
		{
			name: "sliced entry missing the core slice",
			entry: LockEntry{
				ResolvedVersion: "v1",
				SpecHash:        specHashDigest,
				IndexChecksum:   indexDigest,
				Slices:          map[string]string{"ios.arm64": iosDigest},
			},
			wantErr: `must record the "core" slice`,
		},
		{
			name: "sliced entry with an unparseable slice key",
			entry: LockEntry{
				ResolvedVersion: "v1",
				SpecHash:        specHashDigest,
				IndexChecksum:   indexDigest,
				Slices:          map[string]string{"core": coreDigest, "plan9.arm64": iosDigest},
			},
			wantErr: "plan9",
		},
		{
			name: "sliced entry with a non-hex slice checksum",
			entry: LockEntry{
				ResolvedVersion: "v1",
				SpecHash:        specHashDigest,
				IndexChecksum:   indexDigest,
				Slices:          map[string]string{"core": "deadbeef"},
			},
			wantErr: "64 lowercase hex digits",
		},
		{
			name: "sliced entry with a non-hex index checksum",
			entry: LockEntry{
				ResolvedVersion: "v1",
				SpecHash:        specHashDigest,
				IndexChecksum:   "NOTHEX",
				Slices:          map[string]string{"core": coreDigest},
			},
			wantErr: "64 lowercase hex digits",
		},
		{
			name: "entry with both checksum and slices",
			entry: LockEntry{
				ResolvedVersion: "v1",
				SpecHash:        specHashDigest,
				Checksum:        archiveDigest,
				IndexChecksum:   indexDigest,
				Slices:          map[string]string{"core": coreDigest},
			},
			wantErr: "must not set both",
		},
		{
			name: "entry with an index checksum but no slices",
			entry: LockEntry{
				ResolvedVersion: "v1",
				SpecHash:        specHashDigest,
				IndexChecksum:   indexDigest,
			},
			wantErr: "index_sha256",
		},
		{
			name: "entry with an empty slices table",
			entry: LockEntry{
				ResolvedVersion: "v1",
				SpecHash:        specHashDigest,
				IndexChecksum:   indexDigest,
				Slices:          map[string]string{},
			},
			wantErr: `must record the "core" slice`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lockfile := &Lockfile{Addons: map[string]LockEntry{"limboai": test.entry}}
			err := lockfile.Validate()
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
			require.Equal(t, output.ExitManifest, output.CodeFor(err))
		})
	}
}

func TestLoadLockRejectsInvalidEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addons.lock")
	contents := `[addons]
  [addons.limboai]
    resolved_version = "v1.4.0"
    spec_hash = "` + specHashDigest + `"
    [addons.limboai.slices]
      core = "` + coreDigest + `"
`
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))

	_, err := LoadLock(path)
	require.ErrorContains(t, err, "index_sha256")
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
}

func TestLockfileSaveIsDeterministic(t *testing.T) {
	lockfile := &Lockfile{Addons: map[string]LockEntry{
		"zeta":    slicedEntry(),
		"alpha":   slicedEntry(),
		"limboai": slicedEntry(),
	}}
	dir := t.TempDir()
	first := filepath.Join(dir, "first.lock")
	second := filepath.Join(dir, "second.lock")
	require.NoError(t, lockfile.Save(first))
	require.NoError(t, lockfile.Save(second))

	firstData, err := os.ReadFile(first)
	require.NoError(t, err)
	secondData, err := os.ReadFile(second)
	require.NoError(t, err)
	require.Equal(t, string(firstData), string(secondData))

	addonOrder := []string{`[addons.alpha]`, `[addons.limboai]`, `[addons.zeta]`}
	requireAscendingOrder(t, string(firstData), addonOrder)
	sliceOrder := []string{`core =`, `"ios.arm64" =`, `"windows.x86_64" =`}
	requireAscendingOrder(t, string(firstData), sliceOrder)
}

// requireAscendingOrder asserts that every needle occurs in contents and that
// they occur in the given order, which pins the serializer's key sort.
func requireAscendingOrder(t *testing.T, contents string, needles []string) {
	t.Helper()
	previous := -1
	for _, needle := range needles {
		index := strings.Index(contents, needle)
		require.GreaterOrEqual(t, index, 0, "expected %q in:\n%s", needle, contents)
		require.Greater(t, index, previous, "expected %q after the preceding key in:\n%s", needle, contents)
		previous = index
	}
}
