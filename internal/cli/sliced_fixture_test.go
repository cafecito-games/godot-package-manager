package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/packager"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// The sliced fixtures below are produced by the real packager rather than
// hand-written, because the CLI's whole job is to reconcile what the producer
// published against what this disk holds, and a hand-written index can agree
// with a consumer that has drifted from it.
const (
	slicedAddonName     = "sliced"
	slicedVersion       = "1.2.3"
	slicedExtensionPath = "sliced.gdextension"
)

// slicedExtension covers the shapes reconciliation has to distinguish: an
// architecture-less platform that is one host's slice, two architecture-specific
// platforms that are other hosts' slices, and one that is no test host's.
const slicedExtension = `[configuration]

entry_symbol = "sliced_main"
compatibility_minimum = "4.2"

[libraries]

macos.template_release = "res://addons/sliced/bin/addon_macos.dylib"
linux.template_release.x86_64 = "res://addons/sliced/bin/addon_linux.so"
windows.template_release.x86_64 = "res://addons/sliced/bin/addon_windows.dll"
ios.template_release.arm64 = "res://addons/sliced/bin/addon_ios.dylib"
`

// slicedBinaries maps every file slicedExtension names, relative to the addon
// root, to a body distinctive enough for a test to recognize.
var slicedBinaries = map[string]string{
	"bin/addon_macos.dylib":   "macos binary",
	"bin/addon_linux.so":      "linux binary",
	"bin/addon_windows.dll":   "windows binary",
	"bin/addon_ios.dylib":     "ios binary",
	"plugin.cfg":              "[plugin]\nname=\"sliced\"\n",
	"scripts/sliced_tool.gd":  "extends Node\n",
	"scripts/sliced_util.gd":  "extends RefCounted\n",
	"scripts/sliced_extra.gd": "extends Object\n",
}

// hostA and hostB are the two machines the cross-host gate simulates: each
// publishes a different host slice, and neither is the machine running the
// suite, so the result never depends on where the tests run.
var (
	hostA = slice.Host{OperatingSystem: "darwin", Architecture: "arm64"}
	hostB = slice.Host{OperatingSystem: "linux", Architecture: "amd64"}
	// unpublishedHost has no candidate chain at all, which is how an addon
	// publishing nothing for this machine is exercised deterministically.
	unpublishedHost = slice.Host{OperatingSystem: "plan9", Architecture: "mips"}
)

// withHost makes the CLI resolve a fixed machine for the duration of a test.
func withHost(t *testing.T, host slice.Host) {
	t.Helper()
	previous := slice.CurrentHost
	slice.CurrentHost = func() slice.Host { return host }
	t.Cleanup(func() { slice.CurrentHost = previous })
}

// slicedFixture is one real `gpm package` run's output held in memory: the index
// exactly as the producer wrote it, and each archive's bytes keyed by the asset
// name the index names it by.
type slicedFixture struct {
	indexBytes []byte
	assets     map[string][]byte
}

// packSlicedFixture packages a synthetic addon whose core slice carries marker,
// so two fixtures of the same version differ in their archive bytes, their
// per-slice checksums, and their index digest while each stays self-consistent.
// That is exactly the shape of a retagged release, which is what the lock's
// index and slice pins exist to refuse.
func packSlicedFixture(t *testing.T, marker string) slicedFixture {
	t.Helper()
	root := t.TempDir()
	config := fmt.Sprintf(`[package]
name = %q
addon_path = %q
version = %q
`, slicedAddonName, slice.AddonInstallPath(slicedAddonName), slicedVersion)
	require.NoError(t, os.WriteFile(filepath.Join(root, packager.ConfigFileName), []byte(config), 0o644))

	addonRoot := filepath.Join(root, filepath.FromSlash(slice.AddonInstallPath(slicedAddonName)))
	writeTestFile(t, addonRoot, slicedExtensionPath, slicedExtension)
	for path, content := range slicedBinaries {
		writeTestFile(t, addonRoot, path, content)
	}
	writeTestFile(t, addonRoot, "scripts/marker.gd", "# "+marker+"\n")

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)

	indexBytes, err := os.ReadFile(result.Index)
	require.NoError(t, err)
	fixture := slicedFixture{indexBytes: indexBytes, assets: map[string][]byte{}}
	for _, published := range result.Slices {
		body, err := os.ReadFile(filepath.Join(filepath.Dir(result.Index), published.File))
		require.NoError(t, err)
		fixture.assets[published.File] = body
	}
	return fixture
}

func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// slicedPublisher serves a fixture over HTTP as a plain archive host, counting
// requests so a test can assert that a satisfied install fetched nothing, and
// allowing the fixture to be swapped so a test can serve a different, equally
// self-consistent publication under an unchanged manifest.
type slicedPublisher struct {
	mutex    sync.Mutex
	fixture  slicedFixture
	requests int
	url      string
}

func servePublisher(t *testing.T, fixture slicedFixture) *slicedPublisher {
	t.Helper()
	publisher := &slicedPublisher{fixture: fixture}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publisher.mutex.Lock()
		publisher.requests++
		current := publisher.fixture
		publisher.mutex.Unlock()

		name := filepath.Base(r.URL.Path)
		if name == slice.IndexFileName {
			_, _ = w.Write(current.indexBytes)
			return
		}
		body, found := current.assets[name]
		if !found {
			http.Error(w, "no such asset", http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	publisher.url = server.URL
	return publisher
}

func (p *slicedPublisher) publish(fixture slicedFixture) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.fixture = fixture
}

func (p *slicedPublisher) requestCount() int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.requests
}

func (p *slicedPublisher) indexURL() string {
	return p.url + "/dist/" + slice.IndexFileName
}

// archiveURL is the manifest's `url`, which an archive source requires. It is
// never fetched on the sliced path, where the index names the archives.
func (p *slicedPublisher) archiveURL() string {
	return p.url + "/dist/" + slicedAddonName + ".zip"
}

// newSlicedProject writes a Godot project declaring the fixture addon with the
// given project platform list, and returns the project root.
func newSlicedProject(t *testing.T, publisher *slicedPublisher, platforms ...string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), nil, 0o644))
	writeSlicedManifest(t, root, publisher, platforms...)
	return root
}

func writeSlicedManifest(t *testing.T, root string, publisher *slicedPublisher, platforms ...string) {
	t.Helper()
	rendered := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		rendered = append(rendered, fmt.Sprintf("%q", platform))
	}
	body := fmt.Sprintf(`[project]
platforms = [%s]

[addons]
[addons.%s]
source  = "archive"
url     = %q
index   = %q
version = %q
`, strings.Join(rendered, ", "), slicedAddonName, publisher.archiveURL(), publisher.indexURL(), slicedVersion)
	require.NoError(t, os.WriteFile(filepath.Join(root, "addons.toml"), []byte(body), 0o644))
}

// installedFiles lists every regular file under the installed addon as a
// slash-separated path, which is how a test asserts what a selection mode did
// and did not materialize.
func installedFiles(t *testing.T, projectRoot string) []string {
	t.Helper()
	root := filepath.Join(projectRoot, "addons", slicedAddonName)
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

func readFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
