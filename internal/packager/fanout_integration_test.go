package packager_test

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/packager"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// godotJoltExtensionPath is the fan-out fixture's path inside its addon subtree,
// which is also its index section path key.
const godotJoltExtensionPath = "godot_jolt.gdextension"

// godotJoltAddonPath is where the fan-out fixture's addon subtree sits in the
// repository the tests build.
const godotJoltAddonPath = "addons/godot_jolt"

// godotJoltBinaries are the paths the fixture's fourteen [libraries] values name,
// relative to the addon root. The two macOS entries name directory bundles, which
// is what a real .framework is, so each is laid out as a directory with a file
// inside.
var godotJoltBinaries = []string{
	"bin/godot-jolt_windows_x64.dll",
	"bin/godot-jolt_linux_x64.so",
	"bin/godot-jolt_android_arm64.so",
	"bin/godot-jolt_ios_arm64.dylib",
	"bin/godot-jolt_macos.framework/Versions/A/godot-jolt",
	"bin/godot-jolt_macos_universal.framework/Versions/A/godot-jolt",
}

// readFixture reads a .gdextension from internal/packager/testdata.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(content)
}

// writeGodotJoltAddon lays out a packageable addon tree carrying the fan-out
// fixture: the real godot_jolt .gdextension plus every binary it names.
func writeGodotJoltAddon(t *testing.T) string {
	t.Helper()
	files := map[string]string{
		godotJoltAddonPath + "/" + godotJoltExtensionPath: readFixture(t, "godot_jolt.gdextension"),
		godotJoltAddonPath + "/plugin.gd":                 "extends Node\n",
	}
	for _, binary := range godotJoltBinaries {
		files[godotJoltAddonPath+"/"+binary] = binary
	}
	return writeAddon(t, `
[package]
name       = "godot_jolt"
addon_path = "addons/godot_jolt"
version    = "0.15.0"
`, files)
}

func TestPackagerFanOutPublishesTheArchitectureSliceAndSuppressesTheGenericOne(t *testing.T) {
	root := writeGodotJoltAddon(t)

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)

	require.Equal(t,
		[]string{"android.arm64", "core", "ios.arm64", "linux.x86_64", "macos.universal", "windows.x86_64"},
		sliceIDs(index),
	)
	require.NotContains(t, index.Slices, "macos")
	require.Equal(t,
		[]string{"macos.editor", "macos.template_debug", "macos.template_release", "macos.template_release.universal"},
		entryKeys(index.Slices["macos.universal"].Libraries[godotJoltExtensionPath]),
	)
}

func TestPackagerFanOutKeepsEveryPublishedSliceIndependentlyComplete(t *testing.T) {
	root := writeGodotJoltAddon(t)

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)

	core := archiveContent(t,
		filepath.Join(filepath.Dir(result.Index), index.Slices["core"].File), godotJoltExtensionPath)

	entries := map[slice.SliceID]slice.ExtensionEntries{}
	for _, key := range sliceIDs(index) {
		id, err := slice.ParseSliceID(key)
		require.NoError(t, err)
		if id.IsCore() {
			continue
		}
		entries[id] = slice.ExtensionEntries{
			Libraries:    index.Slices[key].Libraries[godotJoltExtensionPath],
			Dependencies: index.Slices[key].Dependencies[godotJoltExtensionPath],
		}
	}

	// A host selects one platform slice, so each one has to reassemble on its own
	// beside core into a .gdextension describing every binary that slice ships.
	for id, sliceEntries := range entries {
		reassembled, err := slice.ReassembleExtension(core, entries, []slice.SliceID{slice.CoreSliceID(), id})
		require.NoError(t, err, "slice %s", id)
		for key, value := range sliceEntries.Libraries {
			require.Contains(t, string(reassembled), key, "slice %s loses entry %s", id, key)
			require.Contains(t, string(reassembled), value, "slice %s loses value %s", id, value)
		}
	}

	// The entry today's gap drops: a darwin host resolving to macos.universal
	// must still receive the editor library.
	macosUniversal, err := slice.ReassembleExtension(core, entries,
		[]slice.SliceID{slice.CoreSliceID(), {Platform: "macos", Architecture: "universal"}})
	require.NoError(t, err)
	require.Contains(t, string(macosUniversal), "macos.editor")
	require.Contains(t, string(macosUniversal), "macos.template_release.universal")
}

