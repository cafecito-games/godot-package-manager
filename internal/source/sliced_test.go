package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// TestFetchResultCarriesSliceFields pins the shape #25 writes a lockfile from.
// It is a compile-time assertion first and a value assertion second: the field
// names and types are the contract, and a rename would stop this file building.
func TestFetchResultCarriesSliceFields(t *testing.T) {
	published := SliceResult{
		ID:       slice.SliceID{Platform: "ios", Architecture: "arm64"},
		Checksum: strings.Repeat("a", 64),
		Size:     42,
	}
	result := FetchResult{
		IndexChecksum:   strings.Repeat("b", 64),
		PublishedSlices: []SliceResult{published},
		InstalledSlices: []slice.SliceID{slice.CoreSliceID()},
	}
	require.Equal(t, "ios.arm64", result.PublishedSlices[0].ID.String())
	require.Equal(t, int64(42), result.PublishedSlices[0].Size)
	require.Equal(t, "core", result.InstalledSlices[0].String())
	require.Len(t, result.IndexChecksum, 64)
}

func TestGitHubReleaseSlicedFetchMergesOnlyNeededSlices(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t)
	server := fixture.serveRelease(t)

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name:      fixtureAddonName,
		Source:    manifest.SourceGitHubRelease,
		Repo:      "owner/repo",
		Version:   fixtureVersion,
		Platforms: []string{"ios.arm64"},
		// A sliced release names its archives in the index, so `asset` cannot
		// select one; it is reported rather than honored.
		Asset: "sliced-1.2.3-core.zip",
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	// The declared iOS slice, the host's macOS slice, and core; no other
	// platform's binary is downloaded at all.
	require.ElementsMatch(t, []string{
		"bin/addon_ios.dylib",
		"bin/addon_macos.dylib",
		"bin/libgodot_ios.a",
		"plugin.cfg",
		"scripts/sliced_extra.gd",
		"scripts/sliced_more.gd",
		"scripts/sliced_tool.gd",
		"scripts/sliced_util.gd",
		fixtureExtensionPath,
	}, treeFiles(t, result.Dir))

	require.Equal(t, fixtureVersion, result.ResolvedVersion)
	require.Empty(t, result.Checksum, "a sliced addon has no single archive checksum")

	digest := sha256.Sum256(fixture.indexBytes)
	require.Equal(t, hex.EncodeToString(digest[:]), result.IndexChecksum)

	require.Equal(t, []string{"core", "ios.arm64", "macos"}, renderSliceIDs(result.InstalledSlices))
	require.Equal(t, []string{
		"android.arm64", "core", "ios.arm64", "linux.x86_64", "macos", "web.wasm32", "windows.x86_64",
	}, renderPublishedSlices(result.PublishedSlices))
	for _, published := range result.PublishedSlices {
		declared := fixture.index.Slices[published.ID.String()]
		require.Equal(t, declared.SHA256, published.Checksum)
		require.Equal(t, declared.Size, published.Size)
	}
	require.Len(t, result.Diagnostics, 1)
	require.Contains(t, result.Diagnostics[0], "`asset`")
}

// TestSlicedFetchReassemblesOnlyInstalledEntries asserts the installed
// .gdextension describes exactly the binaries beside it, by partitioning it
// again through the producer's own splitter.
func TestSlicedFetchReassemblesOnlyInstalledEntries(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t)
	server := fixture.serveRelease(t)

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name: fixtureAddonName, Source: manifest.SourceGitHubRelease,
		Repo: "owner/repo", Version: fixtureVersion, Platforms: []string{"ios.arm64"},
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	installed, err := os.ReadFile(filepath.Join(result.Dir, fixtureExtensionPath))
	require.NoError(t, err)
	_, entries, err := slice.PartitionExtension(
		installed, slice.AddonResourceRoot(fixtureAddonName), fixtureExtensionPath)
	require.NoError(t, err)

	iosID := slice.SliceID{Platform: "ios", Architecture: "arm64"}
	macosID := slice.SliceID{Platform: "macos"}
	require.ElementsMatch(t, []slice.SliceID{iosID, macosID}, mapKeys(entries))
	require.Equal(t,
		slice.ExtensionEntryTable[string]{
			"ios.template_release.arm64": "res://addons/sliced/bin/addon_ios.dylib",
		}, entries[iosID].Libraries)
	require.Equal(t,
		slice.ExtensionEntryTable[slice.ExtensionDependencyTargets]{
			"ios.template_release.arm64": {"res://addons/sliced/bin/libgodot_ios.a": ""},
		}, entries[iosID].Dependencies)
	require.Equal(t,
		slice.ExtensionEntryTable[string]{
			"macos.template_release": "res://addons/sliced/bin/addon_macos.dylib",
		}, entries[macosID].Libraries)
	require.Nil(t, entries[macosID].Dependencies)
}

