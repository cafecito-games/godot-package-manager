package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/project"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// executeGPM runs the CLI through its real root command so flag parsing, error
// wrapping, and output routing are all exercised, and returns the error the
// process would have mapped to an exit code.
func executeGPM(t *testing.T, stdout, stderr io.Writer, args ...string) error {
	t.Helper()
	cmd := NewRootCommand()
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func lockBytesOf(t *testing.T, projectRoot string) []byte {
	t.Helper()
	return readFileBytes(t, filepath.Join(projectRoot, "addons.lock"))
}

func stateBytesOf(t *testing.T, projectRoot string) []byte {
	t.Helper()
	return readFileBytes(t, filepath.Join(projectRoot, project.StateFileName))
}

func installedSlicesOf(t *testing.T, projectRoot string) []string {
	t.Helper()
	state, err := manifest.LoadState(filepath.Join(projectRoot, project.StateFileName))
	require.NoError(t, err)
	return state.Addons[slicedAddonName].Slices
}

// TestSlicedInstallMaterializesTheDeclaredAndHostSlices covers the shipping
// default: a project that declares iOS gets iOS, the host's own slice so the
// editor runs, and core — and nothing else.
func TestSlicedInstallMaterializesTheDeclaredAndHostSlices(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))

	files := installedFiles(t, projectRoot)
	require.Contains(t, files, "bin/addon_ios.dylib")
	require.Contains(t, files, "bin/addon_macos.dylib")
	require.NotContains(t, files, "bin/addon_windows.dll")
	require.NotContains(t, files, "bin/addon_linux.so")
	require.Equal(t, []string{"core", "ios.arm64", "macos"}, installedSlicesOf(t, projectRoot))

	extension := string(readFileBytes(t,
		filepath.Join(projectRoot, "addons", slicedAddonName, slicedExtensionPath)))
	require.Contains(t, extension, "addon_ios.dylib")
	require.Contains(t, extension, "addon_macos.dylib")
	require.NotContains(t, extension, "addon_windows.dll",
		"the reassembled .gdextension must list exactly what is on disk")
	require.NotContains(t, extension, "addon_linux.so")
}

// TestSlicedInstallOnAnIOSOnlyProject is the same case on a machine the addon
// publishes nothing for, so the installed tree is the declared platform and core
// alone.
func TestSlicedInstallOnAnIOSOnlyProject(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, unpublishedHost)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))

	files := installedFiles(t, projectRoot)
	require.Contains(t, files, "bin/addon_ios.dylib")
	require.NotContains(t, files, "bin/addon_windows.dll")
	require.NotContains(t, files, "bin/addon_linux.so")
	require.NotContains(t, files, "bin/addon_macos.dylib")
	require.Equal(t, []string{"core", "ios.arm64"}, installedSlicesOf(t, projectRoot))
}

// TestSlicedInstallIsIdempotentInEverySelectionMode pins that a satisfied
// install fetches nothing at all and writes no diff, separately for each mode.
func TestSlicedInstallIsIdempotentInEverySelectionMode(t *testing.T) {
	for name, args := range selectionModeArguments() {
		t.Run(name, func(t *testing.T) {
			withEnvironment(t, nil)
			withHost(t, hostA)
			publisher := servePublisher(t, packSlicedFixture(t, "one"))
			projectRoot := newSlicedProject(t, publisher, "ios.arm64")

			require.NoError(t, executeGPM(t, io.Discard, io.Discard,
				append([]string{"install", "--dir", projectRoot}, args...)...))
			requests := publisher.requestCount()
			require.Greater(t, requests, 0, "the first install must fetch")
			lockBefore := lockBytesOf(t, projectRoot)
			stateBefore := stateBytesOf(t, projectRoot)

			require.NoError(t, executeGPM(t, io.Discard, io.Discard,
				append([]string{"install", "--dir", projectRoot}, args...)...))

			require.Equal(t, requests, publisher.requestCount(),
				"a second install with nothing changed must fetch nothing")
			require.Equal(t, lockBefore, lockBytesOf(t, projectRoot))
			require.Equal(t, stateBefore, stateBytesOf(t, projectRoot))
		})
	}
}