func TestPackagerFanOutStoresAGenericEntrysFilesInOneSharedArtifact(t *testing.T) {
	extension := `[configuration]

entry_symbol = "addon_main"

[libraries]

macos.editor = "bin/addon_macos.dylib"
macos.template_release.universal = "bin/addon_macos_universal.dylib"
macos.template_debug.arm64 = "bin/addon_macos_arm64.dylib"
`
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.0.0"
`, map[string]string{
		"addons/addon/addon.gdextension":               extension,
		"addons/addon/plugin.gd":                       "extends Node\n",
		"addons/addon/bin/addon_macos.dylib":           "generic",
		"addons/addon/bin/addon_macos_universal.dylib": "universal",
		"addons/addon/bin/addon_macos_arm64.dylib":     "arm64",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, []string{"core", "macos.arm64", "macos.universal"}, sliceIDs(index))

	outputDirectory := filepath.Dir(result.Index)
	// The generic entry's binary is stored once in the shared artifact. Each
	// architecture archive carries only its own bytes and references the same
	// automatic dependency.
	require.Equal(t, []string{"bin/addon_macos_arm64.dylib"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.0.0-macos.arm64.zip")))
	require.Equal(t, []string{"bin/addon_macos_universal.dylib"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.0.0-macos.universal.zip")))
	require.Equal(t, []string{"bin/addon_macos.dylib"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.0.0-shared-macos.zip")))
	require.Equal(t, []string{"addon.gdextension", "plugin.gd"},
		archiveEntries(t, filepath.Join(outputDirectory, "addon-1.0.0-core.zip")))

	for _, key := range []string{"macos.arm64", "macos.universal"} {
		require.Contains(t, entryKeys(index.Slices[key].Libraries["addon.gdextension"]), "macos.editor")
		require.Equal(t, []string{"macos"}, index.Slices[key].Artifacts)
	}
}

func TestPackagerFanOutPublishesAGenericOnlyPlatformUnchanged(t *testing.T) {
	extension := `[configuration]

entry_symbol = "addon_main"

[libraries]

macos.editor = "bin/addon_macos.dylib"
macos.template_release = "bin/addon_macos.dylib"
`
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.0.0"
`, map[string]string{
		"addons/addon/addon.gdextension":     extension,
		"addons/addon/plugin.gd":             "extends Node\n",
		"addons/addon/bin/addon_macos.dylib": "generic",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, []string{"core", "macos"}, sliceIDs(index))
	require.Equal(t, []string{"macos.editor", "macos.template_release"},
		entryKeys(index.Slices["macos"].Libraries["addon.gdextension"]))
}

