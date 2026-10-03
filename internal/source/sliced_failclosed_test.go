package source

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// craftedSlice is one slice of a hand-built fixture, for the fail-closed cases a
// real packaging run cannot produce: an index that disagrees with its archives,
// an archive carrying a traversal or a symlink, or two slices shipping one path.
// Everything a real run can produce is driven by internal/packager instead.
type craftedSlice struct {
	// file overrides the archive's published asset name, which is also what
	// selects the archive format; empty derives a zip name from the slice ID.
	file string
	// body overrides the archive bytes; nil zips files into a zip instead.
	body []byte
	// files are the archive's entries when body is nil.
	files map[string]string

	libraries    slice.ExtensionSectionTable[string]
	dependencies slice.ExtensionSectionTable[slice.ExtensionDependencyTargets]
}

// craftFixture assembles an index over hand-built archives, pinning each by the
// checksum and size of the bytes it actually holds, so that only the property
// under test differs from a healthy fixture.
func craftFixture(t *testing.T, crafted map[string]craftedSlice) slicedFixture {
	t.Helper()
	index := &slice.Index{
		Format:  1,
		Name:    fixtureAddonName,
		Version: fixtureVersion,
		Slices:  map[string]*slice.IndexSlice{},
	}
	fixture := slicedFixture{assets: map[string][]byte{}}
	for id, definition := range crafted {
		body := definition.body
		if body == nil {
			body = zipArchive(t, definition.files)
		}
		name := definition.file
		if name == "" {
			name = fmt.Sprintf("%s-%s-%s.zip", fixtureAddonName, fixtureVersion, id)
		}
		digest := sha256.Sum256(body)
		fixture.assets[name] = body
		index.Slices[id] = &slice.IndexSlice{
			File:         name,
			SHA256:       hex.EncodeToString(digest[:]),
			Size:         int64(len(body)),
			Libraries:    definition.libraries,
			Dependencies: definition.dependencies,
		}
	}
	path := filepath.Join(t.TempDir(), slice.IndexFileName)
	require.NoError(t, index.Save(path))
	indexBytes, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := slice.LoadIndex(indexBytes)
	require.NoError(t, err, "a crafted fixture must still be an index the loader accepts")
	fixture.indexBytes = indexBytes
	fixture.index = parsed
	return fixture
}

// partitionedCoreBody is the fixture .gdextension's core body, produced by the
// producer's own splitter, which is the body reassembly expects to find in the
// core archive.
func partitionedCoreBody(t *testing.T) string {
	t.Helper()
	core, _, err := slice.PartitionExtension(
		[]byte(fixtureExtension), slice.AddonResourceRoot(fixtureAddonName), fixtureExtensionPath)
	require.NoError(t, err)
	return string(core)
}

// fetchSlicedRelease drives the sliced github-release path over a fixture and
// reports the result, the isolated temp root a caller asserts a failure left
// nothing in, and the error.
func fetchSlicedRelease(
	t *testing.T,
	fixture slicedFixture,
	spec manifest.AddonSpec,
	configure func(*GitHubReleaseFetcher),
) (FetchResult, string, error) {
	t.Helper()
	temporaryRoot := t.TempDir()
	t.Setenv("TMPDIR", temporaryRoot)
	server := fixture.serveRelease(t)
	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	if configure != nil {
		configure(fetcher)
	}
	spec.Name = fixtureAddonName
	spec.Source = manifest.SourceGitHubRelease
	spec.Repo = "owner/repo"
	spec.Version = fixtureVersion
	result, err := fetcher.Fetch(context.Background(), spec)
	return result, temporaryRoot, err
}

// requireFetchFailure asserts a failure's error type, its exit code, and that
// nothing was left in the temp root, which is the cleanup half of every
// fail-closed row.
func requireFetchFailure(t *testing.T, err error, temporaryRoot string, code output.ExitCode, substrings ...string) {
	t.Helper()
	require.Error(t, err)
	switch code {
	case output.ExitFetch:
		var fetchError *output.FetchError
		require.ErrorAs(t, err, &fetchError)
	case output.ExitInstall:
		var installError *output.InstallError
		require.ErrorAs(t, err, &installError)
	case output.ExitManifest:
		var manifestError *output.ManifestError
		require.ErrorAs(t, err, &manifestError)
	default:
		t.Fatalf("unexpected expected exit code %d", code)
	}
	require.Equal(t, code, output.CodeFor(err))
	for _, substring := range substrings {
		require.Contains(t, err.Error(), substring)
	}
	requireTemporaryRootEmpty(t, temporaryRoot)
}