// TestSlicedFetchRewritesInstallAsPrefix pins the install_as half of the
// re-rooting rule: an index publishes project-absolute paths under the addon's
// own directory, and a project that renames that directory needs the installed
// .gdextension to name the directory it actually has.
func TestSlicedFetchRewritesInstallAsPrefix(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t)
	server := fixture.serveRelease(t)

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name: fixtureAddonName, Source: manifest.SourceGitHubRelease,
		Repo: "owner/repo", Version: fixtureVersion,
		Platforms: []string{"ios.arm64"}, InstallAs: "renamed",
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	installed, err := os.ReadFile(filepath.Join(result.Dir, fixtureExtensionPath))
	require.NoError(t, err)
	require.Contains(t, string(installed), "res://addons/renamed/bin/addon_ios.dylib")
	require.Contains(t, string(installed), "res://addons/renamed/bin/libgodot_ios.a")
	require.Contains(t, string(installed), "res://addons/renamed/bin/addon_macos.dylib")
	require.NotContains(t, string(installed), slice.AddonResourceRoot(fixtureAddonName)+"/")
}

// TestSlicedFetchUnsupportedHostDiagnoses pins that a host the addon publishes
// no slice for is the publisher's statement rather than a project error.
func TestSlicedFetchUnsupportedHostDiagnoses(t *testing.T) {
	withHost(t, unpublishedHost)
	fixture := packFixture(t)
	server := fixture.serveRelease(t)

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name: fixtureAddonName, Source: manifest.SourceGitHubRelease,
		Repo: "owner/repo", Version: fixtureVersion, Platforms: []string{"linux.x86_64"},
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	require.Equal(t, []string{"core", "linux.x86_64"}, renderSliceIDs(result.InstalledSlices))
	require.Len(t, result.Diagnostics, 1)
	require.Contains(t, result.Diagnostics[0], "plan9/mips")
	require.Contains(t, result.Diagnostics[0], "android.arm64, core, ios.arm64")
	require.Contains(t, treeFiles(t, result.Dir), "bin/addon_linux.so")
	require.NotContains(t, treeFiles(t, result.Dir), "bin/addon_macos.dylib")
}

// TestArchiveSlicedFetchResolvesArchivesBesideIndex pins that an `archive`
// source with `index` takes the sliced path and finds the archives at the
// index's own location.
func TestArchiveSlicedFetchResolvesArchivesBesideIndex(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t)
	server := fixture.serveArchiveHost(t)

	fetcher := &ArchiveFetcher{}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name:    fixtureAddonName,
		Source:  manifest.SourceArchive,
		URL:     server.URL + "/dist/whole-addon.zip",
		Index:   server.URL + "/dist/" + slice.IndexFileName,
		Version: fixtureVersion,
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	require.Equal(t, []string{"core", "macos"}, renderSliceIDs(result.InstalledSlices))
	require.Contains(t, treeFiles(t, result.Dir), "bin/addon_macos.dylib")
	require.Len(t, result.Diagnostics, 1)
	require.Contains(t, result.Diagnostics[0], "`url`")
}

// TestArchiveFetchWithoutIndexStaysUnsliced pins that the unsliced path is
// unchanged when no index is declared.
func TestArchiveFetchWithoutIndexStaysUnsliced(t *testing.T) {
	payload := zipArchive(t, map[string]string{"plugin.cfg": "[plugin]"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	fetcher := &ArchiveFetcher{}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name: "plain", Source: manifest.SourceArchive, URL: server.URL + "/x.zip",
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	requireUnslicedResult(t, result)
}

// TestGitHubReleaseWithoutIndexAssetStaysUnsliced pins that a release with no
// index asset installs by the existing single-asset path, with every slice field
// zero-valued.
func TestGitHubReleaseWithoutIndexAssetStaysUnsliced(t *testing.T) {
	payload := zipArchive(t, map[string]string{"plugin.cfg": "[plugin]"})
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{
				{"name": "addon.zip", "url": "http://" + r.Host + "/assets/addon.zip"},
			},
		})
	})
	mux.HandleFunc("/assets/addon.zip", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	result, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name: "plain", Source: manifest.SourceGitHubRelease, Repo: "owner/repo", Version: "1.0",
		// Declared platforms are irrelevant without an index: the release
		// publishes one archive and that is what is installed.
		Platforms: []string{"ios.arm64"},
	})
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	require.NotEmpty(t, result.Checksum)
	requireUnslicedResult(t, result)
}