func TestPackagerFanOutPublishesOnlyTheArchitectureSlicesOfAnArchitectureOnlyPlatform(t *testing.T) {
	extension := `[configuration]

entry_symbol = "addon_main"

[libraries]

macos.editor.arm64 = "bin/addon_macos_arm64.dylib"
macos.template_release.universal = "bin/addon_macos_universal.dylib"
`
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.0.0"
`, map[string]string{
		"addons/addon/addon.gdextension":               extension,
		"addons/addon/plugin.gd":                       "extends Node\n",
		"addons/addon/bin/addon_macos_arm64.dylib":     "arm64",
		"addons/addon/bin/addon_macos_universal.dylib": "universal",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, []string{"core", "macos.arm64", "macos.universal"}, sliceIDs(index))
	require.NotContains(t, index.Slices, "macos")
}

func TestPackagerFanOutIsDeterministic(t *testing.T) {
	root := writeGodotJoltAddon(t)

	first := t.TempDir()
	_, err := packager.Package(packager.Options{Directory: root, OutputDirectory: first})
	require.NoError(t, err)
	second := t.TempDir()
	_, err = packager.Package(packager.Options{Directory: root, OutputDirectory: second})
	require.NoError(t, err)

	entries, err := os.ReadDir(first)
	require.NoError(t, err)
	require.Len(t, entries, 7)
	for _, entry := range entries {
		firstBytes, err := os.ReadFile(filepath.Join(first, entry.Name()))
		require.NoError(t, err)
		secondBytes, err := os.ReadFile(filepath.Join(second, entry.Name()))
		require.NoError(t, err)
		require.Equal(t, firstBytes, secondBytes, "%s is not reproducible", entry.Name())
	}
}

func TestPackagerFanOutCarriesGenericExtrasIntoTheArchitectureSlicesTheyName(t *testing.T) {
	// [package.slices] names the platform generically, which is the only
	// spelling available for a payload that is one file for every architecture.
	// The platform fans out, so the files land in each architecture slice and no
	// generic slice is published.
	files := map[string]string{
		godotJoltAddonPath + "/" + godotJoltExtensionPath: readFixture(t, "godot_jolt.gdextension"),
		godotJoltAddonPath + "/macos/extra.dylib":         "extra",
	}
	for _, binary := range godotJoltBinaries {
		files[godotJoltAddonPath+"/"+binary] = binary
	}
	root := writeAddon(t, `
[package]
name       = "godot_jolt"
addon_path = "addons/godot_jolt"
version    = "0.15.0"

[package.slices]
"macos" = ["macos/extra.dylib"]
`, files)

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.NotContains(t, index.Slices, "macos")
	require.Contains(t,
		archiveEntries(t, filepath.Join(filepath.Dir(result.Index), "godot_jolt-0.15.0-macos.universal.zip")),
		"macos/extra.dylib")
}

// TestPackagerFixtureMatchesTheSliceFixture keeps the packager's fan-out fixture
// and internal/slice's own godot_jolt fixture from silently diverging: both are
// partitioned with PartitionExtension and must agree on the slice set and on
// each slice's entry keys, so the shape the fan-out is tested against stays the
// shape the real addon ships.
func TestPackagerFixtureMatchesTheSliceFixture(t *testing.T) {
	addonRoot := "res://" + godotJoltAddonPath
	packagerContent, err := os.ReadFile(filepath.Join("testdata", "godot_jolt.gdextension"))
	require.NoError(t, err)
	sliceContent, err := os.ReadFile(filepath.Join("..", "slice", "testdata", "godot_jolt.gdextension"))
	require.NoError(t, err)

	_, packagerEntries, err := slice.PartitionExtension(packagerContent, addonRoot, godotJoltExtensionPath)
	require.NoError(t, err)
	_, sliceEntries, err := slice.PartitionExtension(sliceContent, addonRoot, godotJoltExtensionPath)
	require.NoError(t, err)

	require.Equal(t, describePartition(sliceEntries), describePartition(packagerEntries))
	// The mixed granularity the fan-out exists for: one platform appearing as
	// both a generic and an architecture-specific slice.
	require.Contains(t, describePartition(packagerEntries),
		"macos: macos.editor,macos.template_debug,macos.template_release")
	require.Contains(t, describePartition(packagerEntries),
		"macos.universal: macos.template_release.universal")
}

// describePartition renders a partition's slice set and per-slice entry keys in
// a deterministic, comparable form.
func describePartition(partition map[slice.SliceID]slice.ExtensionEntries) []string {
	described := make([]string, 0, len(partition))
	for id, entries := range partition {
		keys := append(entryKeys(entries.Libraries), entryKeys(entries.Dependencies)...)
		sort.Strings(keys)
		described = append(described, id.String()+": "+joinKeys(keys))
	}
	sort.Strings(described)
	return described
}

func joinKeys(keys []string) string {
	joined := ""
	for index, key := range keys {
		if index > 0 {
			joined += ","
		}
		joined += key
	}
	return joined
}

func TestPackagerFanOutReachesAnExtrasDeclaredArchitectureOfAFannedOutPlatform(t *testing.T) {
	// The .gdextension fans macos out, and [package.slices] ships an additional
	// macOS architecture the .gdextension never names. A darwin/arm64 host
	// resolves macos.arm64 before macos.universal, so that slice has to carry the
	// platform's generic entries too or the host installs an addon with no macOS
	// library at all.
	extension := `[configuration]

entry_symbol = "addon_main"

[libraries]

macos.editor = "bin/addon_macos.dylib"
macos.template_release = "bin/addon_macos.dylib"
macos.template_release.universal = "bin/addon_macos_universal.dylib"
`
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.0.0"

[package.slices]
"macos.arm64" = ["macos/arm64/extra.dylib"]
`, map[string]string{
		"addons/addon/addon.gdextension":               extension,
		"addons/addon/plugin.gd":                       "extends Node\n",
		"addons/addon/bin/addon_macos.dylib":           "generic",
		"addons/addon/bin/addon_macos_universal.dylib": "universal",
		"addons/addon/macos/arm64/extra.dylib":         "extra",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, []string{"core", "macos.arm64", "macos.universal"}, sliceIDs(index))

	for _, key := range []string{"macos.arm64", "macos.universal"} {
		keys := entryKeys(index.Slices[key].Libraries["addon.gdextension"])
		require.Contains(t, keys, "macos.editor", "slice %s loses the platform's generic entries", key)
		require.Contains(t, keys, "macos.template_release", "slice %s loses the platform's generic entries", key)
	}
	require.Equal(t, []string{"macos/arm64/extra.dylib"},
		archiveEntries(t, filepath.Join(filepath.Dir(result.Index), "addon-1.0.0-macos.arm64.zip")))
	require.Equal(t, []string{"bin/addon_macos.dylib"},
		archiveEntries(t, filepath.Join(filepath.Dir(result.Index), "addon-1.0.0-shared-macos.zip")))
}