// selectionModeArguments is the three selection modes as command lines.
func selectionModeArguments() map[string][]string {
	return map[string][]string{
		"the declared-platforms default": nil,
		"--all-platforms":                {"--all-platforms"},
		"--host-only":                    {"--host-only"},
	}
}

// TestSlicedLockIsIdenticalAcrossSelectionModes is the invariant that makes
// host-only safe to use on a shared repository: the lock records what the index
// publishes, not what this disk took, so toggling a mode never dirties it.
func TestSlicedLockIsIdenticalAcrossSelectionModes(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	fixture := packSlicedFixture(t, "one")
	publisher := servePublisher(t, fixture)

	locks := map[string][]byte{}
	states := map[string]string{}
	for name, args := range selectionModeArguments() {
		projectRoot := newSlicedProject(t, publisher, "ios.arm64")
		require.NoError(t, executeGPM(t, io.Discard, io.Discard,
			append([]string{"install", "--dir", projectRoot}, args...)...))
		locks[name] = lockBytesOf(t, projectRoot)
		states[name] = string(stateBytesOf(t, projectRoot))
	}

	reference := locks["the declared-platforms default"]
	for name, lock := range locks {
		require.Equal(t, string(reference), string(lock),
			"addons.lock must be byte-identical in %s", name)
	}
	require.NotEqual(t, states["the declared-platforms default"], states["--host-only"],
		"only .gpm-state.toml varies with the selection mode")
	require.NotEqual(t, states["the declared-platforms default"], states["--all-platforms"])
}

// TestSlicedInstallAcrossSimulatedHosts is the acceptance gate for the whole
// design: a Linux teammate on a repository last installed from a Mac has a
// perfectly consistent lock and an addons/ missing their host slice, and the
// install must still materialize it without touching addons.lock.
func TestSlicedInstallAcrossSimulatedHosts(t *testing.T) {
	for name, args := range map[string][]string{
		"the declared-platforms default": nil,
		"--host-only":                    {"--host-only"},
	} {
		t.Run(name, func(t *testing.T) {
			withEnvironment(t, nil)
			publisher := servePublisher(t, packSlicedFixture(t, "one"))
			projectRoot := newSlicedProject(t, publisher, "ios.arm64")

			withHost(t, hostA)
			require.NoError(t, executeGPM(t, io.Discard, io.Discard,
				append([]string{"install", "--dir", projectRoot}, args...)...))
			lockOnHostA := lockBytesOf(t, projectRoot)
			stateOnHostA := stateBytesOf(t, projectRoot)
			require.Contains(t, installedFiles(t, projectRoot), "bin/addon_macos.dylib")
			require.Contains(t, installedSlicesOf(t, projectRoot), "macos")

			// The same checkout, the same committed lock, a different machine.
			withHost(t, hostB)
			require.NoError(t, executeGPM(t, io.Discard, io.Discard,
				append([]string{"install", "--dir", projectRoot}, args...)...))

			require.Equal(t, string(lockOnHostA), string(lockBytesOf(t, projectRoot)),
				"addons.lock is committed and must be byte-identical on both hosts")
			require.NotEqual(t, string(stateOnHostA), string(stateBytesOf(t, projectRoot)),
				"only the machine-local state differs between the two hosts")
			files := installedFiles(t, projectRoot)
			require.Contains(t, files, "bin/addon_linux.so", "the second host's slice must be materialized")
			require.NotContains(t, files, "bin/addon_macos.dylib",
				"the first host's slice is no longer needed and the install replaces the directory wholesale")
		})
	}
}

