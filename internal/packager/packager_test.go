package packager_test

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/packager"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// multiPlatformExtension is a synthetic .gdextension covering the shapes the
// packager has to partition: one architecture-specific platform, one
// architecture-less platform, and a [dependencies] section whose platform tag
// also appears in [libraries].
const multiPlatformExtension = `[configuration]

entry_symbol = "addon_main"
compatibility_minimum = "4.2"

[libraries]

windows.template_release.x86_64 = "res://addons/addon/bin/addon_windows.dll"
linux.template_release.x86_64 = "bin/addon_linux.so"
ios.template_release.arm64 = "bin/addon_ios.dylib"

[dependencies]

ios.template_release.arm64 = { "bin/libgodot-cpp_ios.a" : "", "bin/libextra_ios.a" : "Frameworks" }
`

// writeAddon lays out a repository with gpm-package.toml and an addon subtree.
// Each files entry maps a path relative to the addon subtree to its content.
func writeAddon(t *testing.T, config string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, packager.ConfigFileName), []byte(config), 0o644))
	for path, content := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	return root
}

// archiveEntries lists an archive's entry names in the order they appear.
func archiveEntries(t *testing.T, path string) []string {
	t.Helper()
	reader, err := zip.OpenReader(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	names := make([]string, 0, len(reader.File))
	for _, entry := range reader.File {
		names = append(names, entry.Name)
	}
	return names
}

// archiveContent returns one entry's bytes.
func archiveContent(t *testing.T, path, name string) []byte {
	t.Helper()
	reader, err := zip.OpenReader(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	for _, entry := range reader.File {
		if entry.Name != name {
			continue
		}
		opened, err := entry.Open()
		require.NoError(t, err)
		content, err := io.ReadAll(opened)
		require.NoError(t, err)
		require.NoError(t, opened.Close())
		return content
	}
	t.Fatalf("archive %s has no entry %q", path, name)
	return nil
}

// loadEmittedIndex loads the index the packager wrote through the schema's own
// loader, which is the only parser of gpm-index.toml.
func loadEmittedIndex(t *testing.T, path string) *slice.Index {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	index, err := slice.LoadIndex(data)
	require.NoError(t, err)
	return index
}

func sliceIDs(index *slice.Index) []string {
	keys := make([]string, 0, len(index.Slices))
	for key := range index.Slices {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func entryKeys[Value any](table slice.ExtensionEntryTable[Value]) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestPackageProducesOneArchivePerSlicePlusCore(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`, map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/icon.svg":               "<svg/>\n",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	require.Equal(t, "addon", result.Name)
	require.Equal(t, "1.2.3", result.Version)

	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, slice.SupportedIndexFormat, index.Format)
	require.Equal(t, []string{"core", "ios.arm64", "linux.x86_64", "windows.x86_64"}, sliceIDs(index))

	outputDirectory := filepath.Join(root, "dist")
	require.Equal(t,
		[]string{"addon.gdextension", "icon.svg", "plugin.gd"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.2.3-core.zip")),
	)
	require.Equal(t,
		[]string{"bin/addon_windows.dll"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.2.3-windows.x86_64.zip")),
	)
	require.Equal(t,
		[]string{"bin/addon_ios.dylib", "bin/libextra_ios.a", "bin/libgodot-cpp_ios.a"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.2.3-ios.arm64.zip")),
	)
}

func TestPackageShipsEachGdextensionPartitionedIntoCore(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`, map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)

	core := string(archiveContent(t, filepath.Join(root, "dist", "addon-1.2.3-core.zip"), "addon.gdextension"))
	require.Contains(t, core, `entry_symbol="addon_main"`)
	require.NotContains(t, core, "addon_windows.dll")
	require.NotContains(t, core, "libgodot-cpp_ios.a")
	require.FileExists(t, result.Index)

	// The author's own working file is untouched.
	authored, err := os.ReadFile(filepath.Join(root, "addons", "addon", "addon.gdextension"))
	require.NoError(t, err)
	require.Equal(t, multiPlatformExtension, string(authored))
}

func TestPackageEmitsTheNestedDependenciesShape(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`, map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)

	iosSlice := index.Slices["ios.arm64"]
	require.NotNil(t, iosSlice)
	require.Equal(t,
		slice.ExtensionEntryTable[string]{"ios.template_release.arm64": "res://addons/addon/bin/addon_ios.dylib"},
		iosSlice.Libraries["addon.gdextension"],
	)
	require.Equal(t,
		slice.ExtensionEntryTable[slice.ExtensionDependencyTargets]{
			"ios.template_release.arm64": {
				"res://addons/addon/bin/libgodot-cpp_ios.a": "",
				"res://addons/addon/bin/libextra_ios.a":     "Frameworks",
			},
		},
		iosSlice.Dependencies["addon.gdextension"],
	)
	// A non-empty destination names an export subdirectory, not a path in the
	// addon tree, so nothing is created for it.
	_, err = os.Stat(filepath.Join(root, "dist", "Frameworks"))
	require.True(t, os.IsNotExist(err))
}

func TestPackageRoundTripsItsOwnIndexThroughTheSchema(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`, map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)

	emitted, err := os.ReadFile(result.Index)
	require.NoError(t, err)
	index, err := slice.LoadIndex(emitted)
	require.NoError(t, err)

	resaved := filepath.Join(t.TempDir(), "gpm-index.toml")
	require.NoError(t, index.Save(resaved))
	resavedBytes, err := os.ReadFile(resaved)
	require.NoError(t, err)
	require.Equal(t, string(emitted), string(resavedBytes))
}

func TestPackageRecordsEachArchivesRealChecksumAndSize(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`, map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.Len(t, result.Slices, len(index.Slices))

	for _, key := range sliceIDs(index) {
		published := index.Slices[key]
		path := filepath.Join(root, "dist", published.File)
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		digest := sha256.Sum256(content)
		require.Equal(t, hex.EncodeToString(digest[:]), published.SHA256, "slice %s", key)
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, info.Size(), published.Size, "slice %s", key)
	}

	for _, reported := range result.Slices {
		published := index.Slices[reported.ID]
		require.NotNil(t, published, "slice %s", reported.ID)
		require.Equal(t, published.File, reported.File)
		require.Equal(t, published.SHA256, reported.SHA256)
		require.Equal(t, published.Size, reported.Size)
	}
}

func TestPackageIsReproducibleAcrossRunsAndPerturbation(t *testing.T) {
	files := map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
	}
	config := `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`
	root := writeAddon(t, config, files)

	first := t.TempDir()
	_, err := packager.Package(packager.Options{Directory: root, OutputDirectory: first})
	require.NoError(t, err)

	// Perturb what must not reach an archive: every file's modification time, and
	// the execute bit of a file that carries no code.
	require.NoError(t, filepath.Walk(filepath.Join(root, "addons"), func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		if info.IsDir() {
			return nil
		}
		return os.Chtimes(path, time.Unix(1, 0), time.Unix(1, 0))
	}))
	require.NoError(t, os.Chmod(filepath.Join(root, "addons", "addon", "plugin.gd"), 0o600))

	second := t.TempDir()
	_, err = packager.Package(packager.Options{Directory: root, OutputDirectory: second})
	require.NoError(t, err)

	entries, err := os.ReadDir(first)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	for _, entry := range entries {
		firstBytes, err := os.ReadFile(filepath.Join(first, entry.Name()))
		require.NoError(t, err)
		secondBytes, err := os.ReadFile(filepath.Join(second, entry.Name()))
		require.NoError(t, err)
		require.Equal(t, firstBytes, secondBytes, "%s is not reproducible", entry.Name())
	}
}

func TestPackageAssertsTheReproducibilityLeversDirectly(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`, map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
		"addons/addon/empty/.keep":            "",
	})
	require.NoError(t, os.Chmod(filepath.Join(root, "addons", "addon", "bin", "addon_linux.so"), 0o744))

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)

	for _, published := range result.Slices {
		reader, err := zip.OpenReader(filepath.Join(filepath.Dir(result.Index), published.File))
		require.NoError(t, err)
		previous := ""
		for _, entry := range reader.File {
			require.False(t, entry.FileInfo().IsDir(), "%s holds a directory entry", published.File)
			require.Greater(t, entry.Name, previous, "%s entries are not sorted", published.File)
			previous = entry.Name
			require.Equal(t,
				time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC), entry.Modified.UTC(),
				"%s entry %s carries a source timestamp", published.File, entry.Name,
			)
			require.Contains(t,
				[]os.FileMode{0o644, 0o755}, entry.Mode().Perm(),
				"%s entry %s carries an unnormalized mode", published.File, entry.Name,
			)
			require.Equal(t, uint16(zip.Deflate), entry.Method)
		}
		require.NoError(t, reader.Close())
	}

	linux := filepath.Join(filepath.Dir(result.Index), "addon-1.2.3-linux.x86_64.zip")
	reader, err := zip.OpenReader(linux)
	require.NoError(t, err)
	defer func() { require.NoError(t, reader.Close()) }()
	require.Len(t, reader.File, 1)
	require.Equal(t, os.FileMode(0o755), reader.File[0].Mode().Perm())
}