func TestPackagerStillRefusesAGenericPlatformBesideAnExtrasOnlyArchitecture(t *testing.T) {
	// Here the .gdextension names no macOS architecture at all, so nothing says
	// which architectures the generic binaries are for. Fanning out would publish
	// macos.arm64 alone and silently drop every other macOS host, so the config
	// is reported instead.
	extension := `[configuration]

entry_symbol = "addon_main"

[libraries]

macos.editor = "bin/addon_macos.dylib"
`
	root := writeAddon(t, `
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.0.0"

[package.slices]
"macos.arm64" = ["macos/arm64/extra.dylib"]
`, map[string]string{
		"addons/addon/addon.gdextension":       extension,
		"addons/addon/plugin.gd":               "extends Node\n",
		"addons/addon/bin/addon_macos.dylib":   "generic",
		"addons/addon/macos/arm64/extra.dylib": "extra",
	})

	_, err := packager.Package(packager.Options{Directory: root})
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.ErrorAs(t, err, &manifestError)
	require.Contains(t, err.Error(), `"macos.arm64"`)
}

// sentryExtension is the shape of an addon whose Android support is published
// per ABI: every [libraries] key carries an architecture, so the platform has no
// generic slice of its own to put a plugin archive in.
const sentryExtension = `[configuration]

entry_symbol = "sentry_main"

[libraries]

android.debug.arm64 = "bin/android/libsentry.android.debug.arm64.so"
android.release.arm64 = "bin/android/libsentry.android.release.arm64.so"
android.debug.arm32 = "bin/android/libsentry.android.debug.arm32.so"
android.release.arm32 = "bin/android/libsentry.android.release.arm32.so"
`

