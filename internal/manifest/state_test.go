package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestLoadStateMissingIsEmpty(t *testing.T) {
	state, err := LoadState(filepath.Join(t.TempDir(), "nope.toml"))
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Empty(t, state.Addons)
}

func TestStateRoundTrip(t *testing.T) {
	state := &State{Addons: map[string]StateEntry{
		"limboai": {ResolvedVersion: "v1.4.0", Slices: []string{"core", "macos", "windows.x86_64"}},
		"gut":     {ResolvedVersion: "aa0d7a1dd5bcf76f6bbe7abd8a1cfd62d1b1eecb"},
	}}
	path := filepath.Join(t.TempDir(), ".gpm-state.toml")
	require.NoError(t, state.Save(path))

	got, err := LoadState(path)
	require.NoError(t, err)
	require.Equal(t, state.Addons, got.Addons)
}

func TestStateRoundTripPreservesTheInstalledFileManifest(t *testing.T) {
	state := &State{Addons: map[string]StateEntry{
		"dlg": {
			ResolvedVersion:     "1.0.0",
			FileManifestVersion: CurrentFileManifestVersion,
			Files:               []string{"plugin.cfg", "scripts/main.gd"},
		},
	}}
	path := filepath.Join(t.TempDir(), ".gpm-state.toml")
	require.NoError(t, state.Save(path))

	got, err := LoadState(path)
	require.NoError(t, err)
	require.Equal(t, state.Addons, got.Addons)
}

// TestLoadStateCorruptIsEmptyAndNonFatal pins the one deliberate fail-open in
// this package: the state file is a machine-local cache rebuildable from
// addons.lock plus addons/, so a corrupt one must never be able to abort a run.
func TestLoadStateCorruptIsEmptyAndNonFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gpm-state.toml")
	require.NoError(t, os.WriteFile(path, []byte("not = = valid"), 0o644))

	state, err := LoadState(path)
	require.Error(t, err)
	require.NotNil(t, state)
	require.Empty(t, state.Addons)

	var corruptErr *CorruptStateError
	require.ErrorAs(t, err, &corruptErr)
	require.Equal(t, path, corruptErr.Path)

	var manifestErr *output.ManifestError
	require.False(t, errors.As(err, &manifestErr), "a corrupt state cache must not be able to become exit code 3")
	require.Equal(t, output.ExitGeneric, output.CodeFor(err))
}

func TestLoadStateUnreadableIsAnError(t *testing.T) {
	requireNonRoot(t)
	dir := t.TempDir()
	path := filepath.Join(dir, ".gpm-state.toml")
	require.NoError(t, os.WriteFile(path, []byte("[addons]\n"), 0o200))

	state, err := LoadState(path)
	require.Error(t, err)
	require.Nil(t, state)
	var corruptErr *CorruptStateError
	require.False(t, errors.As(err, &corruptErr), "an unreadable file is an environment fault, not a cache miss")
}

func TestStateSaveIsDeterministic(t *testing.T) {
	state := &State{Addons: map[string]StateEntry{
		"zeta": {
			ResolvedVersion:     "v1",
			Slices:              []string{"windows.x86_64", "core", "macos"},
			FileManifestVersion: CurrentFileManifestVersion,
			Files:               []string{"zeta.gd", "plugin.cfg", "scripts/main.gd"},
		},
		"alpha":   {ResolvedVersion: "v2", Slices: []string{"core"}},
		"limboai": {ResolvedVersion: "v3", Slices: []string{"ios.arm64", "core"}},
	}}
	dir := t.TempDir()
	first := filepath.Join(dir, "first.toml")
	second := filepath.Join(dir, "second.toml")
	require.NoError(t, state.Save(first))
	require.NoError(t, state.Save(second))

	firstData, err := os.ReadFile(first)
	require.NoError(t, err)
	secondData, err := os.ReadFile(second)
	require.NoError(t, err)
	require.Equal(t, string(firstData), string(secondData))

	requireAscendingOrder(t, string(firstData), []string{`[addons.alpha]`, `[addons.limboai]`, `[addons.zeta]`})
	requireAscendingOrder(t, string(firstData), []string{`"core", "macos", "windows.x86_64"`})
	requireAscendingOrder(t, string(firstData), []string{`"plugin.cfg", "scripts/main.gd", "zeta.gd"`})
}

// TestStateSaveDoesNotMutateReceiver guards that sorting for deterministic
// output happens on a copy, so a caller's in-memory slice order is untouched.
func TestStateSaveDoesNotMutateReceiver(t *testing.T) {
	state := &State{Addons: map[string]StateEntry{
		"limboai": {
			ResolvedVersion:     "v1",
			Slices:              []string{"windows.x86_64", "core"},
			FileManifestVersion: CurrentFileManifestVersion,
			Files:               []string{"z.gd", "a.gd"},
		},
	}}
	require.NoError(t, state.Save(filepath.Join(t.TempDir(), ".gpm-state.toml")))
	require.Equal(t, []string{"windows.x86_64", "core"}, state.Addons["limboai"].Slices)
	require.Equal(t, []string{"z.gd", "a.gd"}, state.Addons["limboai"].Files)
}

func TestStateSaveReplayIsAByteIdenticalNoOp(t *testing.T) {
	state := &State{Addons: map[string]StateEntry{
		"limboai": {ResolvedVersion: "v1.4.0", Slices: []string{"core", "macos"}},
	}}
	path := filepath.Join(t.TempDir(), ".gpm-state.toml")
	require.NoError(t, state.Save(path))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.NoError(t, state.Save(path))
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func TestStateSaveLeavesNothingBehindOnFailure(t *testing.T) {
	requireNonRoot(t)
	state := &State{Addons: map[string]StateEntry{
		"limboai": {ResolvedVersion: "v1.4.0", Slices: []string{"core"}},
	}}

	t.Run("no target file and no temp file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".gpm-state.toml")
		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		require.Error(t, state.Save(path))

		require.NoError(t, os.Chmod(dir, 0o700))
		require.NoFileExists(t, path)
		leftovers, err := filepath.Glob(filepath.Join(dir, ".gpm-state-*.tmp"))
		require.NoError(t, err)
		require.Empty(t, leftovers)
	})

	t.Run("pre-existing file survives byte-identical", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, ".gpm-state.toml")
		require.NoError(t, state.Save(path))
		original, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NotEmpty(t, original)

		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

		other := &State{Addons: map[string]StateEntry{
			"other": {ResolvedVersion: "v9", Slices: []string{"core", "linux.x86_64"}},
		}}
		require.Error(t, other.Save(path))

		require.NoError(t, os.Chmod(dir, 0o700))
		survivor, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, string(original), string(survivor))
		leftovers, err := filepath.Glob(filepath.Join(dir, ".gpm-state-*.tmp"))
		require.NoError(t, err)
		require.Empty(t, leftovers)
	})
}

// requireNonRoot skips a test that depends on filesystem permissions being
// enforced, which they are not for root or on Windows.
func requireNonRoot(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits are not enforced the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses the directory permission bits this test relies on")
	}
}