// TestSlicedInstallRefusesARepublishedIndex is the comparison this issue exists
// for. The publisher serves a different, entirely self-consistent publication
// under an unchanged version; addons.toml and addons.lock are untouched, and the
// install must refuse rather than quietly install different bytes.
func TestSlicedInstallRefusesARepublishedIndex(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))
	lockBefore := lockBytesOf(t, projectRoot)
	markerPath := filepath.Join(projectRoot, "addons", slicedAddonName, "scripts", "marker.gd")
	require.Contains(t, string(readFileBytes(t, markerPath)), "one")

	// A teammate's fresh clone: the committed lock is present and consistent,
	// and nothing is on this disk yet, so the addon must be fetched again.
	require.NoError(t, os.Remove(filepath.Join(projectRoot, project.StateFileName)))
	require.NoError(t, os.RemoveAll(filepath.Join(projectRoot, "addons")))
	publisher.publish(packSlicedFixture(t, "two"))

	err := executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot)
	require.Error(t, err)
	var fetchError *output.FetchError
	require.ErrorAs(t, err, &fetchError)
	require.Equal(t, output.ExitFetch, codeForError(err))
	require.Contains(t, err.Error(), slice.IndexFileName)
	require.Equal(t, string(lockBefore), string(lockBytesOf(t, projectRoot)),
		"a refused install leaves the pin it was measured against untouched")
	_, statErr := os.Stat(markerPath)
	require.True(t, os.IsNotExist(statErr), "nothing from the republished index is installed")
}

// TestSlicedInstallRefusesARepublishedIndexUnderHostOnly pins that verification
// is independent of the selection mode: the full published set is held against
// the lock even when only core and the host slice are materialized.
func TestSlicedInstallRefusesARepublishedIndexUnderHostOnly(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--host-only", "--dir", projectRoot))
	require.NoError(t, os.Remove(filepath.Join(projectRoot, project.StateFileName)))
	publisher.publish(packSlicedFixture(t, "two"))

	err := executeGPM(t, io.Discard, io.Discard, "install", "--host-only", "--dir", projectRoot)
	require.Error(t, err)
	require.Equal(t, output.ExitFetch, codeForError(err))
}

// TestSlicedInstallRecoversASliceMissingFromState covers the central
// reconciliation case directly: a consistent lock plus a needed slice this
// machine has not materialized must still fetch.
func TestSlicedInstallRecoversASliceMissingFromState(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))
	lockBefore := lockBytesOf(t, projectRoot)
	requests := publisher.requestCount()

	statePath := filepath.Join(projectRoot, project.StateFileName)
	state, err := manifest.LoadState(statePath)
	require.NoError(t, err)
	entry := state.Addons[slicedAddonName]
	entry.Slices = []string{"core", "ios.arm64"}
	state.Addons[slicedAddonName] = entry
	require.NoError(t, state.Save(statePath))

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))

	require.Greater(t, publisher.requestCount(), requests, "a needed slice absent from state must be fetched")
	require.Equal(t, string(lockBefore), string(lockBytesOf(t, projectRoot)),
		"re-materializing on one machine must not rewrite the shared lock")
	require.Equal(t, []string{"core", "ios.arm64", "macos"}, installedSlicesOf(t, projectRoot))
}

// TestSlicedInstallReResolvesWhenAPlatformIsDeclared pins that the
// machine-independent drift question keeps its current meaning: adding a
// platform changes spec_hash and re-resolves.
func TestSlicedInstallReResolvesWhenAPlatformIsDeclared(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))
	lock, err := manifest.LoadLock(filepath.Join(projectRoot, "addons.lock"))
	require.NoError(t, err)
	hashBefore := lock.Addons[slicedAddonName].SpecHash
	require.NotContains(t, installedFiles(t, projectRoot), "bin/addon_windows.dll")

	writeSlicedManifest(t, projectRoot, publisher, "ios.arm64", "windows.x86_64")
	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))

	lock, err = manifest.LoadLock(filepath.Join(projectRoot, "addons.lock"))
	require.NoError(t, err)
	require.NotEqual(t, hashBefore, lock.Addons[slicedAddonName].SpecHash)
	require.Contains(t, installedFiles(t, projectRoot), "bin/addon_windows.dll")
}

