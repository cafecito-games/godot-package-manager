package manifest

import (
	"os"
	"path/filepath"
	"testing"

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
