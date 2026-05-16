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