func TestPackagePlacesExtrasInTheNamedSlice(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"

[package.slices]
"android.arm64" = ["android/addon.aar"]
"ios.arm64"     = ["ios/Addon.xcframework/**"]
`, map[string]string{
		"addons/addon/plugin.gd":                                       "extends Node\n",
		"addons/addon/android/addon.aar":                               "aar",
		"addons/addon/ios/Addon.xcframework/Info.plist":                "plist",
		"addons/addon/ios/Addon.xcframework/ios-arm64/Addon":           "binary",
		"addons/addon/ios/Addon.xcframework/ios-arm64/Headers/addon.h": "header",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, []string{"android.arm64", "core", "ios.arm64"}, sliceIDs(index))
	require.Empty(t, index.Slices["android.arm64"].Libraries)

	outputDirectory := filepath.Dir(result.Index)
	require.Equal(t, []string{"android/addon.aar"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.2.3-android.arm64.zip")))
	require.Equal(t, []string{
		"ios/Addon.xcframework/Info.plist",
		"ios/Addon.xcframework/ios-arm64/Addon",
		"ios/Addon.xcframework/ios-arm64/Headers/addon.h",
	}, archiveEntries(t, filepath.Join(outputDirectory, "addon-1.2.3-ios.arm64.zip")))
	require.Equal(t, []string{"plugin.gd"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.2.3-core.zip")))
}

func TestPackageProducesACoreOnlyIndexForAnAddonWithNoNativeCode(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "0.1.0"
`, map[string]string{
		"addons/addon/plugin.gd":  "extends Node\n",
		"addons/addon/plugin.cfg": "[plugin]\n",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, []string{"core"}, sliceIDs(index))
	require.Empty(t, index.Slices["core"].Libraries)
	require.Empty(t, index.Slices["core"].Dependencies)
	require.Equal(t, []string{"plugin.cfg", "plugin.gd"},
		archiveEntries(t, filepath.Join(filepath.Dir(result.Index), "addon-0.1.0-core.zip")))
}