// writeSentryAddon lays out that addon plus the two .aar files no entry key
// references and which Godot needs at Android export time.
func writeSentryAddon(t *testing.T, slices string) string {
	t.Helper()
	return writeAddon(t, `
[package]
name       = "sentry"
addon_path = "addons/sentry"
version    = "2.3.0"
`+slices, map[string]string{
		"addons/sentry/sentry.gdextension":                             sentryExtension,
		"addons/sentry/plugin.gd":                                      "extends Node\n",
		"addons/sentry/bin/android/libsentry.android.debug.arm64.so":   "debug arm64",
		"addons/sentry/bin/android/libsentry.android.release.arm64.so": "release arm64",
		"addons/sentry/bin/android/libsentry.android.debug.arm32.so":   "debug arm32",
		"addons/sentry/bin/android/libsentry.android.release.arm32.so": "release arm32",
		"addons/sentry/bin/android/sentry_godot_plugin.debug.aar":      "debug aar",
		"addons/sentry/bin/android/sentry_godot_plugin.release.aar":    "release aar",
	})
}

func TestPackagerStoresAGenericExtrasEntryOnceForEveryArchitectureSliceOfItsPlatform(t *testing.T) {
	root := writeSentryAddon(t, `
[package.slices]
"android" = ["bin/android/*.aar"]
`)

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)

	// No generic android slice is published: its payload is a non-selectable
	// shared artifact that both ABI slices depend on automatically.
	require.Equal(t, []string{"android.arm32", "android.arm64", "core"}, sliceIDs(index))
	require.Equal(t, 2, index.Format)
	require.Equal(t, []string{"android"}, index.Slices["android.arm32"].Artifacts)
	require.Equal(t, []string{"android"}, index.Slices["android.arm64"].Artifacts)
	require.Contains(t, index.Artifacts, "android")

	outputDirectory := filepath.Dir(result.Index)
	for _, architecture := range []string{"arm32", "arm64"} {
		require.Equal(t,
			[]string{
				"bin/android/libsentry.android.debug." + architecture + ".so",
				"bin/android/libsentry.android.release." + architecture + ".so",
			},
			archiveEntries(t, filepath.Join(outputDirectory, "sentry-2.3.0-android."+architecture+".zip")),
			"architecture %s does not carry the platform's generic extras", architecture,
		)
	}
	require.Equal(t,
		[]string{"bin/android/sentry_godot_plugin.debug.aar", "bin/android/sentry_godot_plugin.release.aar"},
		archiveEntries(t, filepath.Join(outputDirectory, index.Artifacts["android"].File)),
	)
	// The .aar files are the whole point of the extras entry, so they must not
	// also fall into core, which every consumer downloads on every platform.
	require.Equal(t, []string{"plugin.gd", "sentry.gdextension"},
		archiveEntries(t, filepath.Join(outputDirectory, "sentry-2.3.0-core.zip")))
}