// TestSelectionModeRoundTripsOnDisk pins that reconciliation is symmetric and
// self-healing in both directions, and that neither direction touches the lock.
func TestSelectionModeRoundTripsOnDisk(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	install := func(args ...string) {
		t.Helper()
		require.NoError(t, executeGPM(t, io.Discard, io.Discard,
			append([]string{"install", "--dir", projectRoot}, args...)...))
	}

	install("--host-only")
	lockReference := string(lockBytesOf(t, projectRoot))
	require.Equal(t, []string{"core", "macos"}, installedSlicesOf(t, projectRoot))
	stateAfterHostOnly := string(stateBytesOf(t, projectRoot))

	install()
	require.Equal(t, []string{"core", "ios.arm64", "macos"}, installedSlicesOf(t, projectRoot),
		"a default install after a host-only one re-materializes the declared platforms")
	require.Contains(t, installedFiles(t, projectRoot), "bin/addon_ios.dylib")
	require.NotEqual(t, stateAfterHostOnly, string(stateBytesOf(t, projectRoot)))
	require.Equal(t, lockReference, string(lockBytesOf(t, projectRoot)))

	install("--all-platforms")
	require.Equal(t,
		[]string{"core", "ios.arm64", "linux.x86_64", "macos", "windows.x86_64"},
		installedSlicesOf(t, projectRoot))
	require.Equal(t, lockReference, string(lockBytesOf(t, projectRoot)))

	install()
	require.Equal(t, []string{"core", "ios.arm64", "macos"}, installedSlicesOf(t, projectRoot),
		"a default install after --all-platforms prunes the slices the project does not declare")
	require.NotContains(t, installedFiles(t, projectRoot), "bin/addon_windows.dll")
	require.Equal(t, lockReference, string(lockBytesOf(t, projectRoot)))

	install("--all-platforms")
	install("--host-only")
	require.Equal(t, []string{"core", "macos"}, installedSlicesOf(t, projectRoot),
		"host-only after --all-platforms prunes the disk back down")
	files := installedFiles(t, projectRoot)
	require.NotContains(t, files, "bin/addon_windows.dll")
	require.NotContains(t, files, "bin/addon_linux.so")
	require.NotContains(t, files, "bin/addon_ios.dylib")
	require.Equal(t, lockReference, string(lockBytesOf(t, projectRoot)))
	require.Equal(t, stateAfterHostOnly, string(stateBytesOf(t, projectRoot)))
}

// TestHostOnlyWithNoPublishedHostSliceSucceeds pins that a pure-GDScript addon,
// or one that simply does not support this machine, never breaks a worktree
// setup: core alone is installed and the run exits 0.
func TestHostOnlyWithNoPublishedHostSliceSucceeds(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, unpublishedHost)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	require.NoError(t, executeGPM(t, stdout, stderr, "install", "--host-only", "--verbose", "--dir", projectRoot))

	require.Equal(t, []string{"core"}, installedSlicesOf(t, projectRoot))
	files := installedFiles(t, projectRoot)
	require.Contains(t, files, "plugin.cfg")
	require.NotContains(t, files, "bin/addon_macos.dylib")
	require.Contains(t, stderr.String(), "publishes no slice for this host")
	require.Contains(t, stderr.String(), unpublishedHost.OperatingSystem)
	require.Contains(t, stderr.String(), "ios.arm64", "the diagnostic names the published set")

	for name, args := range map[string][]string{
		"--quiet": {"--verbose", "--quiet"},
		"--json":  {"--verbose", "--json"},
	} {
		t.Run("the diagnostic is silent under "+name, func(t *testing.T) {
			require.NoError(t, os.Remove(filepath.Join(projectRoot, project.StateFileName)))
			quietErr := &bytes.Buffer{}
			require.NoError(t, executeGPM(t, io.Discard, quietErr,
				append([]string{"install", "--host-only", "--dir", projectRoot}, args...)...))
			require.Empty(t, quietErr.String())
		})
	}
}

// TestHostOnlyStillFailsOnAnUnpublishedDeclaredPlatform pins that host-only
// never suppresses manifest validation: the mode changes what is materialized,
// not whether the manifest is correct.
func TestHostOnlyStillFailsOnAnUnpublishedDeclaredPlatform(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "web.wasm32")

	err := executeGPM(t, io.Discard, io.Discard, "install", "--host-only", "--dir", projectRoot)
	require.Error(t, err)
	var fetchError *output.FetchError
	require.ErrorAs(t, err, &fetchError)
	require.Equal(t, output.ExitFetch, codeForError(err))
	require.Contains(t, err.Error(), "web.wasm32")
}