func requireTemporaryRootEmpty(t *testing.T, temporaryRoot string) {
	t.Helper()
	entries, err := os.ReadDir(temporaryRoot)
	require.NoError(t, err)
	var leftovers []string
	for _, entry := range entries {
		leftovers = append(leftovers, entry.Name())
	}
	require.Empty(t, leftovers, "a failed fetch left temporary files behind")
}

func TestSlicedFetchIndexDownloadFailureIsFatal(t *testing.T) {
	temporaryRoot := t.TempDir()
	t.Setenv("TMPDIR", temporaryRoot)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/tags/1.0", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"assets":[{"name":%q,"url":"http://%s/assets/index"}]}`,
			slice.IndexFileName, r.Host)
	})
	mux.HandleFunc("/assets/index", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	fetcher := &GitHubReleaseFetcher{APIBase: server.URL}
	_, err := fetcher.Fetch(context.Background(), manifest.AddonSpec{
		Name: fixtureAddonName, Source: manifest.SourceGitHubRelease, Repo: "owner/repo", Version: "1.0",
	})
	// Never a silent fall back to the single-asset path, which would install
	// whichever archive happened to be there.
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch, slice.IndexFileName, "HTTP 500")
}

func TestSlicedFetchRejectsUnparseableIndex(t *testing.T) {
	fixture := packFixture(t).withIndex([]byte("this is not toml = ["))
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch, "parsing index")
}

func TestSlicedFetchRejectsNewerIndexFormat(t *testing.T) {
	fixture := packFixture(t).withIndex([]byte("format = 99\nname = \"sliced\"\nversion = \"1.2.3\"\n"))
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch, "unsupported index format 99")
}

func TestSlicedFetchRejectsUnpublishedDeclaredPlatform(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t)
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture,
		manifest.AddonSpec{Platforms: []string{"linux.arm64"}}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch,
		`declared platform "linux.arm64"`, "published slices are core, android.arm64")
}

func TestSlicedFetchRejectsMissingSliceArchive(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t).mutateIndex(t, func(index *slice.Index) {
		index.Slices["macos"].File = "sliced-1.2.3-macos-missing.zip"
	})
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch,
		`slice "macos"`, "sliced-1.2.3-macos-missing.zip", "does not offer")
}

func TestSlicedFetchRejectsSliceChecksumMismatch(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t).mutateIndex(t, func(index *slice.Index) {
		index.Slices["macos"].SHA256 = strings.Repeat("0", 64)
	})
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch,
		`slice "macos": checksum mismatch`, strings.Repeat("0", 64))
}

func TestSlicedFetchRejectsSliceSizeMismatch(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t)
	declaredSize := fixture.index.Slices["macos"].Size
	fixture = fixture.mutateIndex(t, func(index *slice.Index) {
		index.Slices["macos"].Size = declaredSize + 1
	})
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch,
		`slice "macos": size mismatch`,
		fmt.Sprintf("index: %d bytes, downloaded: %d bytes", declaredSize+1, declaredSize))
}

// TestSlicedMergeRejectsCollidingPathsInEitherOrder pins that two slices
// shipping one path is an error naming both, and that the message does not
// depend on which of them was extracted first.
func TestSlicedMergeRejectsCollidingPathsInEitherOrder(t *testing.T) {
	fixture := craftFixture(t, map[string]craftedSlice{
		"core":      {files: map[string]string{"plugin.cfg": "[plugin]"}},
		"ios.arm64": {files: map[string]string{"bin/shared.so": "from ios"}},
		"macos":     {files: map[string]string{"bin/shared.so": "from macos"}},
	})
	server := fixture.serveRelease(t)

	forward := []slice.SliceID{
		slice.CoreSliceID(),
		{Platform: "ios", Architecture: "arm64"},
		{Platform: "macos"},
	}
	reversed := []slice.SliceID{
		slice.CoreSliceID(),
		{Platform: "macos"},
		{Platform: "ios", Architecture: "arm64"},
	}

	messages := make([]string, 0, 2)
	for _, order := range [][]slice.SliceID{forward, reversed} {
		staging := t.TempDir()
		fetcher := &slicedFetcher{resolve: releaseSliceResolver(releaseAssetsOf(fixture, server.URL), nil)}
		err := fetcher.mergeSlices(context.Background(), fixture.index, order, nil, staging)
		require.Error(t, err)
		var fetchError *output.FetchError
		require.ErrorAs(t, err, &fetchError)
		require.Equal(t, output.ExitFetch, output.CodeFor(err))
		require.Contains(t, err.Error(), "bin/shared.so")
		messages = append(messages, err.Error())
	}
	require.Equal(t, messages[0], messages[1],
		"collision detection runs against the accumulated set, so extraction order cannot change the message")
	require.Contains(t, messages[0], `slices "ios.arm64" and "macos"`)
}