func TestPackageVersionFlagOverridesTheConfiguredVersion(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "0.1.0"
`, map[string]string{"addons/addon/plugin.gd": "extends Node\n"})

	result, err := packager.Package(packager.Options{Directory: root, Version: "9.9.9"})
	require.NoError(t, err)
	require.Equal(t, "9.9.9", result.Version)
	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, "9.9.9", index.Version)
	require.Equal(t, "addon-9.9.9-core.zip", index.Slices["core"].File)
}

func TestPackageLeavesUnrelatedOutputFilesAloneAndOverwritesItsOwn(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "0.1.0"
`, map[string]string{"addons/addon/plugin.gd": "extends Node\n"})
	outputDirectory := t.TempDir()
	unrelated := filepath.Join(outputDirectory, "addon-0.0.9-core.zip")
	require.NoError(t, os.WriteFile(unrelated, []byte("older release"), 0o644))
	stale := filepath.Join(outputDirectory, "addon-0.1.0-core.zip")
	require.NoError(t, os.WriteFile(stale, []byte("stale"), 0o644))

	_, err := packager.Package(packager.Options{Directory: root, OutputDirectory: outputDirectory})
	require.NoError(t, err)

	kept, err := os.ReadFile(unrelated)
	require.NoError(t, err)
	require.Equal(t, "older release", string(kept))
	replaced, err := os.ReadFile(stale)
	require.NoError(t, err)
	require.NotEqual(t, "stale", string(replaced))
}