// TestContradictorySelectionFlagsFetchNothing pins that the usage error is
// raised before any project discovery or fetch.
func TestContradictorySelectionFlagsFetchNothing(t *testing.T) {
	for _, command := range []string{"install", "update"} {
		t.Run(command, func(t *testing.T) {
			withEnvironment(t, nil)
			withHost(t, hostA)
			publisher := servePublisher(t, packSlicedFixture(t, "one"))
			projectRoot := newSlicedProject(t, publisher, "ios.arm64")

			err := executeGPM(t, io.Discard, io.Discard,
				command, "--host-only", "--all-platforms", "--dir", projectRoot)
			require.Error(t, err)
			var usageError *UsageError
			require.ErrorAs(t, err, &usageError)
			require.Equal(t, output.ExitUsage, codeForError(err))
			require.Zero(t, publisher.requestCount(), "nothing may be fetched before the flags are accepted")
		})
	}
}

// TestHostOnlyFromTheEnvironment pins the unattended case the variable exists
// for, and that an explicit flag still wins over it.
func TestHostOnlyFromTheEnvironment(t *testing.T) {
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))

	t.Run("a truthy variable selects host-only", func(t *testing.T) {
		withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: "true"})
		projectRoot := newSlicedProject(t, publisher, "ios.arm64")
		require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))
		require.Equal(t, []string{"core", "macos"}, installedSlicesOf(t, projectRoot))
	})

	t.Run("--all-platforms overrides a truthy variable", func(t *testing.T) {
		withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: "true"})
		projectRoot := newSlicedProject(t, publisher, "ios.arm64")
		require.NoError(t, executeGPM(t, io.Discard, io.Discard,
			"install", "--all-platforms", "--dir", projectRoot))
		require.Equal(t,
			[]string{"core", "ios.arm64", "linux.x86_64", "macos", "windows.x86_64"},
			installedSlicesOf(t, projectRoot))
	})

	t.Run("--host-only=false overrides a truthy variable", func(t *testing.T) {
		withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: "true"})
		projectRoot := newSlicedProject(t, publisher, "ios.arm64")
		require.NoError(t, executeGPM(t, io.Discard, io.Discard,
			"install", "--host-only=false", "--dir", projectRoot))
		require.Equal(t, []string{"core", "ios.arm64", "macos"}, installedSlicesOf(t, projectRoot))
	})

	t.Run("a malformed variable is a usage error", func(t *testing.T) {
		withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: "yes"})
		projectRoot := newSlicedProject(t, publisher, "ios.arm64")
		err := executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot)
		require.Error(t, err)
		require.Equal(t, output.ExitUsage, codeForError(err))
	})
}

// TestCorruptStateFileIsNotFatal pins that an unparseable machine-local cache
// costs a re-download rather than wedging the install behind a file the user is
// not meant to care about. It must not become exit 3.
func TestCorruptStateFileIsNotFatal(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")
	statePath := filepath.Join(projectRoot, project.StateFileName)

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))
	requests := publisher.requestCount()

	corrupt := func() {
		t.Helper()
		require.NoError(t, os.WriteFile(statePath, []byte("this is not toml = = ["), 0o644))
	}

	corrupt()
	stderr := &bytes.Buffer{}
	require.NoError(t, executeGPM(t, io.Discard, stderr, "install", "--verbose", "--dir", projectRoot))
	require.Greater(t, publisher.requestCount(), requests, "an ignored state file re-materializes the addon")
	require.Contains(t, stderr.String(), "ignoring unparseable state file")
	require.Equal(t, []string{"core", "ios.arm64", "macos"}, installedSlicesOf(t, projectRoot))

	corrupt()
	quietErr := &bytes.Buffer{}
	require.NoError(t, executeGPM(t, io.Discard, quietErr, "install", "--verbose", "--quiet", "--dir", projectRoot))
	require.Empty(t, quietErr.String())
}