func TestSlicedFetchRejectsTraversalInSliceArchive(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("../escape.txt")
	require.NoError(t, err)
	_, err = entry.Write([]byte("escaped"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	fixture := craftFixture(t, map[string]craftedSlice{
		"core":  {files: map[string]string{"plugin.cfg": "[plugin]"}},
		"macos": {body: buffer.Bytes()},
	})
	withHost(t, macOSHost)
	_, temporaryRoot, fetchErr := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, fetchErr, temporaryRoot, output.ExitInstall, "escapes target dir")
}

func TestSlicedFetchRejectsSymlinkInSliceArchive(t *testing.T) {
	var buffer bytes.Buffer
	gzipWriter := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gzipWriter)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{
		Name: "bin/evil", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777,
	}))
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())

	fixture := craftFixture(t, map[string]craftedSlice{
		"core":  {files: map[string]string{"plugin.cfg": "[plugin]"}},
		"macos": {file: "sliced-1.2.3-macos.tar.gz", body: buffer.Bytes()},
	})
	withHost(t, macOSHost)
	_, temporaryRoot, fetchErr := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, fetchErr, temporaryRoot, output.ExitInstall, "symlink")
}

// TestSlicedFetchAppliesDownloadCapPerSlice pins that --max-download-size bounds
// one archive, which is how a sliced addon can ship more bytes in total than any
// one of its slices is allowed to be.
func TestSlicedFetchAppliesDownloadCapPerSlice(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t)
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{},
		func(fetcher *GitHubReleaseFetcher) { fetcher.maxBytes = 64 })
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch, "exceeds maximum download size")
}

// TestSlicedFetchAppliesExtractCapAcrossSlices pins that --max-extract-size
// bounds the merged tree rather than each archive: each slice here fits the
// budget on its own and the set does not.
func TestSlicedFetchAppliesExtractCapAcrossSlices(t *testing.T) {
	half := strings.Repeat("a", 200)
	fixture := craftFixture(t, map[string]craftedSlice{
		"core":  {files: map[string]string{"core.bin": half}},
		"macos": {files: map[string]string{"bin/macos.bin": half}},
	})
	withHost(t, macOSHost)
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{},
		func(fetcher *GitHubReleaseFetcher) { fetcher.maxExtracted = 300 })
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch, "maximum extracted size of 300 bytes")

	// The same slices extract cleanly when the budget covers their sum, so the
	// cap failed for the aggregate rather than for either archive on its own.
	result, _, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{},
		func(fetcher *GitHubReleaseFetcher) { fetcher.maxExtracted = 500 })
	require.NoError(t, err)
	_ = os.RemoveAll(result.Dir)
}

func TestSlicedFetchRejectsExtensionMissingFromCore(t *testing.T) {
	fixture := craftFixture(t, map[string]craftedSlice{
		"core": {files: map[string]string{"plugin.cfg": "[plugin]"}},
		"macos": {
			files: map[string]string{"bin/addon_macos.dylib": "macos binary"},
			libraries: slice.ExtensionSectionTable[string]{
				fixtureExtensionPath: {
					"macos.template_release": slice.AddonResourceRoot(fixtureAddonName) + "/bin/addon_macos.dylib",
				},
			},
		},
	})
	withHost(t, macOSHost)
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch,
		fixtureExtensionPath, `the "core" slice ships no such file`)
}

func TestSlicedFetchRejectsUnpartitionedCoreBody(t *testing.T) {
	fixture := craftFixture(t, map[string]craftedSlice{
		// The core body still carries its platform-tagged entries, so it was
		// published without being partitioned.
		"core": {files: map[string]string{fixtureExtensionPath: fixtureExtension}},
		"macos": {
			files: map[string]string{"bin/addon_macos.dylib": "macos binary"},
			libraries: slice.ExtensionSectionTable[string]{
				fixtureExtensionPath: {
					"macos.template_release": slice.AddonResourceRoot(fixtureAddonName) + "/bin/addon_macos.dylib",
				},
			},
		},
	})
	withHost(t, macOSHost)
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch, "without being partitioned")
}

