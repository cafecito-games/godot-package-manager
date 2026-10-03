package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/packager"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// fanOutExtension publishes two architecture slices of one platform plus a
// generic entry for it, which is the shape `gpm package` fans out: the generic
// binary lands in both architecture archives so that either one installs on its
// own.
const fanOutExtension = `[configuration]

entry_symbol = "sliced_main"

[libraries]

macos.editor = "res://addons/sliced/bin/addon_macos.dylib"
macos.template_release.universal = "res://addons/sliced/bin/addon_macos_universal.dylib"
macos.template_release.arm64 = "res://addons/sliced/bin/addon_macos_arm64.dylib"
`

// fanOutBinaries are the files fanOutExtension names, plus a script so core is
// not empty.
var fanOutBinaries = map[string]string{
	"bin/addon_macos.dylib":           "generic macos binary",
	"bin/addon_macos_universal.dylib": "universal macos binary",
	"bin/addon_macos_arm64.dylib":     "arm64 macos binary",
	"plugin.cfg":                      "[plugin]\nname=\"sliced\"\n",
}

// packFanOutFixture packages an addon shaped like fanOutExtension, optionally
// with a [package.slices] block appended to its config.
func packFanOutFixture(t *testing.T, slices string) slicedFixture {
	t.Helper()
	root := t.TempDir()
	config := fmt.Sprintf(`[package]
name = %q
addon_path = %q
version = %q
`, fixtureAddonName, slice.AddonInstallPath(fixtureAddonName), fixtureVersion) + slices
	require.NoError(t, os.WriteFile(filepath.Join(root, packager.ConfigFileName), []byte(config), 0o644))

	addonRoot := filepath.Join(root, filepath.FromSlash(slice.AddonInstallPath(fixtureAddonName)))
	writeFixtureFile(t, addonRoot, fixtureExtensionPath, fanOutExtension)
	for path, content := range fanOutBinaries {
		writeFixtureFile(t, addonRoot, path, content)
	}
	writeFixtureFile(t, addonRoot, "bin/android/plugin.aar", "android plugin archive")

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	return loadFixtureOutput(t, result)
}

// TestSlicedFetchMergesAFannedOutFileSharedByTwoSelectedSlices covers the
// project that selects two architecture slices of one fanned-out platform, which
// is routine: an app shipping both macOS architectures declares both, and
// --all-platforms selects every published slice. Both archives carry the
// platform's generic binary by design, so the merge has to accept the second
// copy of it rather than report the publisher's release as broken.
func TestSlicedFetchMergesAFannedOutFileSharedByTwoSelectedSlices(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFanOutFixture(t, "")
	server := fixture.serveRelease(t)

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name:      fixtureAddonName,
		Source:    manifest.SourceGitHubRelease,
		Repo:      "owner/repo",
		Version:   fixtureVersion,
		Platforms: []string{"macos.arm64", "macos.universal"},
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	// A slice archive carries the addon subtree unprefixed, so the fetch
	// directory is the addon root.
	shared, err := os.ReadFile(filepath.Join(result.Dir, "bin", "addon_macos.dylib"))
	require.NoError(t, err)
	require.Equal(t, "generic macos binary", string(shared))

	// Both architectures' own binaries are there too, and the reassembled
	// .gdextension declares the shared generic key exactly once.
	for _, binary := range []string{"addon_macos_arm64.dylib", "addon_macos_universal.dylib"} {
		require.FileExists(t, filepath.Join(result.Dir, "bin", binary))
	}
	reassembled, err := os.ReadFile(filepath.Join(result.Dir, fixtureExtensionPath))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(reassembled), "macos.editor"))
}

// TestSlicedFetchMergesAFannedOutExtrasFileSharedByTwoSelectedSlices is the same
// property for a file no entry key names, which [package.slices] claims
// generically and the fan-out copies into every architecture slice.
func TestSlicedFetchMergesAFannedOutExtrasFileSharedByTwoSelectedSlices(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFanOutFixture(t, "\n[package.slices]\n\"macos\" = [\"bin/android/plugin.aar\"]\n")
	server := fixture.serveRelease(t)

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name:      fixtureAddonName,
		Source:    manifest.SourceGitHubRelease,
		Repo:      "owner/repo",
		Version:   fixtureVersion,
		Platforms: []string{"macos.arm64", "macos.universal"},
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	shared, err := os.ReadFile(filepath.Join(result.Dir, "bin", "android", "plugin.aar"))
	require.NoError(t, err)
	require.Equal(t, "android plugin archive", string(shared))
}

// TestSlicedFetchMergesEveryFannedOutSliceUnderAllPlatforms covers the selection
// the authoring docs recommend as the check before publishing a cut: every
// published slice at once, which is the widest set a fanned-out file is shared
// across.
func TestSlicedFetchMergesEveryFannedOutSliceUnderAllPlatforms(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFanOutFixture(t, "\n[package.slices]\n\"macos\" = [\"bin/android/plugin.aar\"]\n")
	server := fixture.serveRelease(t)

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL, selectionMode: slice.SelectAllPublishedSlices}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name:    fixtureAddonName,
		Source:  manifest.SourceGitHubRelease,
		Repo:    "owner/repo",
		Version: fixtureVersion,
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	require.ElementsMatch(t, []string{"core", "macos.arm64", "macos.universal"},
		sliceIDStrings(result.InstalledSlices))
	for _, path := range []string{
		"bin/addon_macos.dylib",
		"bin/addon_macos_arm64.dylib",
		"bin/addon_macos_universal.dylib",
		"bin/android/plugin.aar",
	} {
		require.FileExists(t, filepath.Join(result.Dir, filepath.FromSlash(path)))
	}
}

func sliceIDStrings(ids []slice.SliceID) []string {
	spelled := make([]string, 0, len(ids))
	for _, id := range ids {
		spelled = append(spelled, id.String())
	}
	return spelled
}