// TestInstallAndUpdateJSONReportTheInstalledSlices pins that the
// machine-readable signal is what was materialized, not the declared or
// published set.
func TestInstallAndUpdateJSONReportTheInstalledSlices(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))

	for _, command := range []string{"install", "update"} {
		t.Run(command+" --json under --host-only", func(t *testing.T) {
			projectRoot := newSlicedProject(t, publisher, "ios.arm64")
			stdout := &bytes.Buffer{}
			require.NoError(t, executeGPM(t, stdout, io.Discard,
				command, "--host-only", "--json", "--dir", projectRoot))

			var results []struct {
				Name   string   `json:"name"`
				Slices []string `json:"slices"`
			}
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &results))
			require.Len(t, results, 1)
			require.Equal(t, slicedAddonName, results[0].Name)
			require.Equal(t, []string{"core", "macos"}, results[0].Slices)
		})

		t.Run(command+" --quiet writes nothing", func(t *testing.T) {
			projectRoot := newSlicedProject(t, publisher, "ios.arm64")
			stdout := &bytes.Buffer{}
			require.NoError(t, executeGPM(t, stdout, io.Discard, command, "--quiet", "--dir", projectRoot))
			require.Empty(t, stdout.String())
		})
	}
}

// TestStateEntriesArePrunedForRemovedAddons pins that a full run drops records
// for addons no longer in the manifest, alongside the existing lock pruning.
func TestStateEntriesArePrunedForRemovedAddons(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))
	require.NotEmpty(t, installedSlicesOf(t, projectRoot))

	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, "addons.toml"), []byte("[addons]\n"), 0o644))
	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))

	state, err := manifest.LoadState(filepath.Join(projectRoot, project.StateFileName))
	require.NoError(t, err)
	require.NotContains(t, state.Addons, slicedAddonName)
	lock, err := manifest.LoadLock(filepath.Join(projectRoot, "addons.lock"))
	require.NoError(t, err)
	require.NotContains(t, lock.Addons, slicedAddonName)
}

// TestUpdateRewritesTheSlicePinsFromTheFreshIndex pins that ModeUpdate
// re-resolves without comparing against the old lock, which is how a legitimate
// republication is adopted.
func TestUpdateRewritesTheSlicePinsFromTheFreshIndex(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))
	lock, err := manifest.LoadLock(filepath.Join(projectRoot, "addons.lock"))
	require.NoError(t, err)
	indexBefore := lock.Addons[slicedAddonName].IndexChecksum
	require.NotEmpty(t, indexBefore)
	require.Len(t, lock.Addons[slicedAddonName].Slices, 5)
	require.Empty(t, lock.Addons[slicedAddonName].Checksum,
		"a sliced addon has no single archive to pin")

	publisher.publish(packSlicedFixture(t, "two"))
	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "update", "--dir", projectRoot))

	lock, err = manifest.LoadLock(filepath.Join(projectRoot, "addons.lock"))
	require.NoError(t, err)
	require.NotEqual(t, indexBefore, lock.Addons[slicedAddonName].IndexChecksum)
	require.Contains(t, string(readFileBytes(t,
		filepath.Join(projectRoot, "addons", slicedAddonName, "scripts", "marker.gd"))), "two")
}

// TestSlicedLockRecordsTheWholePublishedSet pins the shape the cross-machine
// invariant rests on: the lock's slices table is the index's published set, not
// the subset this disk took.
func TestSlicedLockRecordsTheWholePublishedSet(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")

	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--host-only", "--dir", projectRoot))

	lock, err := manifest.LoadLock(filepath.Join(projectRoot, "addons.lock"))
	require.NoError(t, err)
	entry := lock.Addons[slicedAddonName]
	for _, id := range []string{"core", "ios.arm64", "linux.x86_64", "macos", "windows.x86_64"} {
		require.Contains(t, entry.Slices, id)
	}
	require.Equal(t, []string{"core", "macos"}, installedSlicesOf(t, projectRoot),
		"while only core and the host slice are on this disk")
}