// TestSlicedFetchRejectsLibraryOutsideAddonRoot pins the containment rule no
// earlier issue could apply: gpm-index.toml declares no addon root, so an entry
// value naming a clean res:// path anywhere in the project passes the loader and
// is only caught here, where the installed root is known.
func TestSlicedFetchRejectsLibraryOutsideAddonRoot(t *testing.T) {
	outside := "res://addons/other/bin/addon_macos.dylib"
	fixture := craftFixture(t, map[string]craftedSlice{
		"core": {files: map[string]string{fixtureExtensionPath: partitionedCoreBody(t)}},
		"macos": {
			files: map[string]string{"bin/addon_macos.dylib": "macos binary"},
			libraries: slice.ExtensionSectionTable[string]{
				fixtureExtensionPath: {"macos.template_release": outside},
			},
		},
	})
	withHost(t, macOSHost)
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch,
		`index slice "macos"`, `libraries entry "macos.template_release"`,
		fixtureExtensionPath, outside, "outside the addon root")
}

// TestSlicedFetchRejectsSiblingPrefixOutsideAddonRoot exercises a path only the
// shared containment comparison rejects: it is a clean res:// path, so every
// grammar rule accepts it, and it shares the addon root as a string prefix
// without being inside it.
func TestSlicedFetchRejectsSiblingPrefixOutsideAddonRoot(t *testing.T) {
	root := slice.AddonResourceRoot(fixtureAddonName)
	sibling := root + "x/bin/addon_macos.dylib"
	require.True(t, strings.HasPrefix(sibling, root), "the test needs a path sharing the root as a prefix")

	fixture := craftFixture(t, map[string]craftedSlice{
		"core": {files: map[string]string{fixtureExtensionPath: partitionedCoreBody(t)}},
		"macos": {
			files: map[string]string{"bin/addon_macos.dylib": "macos binary"},
			libraries: slice.ExtensionSectionTable[string]{
				fixtureExtensionPath: {"macos.template_release": sibling},
			},
		},
	})
	withHost(t, macOSHost)
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture, manifest.AddonSpec{}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch, sibling, "outside the addon root")
}

// TestSlicedFetchRejectsDependencyKeyOutsideAddonRoot pins that a
// [dependencies] Dictionary's keys obey the containment rule, and that its
// export destinations do not: a destination is a path relative to Godot's export
// directory rather than a path in the project.
func TestSlicedFetchRejectsDependencyKeyOutsideAddonRoot(t *testing.T) {
	root := slice.AddonResourceRoot(fixtureAddonName)
	outside := "res://addons/other/bin/libgodot.a"
	fixture := craftFixture(t, map[string]craftedSlice{
		"core": {files: map[string]string{fixtureExtensionPath: partitionedCoreBody(t)}},
		"ios.arm64": {
			files: map[string]string{"bin/addon_ios.dylib": "ios binary"},
			libraries: slice.ExtensionSectionTable[string]{
				fixtureExtensionPath: {"ios.template_release.arm64": root + "/bin/addon_ios.dylib"},
			},
			dependencies: slice.ExtensionSectionTable[slice.ExtensionDependencyTargets]{
				fixtureExtensionPath: {"ios.template_release.arm64": {outside: ""}},
			},
		},
	})
	withHost(t, unpublishedHost)
	_, temporaryRoot, err := fetchSlicedRelease(t, fixture,
		manifest.AddonSpec{Platforms: []string{"ios.arm64"}}, nil)
	requireFetchFailure(t, err, temporaryRoot, output.ExitFetch,
		`index slice "ios.arm64"`, `dependencies entry "ios.template_release.arm64"`,
		outside, "outside the addon root")
}

// TestSlicedFetchKeepsDependencyExportDestination is the other half of that
// rule: a destination naming a directory that would never be a path inside the
// addon is carried through untouched, because it is not a project path at all.
func TestSlicedFetchKeepsDependencyExportDestination(t *testing.T) {
	root := slice.AddonResourceRoot(fixtureAddonName)
	destination := "addons/other/Frameworks"
	fixture := craftFixture(t, map[string]craftedSlice{
		"core": {files: map[string]string{fixtureExtensionPath: partitionedCoreBody(t)}},
		"ios.arm64": {
			files: map[string]string{
				"bin/addon_ios.dylib": "ios binary",
				"bin/libgodot_ios.a":  "ios dependency",
			},
			libraries: slice.ExtensionSectionTable[string]{
				fixtureExtensionPath: {"ios.template_release.arm64": root + "/bin/addon_ios.dylib"},
			},
			dependencies: slice.ExtensionSectionTable[slice.ExtensionDependencyTargets]{
				fixtureExtensionPath: {
					"ios.template_release.arm64": {root + "/bin/libgodot_ios.a": destination},
				},
			},
		},
	})
	withHost(t, unpublishedHost)
	result, _, err := fetchSlicedRelease(t, fixture,
		manifest.AddonSpec{Platforms: []string{"ios.arm64"}, InstallAs: "renamed"}, nil)
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()

	installed, err := os.ReadFile(filepath.Join(result.Dir, fixtureExtensionPath))
	require.NoError(t, err)
	require.Contains(t, string(installed), destination,
		"an export destination is not a project path, so it is neither checked nor re-rooted")
	require.Contains(t, string(installed), "res://addons/renamed/bin/libgodot_ios.a")
}