func TestPackagePartitionsEveryFileIntoExactlyOneSliceApartFromTheFanOut(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"

[package.slices]
"android.arm64" = ["android/addon.aar"]
`, map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/android/addon.aar":      "aar",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)

	carriers := map[string][]string{}
	for _, published := range result.Slices {
		for _, name := range archiveEntries(t, filepath.Join(filepath.Dir(result.Index), published.File)) {
			carriers[name] = append(carriers[name], published.ID)
		}
	}
	walked := map[string]bool{}
	addonRoot := filepath.Join(root, "addons", "addon")
	require.NoError(t, filepath.Walk(addonRoot, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(addonRoot, path)
		require.NoError(t, err)
		walked[filepath.ToSlash(relative)] = true
		return nil
	}))

	for path := range walked {
		require.Len(t, carriers[path], 1, "%s is carried by %v", path, carriers[path])
	}
	for path := range carriers {
		require.True(t, walked[path], "%s is archived but is not a file under addon_path", path)
	}
}

func TestPackageWritesTheIndexOnlyAfterEveryArchiveExists(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`, map[string]string{"addons/addon/plugin.gd": "extends Node\n"})
	outputDirectory := t.TempDir()
	// The directory exists, so it is not created, but no archive can be written
	// into it. The index must not appear.
	require.NoError(t, os.Chmod(outputDirectory, 0o555))
	t.Cleanup(func() { _ = os.Chmod(outputDirectory, 0o755) })

	_, err := packager.Package(packager.Options{Directory: root, OutputDirectory: outputDirectory})
	require.Error(t, err)
	require.Equal(t, output.ExitInstall, output.CodeFor(err))
	_, statErr := os.Stat(filepath.Join(outputDirectory, packager.IndexFileName))
	require.True(t, os.IsNotExist(statErr), "the index was written before its archives")
}

func TestPackageWritesNothingOutsideTheOutputDirectory(t *testing.T) {
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`, map[string]string{
		"addons/addon/addon.gdextension":      multiPlatformExtension,
		"addons/addon/plugin.gd":              "extends Node\n",
		"addons/addon/bin/addon_windows.dll":  "windows",
		"addons/addon/bin/addon_linux.so":     "linux",
		"addons/addon/bin/addon_ios.dylib":    "ios",
		"addons/addon/bin/libgodot-cpp_ios.a": "ios dependency",
		"addons/addon/bin/libextra_ios.a":     "ios extra dependency",
	})
	before := treeSnapshot(t, root)

	outputDirectory := t.TempDir()
	_, err := packager.Package(packager.Options{Directory: root, OutputDirectory: outputDirectory})
	require.NoError(t, err)

	require.Equal(t, before, treeSnapshot(t, root), "the addon repository was modified")
}

// treeSnapshot records every path under root with its content, so a test can
// assert the tree was not touched.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		relative, err := filepath.Rel(root, path)
		require.NoError(t, err)
		if info.IsDir() {
			snapshot[relative] = "<dir>"
			return nil
		}
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		snapshot[relative] = string(content)
		return nil
	}))
	return snapshot
}