func TestPackagerAcceptsAndroidLibrariesProvidedByAnAARPlugin(t *testing.T) {
	extension := `[configuration]

entry_symbol = "aarkit_main"
android_aar_plugin = true

[libraries]

android.debug.arm64 = "res://addons/aarkit/bin/android/arm64-v8a/libaarkit.so"
android.release.arm64 = "res://addons/aarkit/bin/android/arm64-v8a/libaarkit.so"
android.debug.x86_64 = "res://addons/aarkit/bin/android/x86_64/libaarkit.so"
android.release.x86_64 = "res://addons/aarkit/bin/android/x86_64/libaarkit.so"
linux.x86_64 = "res://addons/aarkit/bin/linux_x86_64/libaarkit.so"
`
	root := writeAddon(t, `
[package]
name       = "aarkit"
addon_path = "addons/aarkit"
version    = "1.0.0"

[package.slices]
android = ["bin/android/aarkit.aar"]
`, map[string]string{
		"addons/aarkit/aarkit.gdextension":            extension,
		"addons/aarkit/plugin.gd":                     "extends Node\n",
		"addons/aarkit/bin/android/aarkit.aar":        "Android libraries live at jni/<abi>/ inside this archive",
		"addons/aarkit/bin/linux_x86_64/libaarkit.so": "linux",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)

	require.Equal(t, []string{"android.arm64", "android.x86_64", "core", "linux.x86_64"}, sliceIDs(index))
	require.Equal(t, 2, index.Format)
	require.Equal(t, []string{"android"}, index.Slices["android.arm64"].Artifacts)
	require.Equal(t, []string{"android"}, index.Slices["android.x86_64"].Artifacts)
	require.Equal(t,
		slice.ExtensionEntryTable[string]{
			"android.debug.arm64":   "res://addons/aarkit/bin/android/arm64-v8a/libaarkit.so",
			"android.release.arm64": "res://addons/aarkit/bin/android/arm64-v8a/libaarkit.so",
		},
		index.Slices["android.arm64"].Libraries["aarkit.gdextension"],
	)
	require.Equal(t,
		slice.ExtensionEntryTable[string]{
			"android.debug.x86_64":   "res://addons/aarkit/bin/android/x86_64/libaarkit.so",
			"android.release.x86_64": "res://addons/aarkit/bin/android/x86_64/libaarkit.so",
		},
		index.Slices["android.x86_64"].Libraries["aarkit.gdextension"],
	)
	require.Equal(t,
		[]string{"bin/android/aarkit.aar"},
		archiveEntries(t, filepath.Join(filepath.Dir(result.Index), index.Artifacts["android"].File)),
	)
	core := string(archiveContent(t,
		filepath.Join(filepath.Dir(result.Index), index.Slices["core"].File), "aarkit.gdextension"))
	require.Contains(t, core, "android_aar_plugin=true")
}

func TestPackagerKeepsThreadedAndNonThreadedWebLibrariesInOneSlice(t *testing.T) {
	extension := `[configuration]

entry_symbol = "sentry_main"

[libraries]

web.debug.wasm32 = "bin/web/libsentry.web.debug.wasm32.nothreads.wasm"
web.release.wasm32 = "bin/web/libsentry.web.release.wasm32.nothreads.wasm"
web.debug.threads.wasm32 = "bin/web/libsentry.web.debug.wasm32.wasm"
web.release.threads.wasm32 = "bin/web/libsentry.web.release.wasm32.wasm"
`
	root := writeAddon(t, `
[package]
name       = "sentry"
addon_path = "addons/sentry"
version    = "2.3.0"
`, map[string]string{
		"addons/sentry/sentry.gdextension":                                  extension,
		"addons/sentry/plugin.gd":                                           "extends Node\n",
		"addons/sentry/bin/web/libsentry.web.debug.wasm32.nothreads.wasm":   "debug no threads",
		"addons/sentry/bin/web/libsentry.web.release.wasm32.nothreads.wasm": "release no threads",
		"addons/sentry/bin/web/libsentry.web.debug.wasm32.wasm":             "debug threads",
		"addons/sentry/bin/web/libsentry.web.release.wasm32.wasm":           "release threads",
	})

	result, err := packager.Package(packager.Options{Directory: root})
	require.NoError(t, err)
	index := loadEmittedIndex(t, result.Index)
	require.Equal(t, []string{"core", "web.wasm32"}, sliceIDs(index))
	require.Equal(t,
		[]string{
			"web.debug.threads.wasm32",
			"web.debug.wasm32",
			"web.release.threads.wasm32",
			"web.release.wasm32",
		},
		entryKeys(index.Slices["web.wasm32"].Libraries["sentry.gdextension"]),
	)
	require.Equal(t,
		[]string{
			"bin/web/libsentry.web.debug.wasm32.nothreads.wasm",
			"bin/web/libsentry.web.debug.wasm32.wasm",
			"bin/web/libsentry.web.release.wasm32.nothreads.wasm",
			"bin/web/libsentry.web.release.wasm32.wasm",
		},
		archiveEntries(t, filepath.Join(filepath.Dir(result.Index), "sentry-2.3.0-web.wasm32.zip")),
	)
}