// TestSlicedFetchAcceptsPathsInsideAddonRoot pins that the new rule adds no
// false rejection: the real producer's own index names only paths inside the
// addon and installs unchanged.
func TestSlicedFetchAcceptsPathsInsideAddonRoot(t *testing.T) {
	withHost(t, macOSHost)
	fixture := packFixture(t)
	result, _, err := fetchSlicedRelease(t, fixture,
		manifest.AddonSpec{Platforms: []string{"ios.arm64", "linux.x86_64"}}, nil)
	require.NoError(t, err)
	defer func() { _ = os.RemoveAll(result.Dir) }()
	require.Equal(t, []string{"core", "ios.arm64", "linux.x86_64", "macos"},
		renderSliceIDs(result.InstalledSlices))
}

// releaseAssetsOf renders a fixture's assets the way the release JSON does, for
// the tests that drive the merge directly rather than through Fetch.
func releaseAssetsOf(fixture slicedFixture, baseURL string) []ghAsset {
	assets := make([]ghAsset, 0, len(fixture.assets))
	for name := range fixture.assets {
		assets = append(assets, ghAsset{Name: name, APIURL: baseURL + "/assets/" + name})
	}
	return assets
}

// TestSlicedMergeRejectsCaseVariantPathsAcrossSlices pins that collision
// detection keys on the identity of the destination file rather than on the
// archive's exact spelling. On a case-insensitive filesystem the two paths here
// are one file, so an exact-spelling key would let the second slice truncate
// what the first wrote; refusing the pair on every host is what keeps one index
// installing one tree everywhere.
func TestSlicedMergeRejectsCaseVariantPathsAcrossSlices(t *testing.T) {
	fixture := craftFixture(t, map[string]craftedSlice{
		"core":      {files: map[string]string{"plugin.cfg": "[plugin]"}},
		"ios.arm64": {files: map[string]string{"bin/Shared.so": "from ios"}},
		"macos":     {files: map[string]string{"bin/shared.so": "from macos"}},
	})
	server := fixture.serveRelease(t)

	iosFirst := []slice.SliceID{
		slice.CoreSliceID(),
		{Platform: "ios", Architecture: "arm64"},
		{Platform: "macos"},
	}
	macosFirst := []slice.SliceID{
		slice.CoreSliceID(),
		{Platform: "macos"},
		{Platform: "ios", Architecture: "arm64"},
	}

	messages := make([]string, 0, 2)
	for _, order := range [][]slice.SliceID{iosFirst, macosFirst} {
		fetcher := &slicedFetcher{resolve: releaseSliceResolver(releaseAssetsOf(fixture, server.URL), nil)}
		err := fetcher.mergeSlices(context.Background(), fixture.index, order, nil, t.TempDir())
		require.Error(t, err)
		var fetchError *output.FetchError
		require.ErrorAs(t, err, &fetchError)
		require.Equal(t, output.ExitFetch, output.CodeFor(err))
		messages = append(messages, err.Error())
	}
	require.Equal(t, messages[0], messages[1])
	require.Contains(t, messages[0], `slices "ios.arm64" and "macos" ship "bin/Shared.so" and "bin/shared.so"`)
	require.Contains(t, messages[0], "case-insensitive")
}

