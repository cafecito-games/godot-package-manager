package source

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/packager"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// fixtureAddonName is the addon every sliced fixture publishes, and therefore
// the directory its index's entry values are written against.
const fixtureAddonName = "sliced"

// fixtureVersion is the version every sliced fixture publishes.
const fixtureVersion = "1.2.3"

// fixtureExtensionPath is the fixture's one .gdextension, relative to the addon
// root, which is also its index section path key.
const fixtureExtensionPath = "sliced.gdextension"

// fixtureExtension is the fixture addon's .gdextension before packaging. It
// covers the shapes a consumer has to merge: an architecture-less platform, four
// architecture-specific ones, and a [dependencies] entry whose platform tag also
// appears in [libraries].
const fixtureExtension = `[configuration]

entry_symbol = "sliced_main"
compatibility_minimum = "4.2"

[libraries]

macos.template_release = "res://addons/sliced/bin/addon_macos.dylib"
ios.template_release.arm64 = "res://addons/sliced/bin/addon_ios.dylib"
linux.template_release.x86_64 = "res://addons/sliced/bin/addon_linux.so"
windows.template_release.x86_64 = "res://addons/sliced/bin/addon_windows.dll"
android.template_release.arm64 = "res://addons/sliced/bin/addon_android.so"
web.template_release.wasm32 = "res://addons/sliced/bin/addon_web.wasm"

[dependencies]

ios.template_release.arm64 = { "res://addons/sliced/bin/libgodot_ios.a" : "" }
`

// fixtureBinaries maps every file the fixture's .gdextension names, relative to
// the addon root, to a body distinctive enough for a test to recognize.
var fixtureBinaries = map[string]string{
	"bin/addon_macos.dylib":   "macos binary",
	"bin/addon_ios.dylib":     "ios binary",
	"bin/addon_linux.so":      "linux binary",
	"bin/addon_windows.dll":   "windows binary",
	"bin/addon_android.so":    "android binary",
	"bin/addon_web.wasm":      "web binary",
	"bin/libgodot_ios.a":      "ios dependency",
	"plugin.cfg":              "[plugin]\nname=\"sliced\"\n",
	"scripts/sliced_tool.gd":  "extends Node\n",
	"scripts/sliced_util.gd":  "extends RefCounted\n",
	"scripts/sliced_more.gd":  "extends Object\n",
	"scripts/sliced_extra.gd": "extends Node2D\n",
}

// slicedFixture is one real `gpm package` run's output, held in memory: the
// index exactly as the producer wrote it, and each archive's bytes keyed by the
// asset name the index names it by.
//
// The fixtures are produced by internal/packager rather than hand-written,
// because the consumer's whole job is to read what the producer writes, and a
// hand-written index can agree with a consumer that has drifted from it.
type slicedFixture struct {
	indexBytes []byte
	index      *slice.Index
	assets     map[string][]byte
}

// packFixture runs the real packager over a synthetic addon repository and
// returns its output in memory.
func packFixture(t *testing.T) slicedFixture {
	t.Helper()
	root := t.TempDir()
	config := fmt.Sprintf(`[package]
name = %q
addon_path = %q
version = %q
`, fixtureAddonName, slice.AddonInstallPath(fixtureAddonName), fixtureVersion)
	require.NoError(t, os.WriteFile(filepath.Join(root, packager.ConfigFileName), []byte(config), 0o644))

	addonRoot := filepath.Join(root, filepath.FromSlash(slice.AddonInstallPath(fixtureAddonName)))
	writeFixtureFile(t, addonRoot, fixtureExtensionPath, fixtureExtension)
	for path, content := range fixtureBinaries {
		writeFixtureFile(t, addonRoot, path, content)
	}

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)

	indexBytes, err := os.ReadFile(result.Index)
	require.NoError(t, err)
	index, err := slice.LoadIndex(indexBytes)
	require.NoError(t, err)

	fixture := slicedFixture{indexBytes: indexBytes, index: index, assets: map[string][]byte{}}
	for _, published := range result.Slices {
		body, err := os.ReadFile(filepath.Join(filepath.Dir(result.Index), published.File))
		require.NoError(t, err)
		fixture.assets[published.File] = body
	}
	return fixture
}

func writeFixtureFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// withIndex returns a copy of the fixture whose index bytes are the given
// document, for the fail-closed cases that need an index the producer would
// never write. The archives are the producer's own, so only the one property
// under test differs from a healthy fixture.
func (fixture slicedFixture) withIndex(indexBytes []byte) slicedFixture {
	replaced := fixture
	replaced.indexBytes = indexBytes
	return replaced
}

// mutateIndex re-saves the fixture's index after applying mutate to the parsed
// document, which is how a fail-closed test produces an index that differs from
// the producer's in exactly one field. The result is written through the index's
// own writer, so it is a document LoadIndex accepts.
func (fixture slicedFixture) mutateIndex(t *testing.T, mutate func(*slice.Index)) slicedFixture {
	t.Helper()
	reparsed, err := slice.LoadIndex(fixture.indexBytes)
	require.NoError(t, err)
	mutate(reparsed)
	path := filepath.Join(t.TempDir(), slice.IndexFileName)
	require.NoError(t, reparsed.Save(path))
	mutated, err := os.ReadFile(path)
	require.NoError(t, err)
	return fixture.withIndex(mutated)
}

// serveRelease publishes the fixture as a GitHub release: the index and every
// archive as release assets, each downloadable from its own asset API URL.
func (fixture slicedFixture) serveRelease(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	assetNames := []string{slice.IndexFileName}
	for name := range fixture.assets {
		assetNames = append(assetNames, name)
	}
	mux.HandleFunc("/repos/owner/repo/releases/tags/"+fixtureVersion,
		func(w http.ResponseWriter, r *http.Request) {
			assets := make([]map[string]any, 0, len(assetNames))
			for _, name := range assetNames {
				assets = append(assets, map[string]any{
					"name": name,
					"url":  "http://" + r.Host + "/assets/" + name,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"assets": assets})
		})
	mux.HandleFunc("/assets/", func(w http.ResponseWriter, r *http.Request) {
		fixture.writeAsset(w, filepath.Base(r.URL.Path))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// serveArchiveHost publishes the fixture as plain URLs under /dist, which is
// what an `archive` source with an `index` reads.
func (fixture slicedFixture) serveArchiveHost(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/dist/", func(w http.ResponseWriter, r *http.Request) {
		fixture.writeAsset(w, filepath.Base(r.URL.Path))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func (fixture slicedFixture) writeAsset(w http.ResponseWriter, name string) {
	if name == slice.IndexFileName {
		_, _ = w.Write(fixture.indexBytes)
		return
	}
	body, found := fixture.assets[name]
	if !found {
		http.Error(w, "no such asset", http.StatusNotFound)
		return
	}
	_, _ = w.Write(body)
}

// withHost makes the sliced path resolve a fixed machine for the duration of a
// test, so a result never depends on which machine runs the suite.
func withHost(t *testing.T, host slice.Host) {
	t.Helper()
	previous := slice.CurrentHost
	slice.CurrentHost = func() slice.Host { return host }
	t.Cleanup(func() { slice.CurrentHost = previous })
}

// macOSHost is the machine most sliced tests simulate: it is the one host whose
// published slice is architecture-less, which is the case a candidate chain
// exists for.
var macOSHost = slice.Host{OperatingSystem: "darwin", Architecture: "arm64"}

// unpublishedHost is a machine the fixture publishes no slice for, so a test can
// exercise the unsupported-host diagnostic without depending on the suite's own
// machine.
var unpublishedHost = slice.Host{OperatingSystem: "plan9", Architecture: "mips"}

// treeFiles lists every regular file under root as a slash-separated path
// relative to it, which is how a test asserts what a merge did and did not
// install.
func treeFiles(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(relative))
		return nil
	}))
	return found
}

// zipArchive builds a zip in memory and reports its bytes, SHA-256, and size,
// which is what an index slice declares about an archive.
func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range files {
		entry, err := writer.Create(name)
		require.NoError(t, err)
		_, err = entry.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buffer.Bytes()
}
