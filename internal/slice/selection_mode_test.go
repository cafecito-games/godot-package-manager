package slice

import (
	"errors"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

// TestSelectDeclaredPlatformsIsTheZeroSelectionMode pins the property that lets
// the boolean parameter be replaced without any call site changing meaning: a
// SelectionMode field left unset, in a Runner literal or anywhere else, is the
// shipping default.
func TestSelectDeclaredPlatformsIsTheZeroSelectionMode(t *testing.T) {
	require.Equal(t, SelectionMode(0), SelectDeclaredPlatforms)
	require.NotEqual(t, SelectDeclaredPlatforms, SelectAllPublishedSlices)
	require.NotEqual(t, SelectDeclaredPlatforms, SelectHostOnly)
	require.NotEqual(t, SelectAllPublishedSlices, SelectHostOnly)
}

func TestSelectHostOnlyReturnsCorePlusTheFirstPublishedHostCandidate(t *testing.T) {
	host := Host{OperatingSystem: "darwin", Architecture: "arm64"}
	published := publishedSlices(t, "core", "macos", "macos.universal", "ios.arm64", "linux.x86_64")

	selection, err := SelectSlices([]string{"ios.arm64", "linux.x86_64"}, host, published, SelectHostOnly)
	require.NoError(t, err)
	require.True(t, selection.HostSupported)
	require.Equal(t, "macos.universal", selection.HostSlice.String(),
		"host-only reuses the candidate chain, so it picks the slice a default install would have picked")
	require.Equal(t, []string{CorePlatform, "macos.universal"}, sliceIDStrings(selection.Slices),
		"the declared platforms are validated but not materialized")
}

func TestSelectHostOnlyReturnsCoreAloneWhenTheHostIsUnpublished(t *testing.T) {
	published := publishedSlices(t, "core", "ios.arm64")

	for name, host := range map[string]Host{
		"a host with a chain the addon publishes nothing for": {OperatingSystem: "linux", Architecture: "amd64"},
		"a host gpm has no candidate chain for":               {OperatingSystem: "plan9", Architecture: "mips"},
	} {
		t.Run(name, func(t *testing.T) {
			selection, err := SelectSlices([]string{"ios.arm64"}, host, published, SelectHostOnly)
			require.NoError(t, err, "an addon that does not support this machine is the author's statement, not a failure")
			require.False(t, selection.HostSupported)
			require.Equal(t, SliceID{}, selection.HostSlice)
			require.Equal(t, []string{CorePlatform}, sliceIDStrings(selection.Slices))
		})
	}
}

// TestSelectHostOnlyStillValidatesDeclaredPlatforms pins that a selection mode
// changes which slices are materialized and never whether the manifest is
// correct. A platform the addon does not publish is a manifest mistake however
// little of the addon this machine wants.
func TestSelectHostOnlyStillValidatesDeclaredPlatforms(t *testing.T) {
	host := Host{OperatingSystem: "darwin", Architecture: "arm64"}
	published := publishedSlices(t, "core", "macos")

	t.Run("a declared platform the addon does not publish", func(t *testing.T) {
		selection, err := SelectSlices([]string{"windows.x86_64"}, host, published, SelectHostOnly)
		require.Error(t, err)
		require.Equal(t, Selection{}, selection)
		var fetchError *output.FetchError
		require.True(t, errors.As(err, &fetchError))
		require.Equal(t, output.ExitFetch, output.CodeFor(err))
		require.Contains(t, err.Error(), "windows.x86_64")
	})

	t.Run("an invalid declared tag", func(t *testing.T) {
		selection, err := SelectSlices([]string{"macos.debug"}, host, published, SelectHostOnly)
		require.Error(t, err)
		require.Equal(t, Selection{}, selection)
		var manifestError *output.ManifestError
		require.True(t, errors.As(err, &manifestError))
		require.Equal(t, output.ExitManifest, output.CodeFor(err))
	})
}

// TestSelectSlicesModesAgreeOnTheHostSlice pins that the three modes differ only
// in which slices they add beyond core and the host's own.
func TestSelectSlicesModesAgreeOnTheHostSlice(t *testing.T) {
	host := Host{OperatingSystem: "linux", Architecture: "amd64"}
	published := publishedSlices(t, "core", "linux.x86_64", "ios.arm64", "windows.x86_64")
	declared := []string{"ios.arm64"}

	declaredSelection, err := SelectSlices(declared, host, published, SelectDeclaredPlatforms)
	require.NoError(t, err)
	require.Equal(t, []string{CorePlatform, "ios.arm64", "linux.x86_64"}, sliceIDStrings(declaredSelection.Slices))

	allSelection, err := SelectSlices(declared, host, published, SelectAllPublishedSlices)
	require.NoError(t, err)
	require.Equal(t,
		[]string{CorePlatform, "ios.arm64", "linux.x86_64", "windows.x86_64"},
		sliceIDStrings(allSelection.Slices))

	hostSelection, err := SelectSlices(declared, host, published, SelectHostOnly)
	require.NoError(t, err)
	require.Equal(t, []string{CorePlatform, "linux.x86_64"}, sliceIDStrings(hostSelection.Slices))

	require.Equal(t, declaredSelection.HostSlice, allSelection.HostSlice)
	require.Equal(t, declaredSelection.HostSlice, hostSelection.HostSlice)
}