// TestSlicedFetchRejectsManifestArchiveChecksum pins that a `checksum` written
// for an addon that turns out to be sliced is reported rather than ignored. A
// sliced fetch has no single archive to compare it against, so honoring the
// field is impossible and silently dropping it would leave an explicitly
// written verification directive inert.
//
// It fires on both sliced sources, before anything is downloaded, and it is a
// manifest failure: the remote content is fine, the declaration is wrong.
func TestSlicedFetchRejectsManifestArchiveChecksum(t *testing.T) {
	declared := strings.Repeat("ab", 32)

	t.Run("github-release", func(t *testing.T) {
		withHost(t, macOSHost)
		fixture := packFixture(t)
		_, temporaryRoot, err := fetchSlicedRelease(t, fixture,
			manifest.AddonSpec{Checksum: declared}, nil)
		requireManifestChecksumRejection(t, err, temporaryRoot)
	})

	t.Run("archive", func(t *testing.T) {
		withHost(t, macOSHost)
		temporaryRoot := t.TempDir()
		t.Setenv("TMPDIR", temporaryRoot)
		fixture := packFixture(t)
		server := fixture.serveArchiveHost(t)
		_, err := (&ArchiveFetcher{}).Fetch(context.Background(), manifest.AddonSpec{
			Name:     fixtureAddonName,
			Source:   manifest.SourceArchive,
			URL:      server.URL + "/dist/whole-addon.zip",
			Index:    server.URL + "/dist/" + slice.IndexFileName,
			Version:  fixtureVersion,
			Checksum: declared,
		})
		requireManifestChecksumRejection(t, err, temporaryRoot)
	})
}

func requireManifestChecksumRejection(t *testing.T, err error, temporaryRoot string) {
	t.Helper()
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.ErrorAs(t, err, &manifestError)
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
	require.Contains(t, err.Error(), `addon "sliced" declares a checksum`)
	require.Contains(t, err.Error(), "pins one archive")
	require.Contains(t, err.Error(), "publishes several")
	require.Contains(t, err.Error(), "index_sha256")
	// No staging directory and no downloaded archive: the declaration is
	// rejected before any slice is fetched, not after.
	requireTemporaryRootEmpty(t, temporaryRoot)
}

// TestSlicedFetchRejectsTwoArchitectureSlicesDisagreeingAboutASharedPath covers
// the one duplication the merge accepts, gone wrong. Two architecture slices of
// one platform may share a path, because that is what the fan-out produces, but
// either may be selected alone, so there is no authoritative copy when their
// contents differ.
func TestSlicedFetchRejectsTwoArchitectureSlicesDisagreeingAboutASharedPath(t *testing.T) {
	fixture := craftFixture(t, map[string]craftedSlice{
		"core":            {files: map[string]string{"plugin.cfg": "[plugin]"}},
		"macos.arm64":     {files: map[string]string{"bin/shared.so": "from arm64"}},
		"macos.universal": {files: map[string]string{"bin/shared.so": "from universal"}},
	})
	server := fixture.serveRelease(t)

	selected := []slice.SliceID{
		slice.CoreSliceID(),
		{Platform: "macos", Architecture: "arm64"},
		{Platform: "macos", Architecture: "universal"},
	}
	fetcher := &slicedFetcher{resolve: releaseSliceResolver(releaseAssetsOf(fixture, server.URL), nil)}
	err := fetcher.mergeSlices(context.Background(), fixture.index, selected, nil, t.TempDir())

	var fetchError *output.FetchError
	require.ErrorAs(t, err, &fetchError)
	require.Equal(t, output.ExitFetch, output.CodeFor(err))
	require.Contains(t, err.Error(), "bin/shared.so")
	require.Contains(t, err.Error(), "with different contents")
}

// TestSlicedFetchRejectsUnrelatedSlicesSharingAnIdenticalPath keeps the
// fan-out's exemption as narrow as the packager's own claim rule. Identical
// bytes are not what makes a shared path legitimate; being two architecture
// slices of one platform is, and these are two different platforms.
func TestSlicedFetchRejectsUnrelatedSlicesSharingAnIdenticalPath(t *testing.T) {
	fixture := craftFixture(t, map[string]craftedSlice{
		"core":        {files: map[string]string{"plugin.cfg": "[plugin]"}},
		"ios.arm64":   {files: map[string]string{"bin/shared.so": "same bytes"}},
		"macos.arm64": {files: map[string]string{"bin/shared.so": "same bytes"}},
	})
	server := fixture.serveRelease(t)

	selected := []slice.SliceID{
		slice.CoreSliceID(),
		{Platform: "ios", Architecture: "arm64"},
		{Platform: "macos", Architecture: "arm64"},
	}
	fetcher := &slicedFetcher{resolve: releaseSliceResolver(releaseAssetsOf(fixture, server.URL), nil)}
	err := fetcher.mergeSlices(context.Background(), fixture.index, selected, nil, t.TempDir())

	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot come from two slices")
}