// TestGitSourceIsNeverSliced pins that slices are a property of published
// release artifacts rather than of a source checkout, even for a spec that
// carries platforms and an index.
func TestGitSourceIsNeverSliced(t *testing.T) {
	repo := makeLocalRepo(t)
	spec := manifest.AddonSpec{
		Name: "plain", Source: manifest.SourceGit, URL: repo, Version: "v1.0",
		Platforms: []string{"ios.arm64"}, Index: "https://example.invalid/" + slice.IndexFileName,
	}
	_, sliced := slicedIndexURL(spec, func(string) (string, bool) {
		t.Fatal("a git source must not consult an asset list")
		return "", false
	})
	require.False(t, sliced)

	result, err := (&GitFetcher{}).Fetch(context.Background(), spec)
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()
	requireUnslicedResult(t, result)
}

// TestSlicedIndexURLIsTheOneDecision pins the decision function both fetchers
// share, including that an archive source never consults an asset list and a
// release source never reads the manifest's index field.
func TestSlicedIndexURLIsTheOneDecision(t *testing.T) {
	refuse := func(string) (string, bool) { return "", false }
	offer := func(name string) (string, bool) { return "https://example.invalid/" + name, true }

	indexURL, sliced := slicedIndexURL(
		manifest.AddonSpec{Source: manifest.SourceArchive, Index: "https://example.invalid/i.toml"}, refuse)
	require.True(t, sliced)
	require.Equal(t, "https://example.invalid/i.toml", indexURL)

	_, sliced = slicedIndexURL(manifest.AddonSpec{Source: manifest.SourceArchive}, offer)
	require.False(t, sliced)

	indexURL, sliced = slicedIndexURL(manifest.AddonSpec{Source: manifest.SourceGitHubRelease}, offer)
	require.True(t, sliced)
	require.Equal(t, "https://example.invalid/"+slice.IndexFileName, indexURL)

	_, sliced = slicedIndexURL(manifest.AddonSpec{Source: manifest.SourceGitHubRelease}, refuse)
	require.False(t, sliced)
}

func TestArchiveSliceResolverResolvesSiblings(t *testing.T) {
	resolve := archiveSliceResolver("https://example.test/dist/" + slice.IndexFileName)
	resolved, found := resolve("addon-1.0-core.zip")
	require.True(t, found)
	require.Equal(t, "https://example.test/dist/addon-1.0-core.zip", resolved)

	resolve = archiveSliceResolver("https://example.test/" + slice.IndexFileName + "?token=secret")
	resolved, found = resolve("addon-1.0-core.zip")
	require.True(t, found)
	require.Equal(t, "https://example.test/addon-1.0-core.zip", resolved)
}

// requireUnslicedResult asserts every slice field is zero-valued, which is the
// contract for a fetch that took the unsliced path.
func requireUnslicedResult(t *testing.T, result FetchResult) {
	t.Helper()
	require.Empty(t, result.IndexChecksum)
	require.Nil(t, result.PublishedSlices)
	require.Nil(t, result.InstalledSlices)
	require.Nil(t, result.Diagnostics)
}

func renderSliceIDs(ids []slice.SliceID) []string {
	rendered := make([]string, 0, len(ids))
	for _, id := range ids {
		rendered = append(rendered, id.String())
	}
	return rendered
}

func renderPublishedSlices(published []SliceResult) []string {
	rendered := make([]string, 0, len(published))
	for _, entry := range published {
		rendered = append(rendered, entry.ID.String())
	}
	return rendered
}

func mapKeys[Value any](table map[slice.SliceID]Value) []slice.SliceID {
	keys := make([]slice.SliceID, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	return keys
}
