package slice

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

const coreSliceDigest = "2bbed7f0a0218278217edd81a4a4d10f91362aa0c46f80f5931423947004e853"
const platformSliceDigest = "fe7ee35a4e429b6f0e0e07931a046f52b91e1530689ed6ad46fa83239d166711"
const sharedArtifactDigest = "a17ee35a4e429b6f0e0e07931a046f52b91e1530689ed6ad46fa83239d166712"

// validIndexTOML is the minimal well-formed index the rejection table mutates.
const validIndexTOML = `format = 1
name = "limboai"
version = "1.4.0"

[slices.core]
file = "limboai-1.4.0-core.zip"
sha256 = "` + coreSliceDigest + `"
size = 182344

[slices."ios.arm64"]
file = "limboai-1.4.0-ios.arm64.zip"
sha256 = "` + platformSliceDigest + `"
size = 4821001
`

const validFormat2IndexTOML = `format = 2
name = "limboai"
version = "1.4.0"

[artifacts.android]
file = "limboai-1.4.0-shared-android.zip"
sha256 = "` + sharedArtifactDigest + `"
size = 90210

[slices.core]
file = "limboai-1.4.0-core.zip"
sha256 = "` + coreSliceDigest + `"
size = 182344

[slices."android.arm64"]
file = "limboai-1.4.0-android.arm64.zip"
sha256 = "` + platformSliceDigest + `"
size = 4821001
artifacts = ["android"]

[slices."android.arm32"]
file = "limboai-1.4.0-android.arm32.zip"
sha256 = "` + coreSliceDigest + `"
size = 3821001
artifacts = ["android"]
`

// renderSectionEntry renders one partitioned entry in the section's own schema:
// a [libraries] entry is a res:// string, while a [dependencies] entry is a
// table of dependency paths to export destinations, so a contract row that
// applies to every section has to be spelled per section rather than once.
func renderSectionEntry(sliceKey string, section ExtensionSection, pathKey, entryKey, resourcePath string) string {
	header := "\n[slices.\"" + sliceKey + "\"." + string(section) + ".\"" + pathKey + "\"]\n"
	if section == SectionDependencies {
		return header + "\"" + entryKey + "\" = { \"" + resourcePath + "\" = \"\" }\n"
	}
	return header + "\"" + entryKey + "\" = \"" + resourcePath + "\"\n"
}

// platformSliceWithSection renders the ios.arm64 slice carrying one entry in the
// named section.
func platformSliceWithSection(section ExtensionSection, pathKey, entryKey, entryValue string) string {
	return validIndexTOML + renderSectionEntry("ios.arm64", section, pathKey, entryKey, entryValue)
}

func coreSliceWithSection(section ExtensionSection) string {
	return validIndexTOML + renderSectionEntry(
		CorePlatform, section, "limboai.gdextension", "ios.template_release", "res://addons/limboai/bin/libai.a",
	)
}

func requireFetchError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var fetchError *output.FetchError
	require.ErrorAs(t, err, &fetchError, "want an *output.FetchError, got %v", err)
	require.Equal(t, output.ExitFetch, output.CodeFor(err))
}

func TestIndexLoadAcceptsCheckedInFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "gpm-index.toml"))
	require.NoError(t, err)

	index, err := LoadIndex(data)
	require.NoError(t, err)
	require.Equal(t, 1, index.Format)
	require.Equal(t, "limboai", index.Name)
	require.Equal(t, "1.4.0", index.Version)
	require.Len(t, index.Slices, 2)

	platformSlice := index.Slices["ios.arm64"]
	require.NotNil(t, platformSlice)
	require.Equal(t, "limboai-1.4.0-ios.arm64.zip", platformSlice.File)
	require.Equal(t, int64(4821001), platformSlice.Size)
	require.Len(t, platformSlice.Libraries["limboai.gdextension"], 2)
	require.Equal(t,
		"res://addons/limboai/bin/liblimboai.ios.template_debug.xcframework",
		platformSlice.Libraries["limboai.gdextension"]["ios.template_debug"],
	)
	require.Len(t, platformSlice.Dependencies["limboai.gdextension"], 2)
	// The destination the nested shape exists to carry: limboai copies its
	// release dependency into the export's Frameworks subdirectory and its debug
	// dependency beside the exported binary.
	require.Equal(t,
		ExtensionDependencyTargets{"res://addons/limboai/bin/libgodot-cpp.ios.template_debug.a": ""},
		platformSlice.Dependencies["limboai.gdextension"]["ios.template_debug"],
	)
	require.Equal(t,
		ExtensionDependencyTargets{"res://addons/limboai/bin/libgodot-cpp.ios.template_release.a": "Frameworks"},
		platformSlice.Dependencies["limboai.gdextension"]["ios.template_release"],
	)
}

func TestIndexLoadAcceptsFormat2SharedArtifactDependencies(t *testing.T) {
	index, err := LoadIndex([]byte(validFormat2IndexTOML))
	require.NoError(t, err)
	require.Equal(t, 2, index.Format)
	require.Equal(t, &IndexArtifact{
		File: "limboai-1.4.0-shared-android.zip", SHA256: sharedArtifactDigest, Size: 90210,
	}, index.Artifacts["android"])
	require.Equal(t, []string{"android"}, index.Slices["android.arm64"].Artifacts)
	require.Equal(t, []string{"android"}, index.RequiredArtifacts([]SliceID{{Platform: "android", Architecture: "arm64"}}))
}

func TestIndexFormat1RejectsFormat2ArtifactKeys(t *testing.T) {
	document := strings.Replace(validFormat2IndexTOML, "format = 2", "format = 1", 1)
	index, err := LoadIndex([]byte(document))
	require.Nil(t, index)
	requireFetchError(t, err)
	require.Contains(t, err.Error(), "artifacts")
	require.Contains(t, err.Error(), "format 1")
}

func TestIndexRejectsInvalidFormat2ArtifactGraphs(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		document string
		contains string
	}{
		{
			name:     "format 2 without artifacts",
			document: strings.Replace(validIndexTOML, "format = 1", "format = 2", 1),
			contains: "declares no [artifacts]",
		},
		{
			name:     "format 1 slice dependency",
			document: strings.Replace(validIndexTOML, "size = 4821001", "size = 4821001\nartifacts = [\"android\"]", 1),
			contains: "format 1",
		},
		{
			name:     "missing artifact",
			document: strings.ReplaceAll(validFormat2IndexTOML, `artifacts = ["android"]`, `artifacts = ["ios"]`),
			contains: "does not publish",
		},
		{
			name:     "generic slice dependency",
			document: strings.Replace(validFormat2IndexTOML, `[slices."android.arm64"]`, `[slices.android]`, 1),
			contains: "only architecture slices",
		},
		{
			name:     "artifact referenced once",
			document: strings.Replace(validFormat2IndexTOML, "size = 3821001\nartifacts = [\"android\"]", "size = 3821001", 1),
			contains: "referenced by 1 slices",
		},
		{
			name: "one architecture omits its platform artifact",
			document: validFormat2IndexTOML + `

[slices."android.x86_64"]
file = "limboai-1.4.0-android.x86_64.zip"
sha256 = "` + coreSliceDigest + `"
size = 2821001
`,
			contains: "does not reference",
		},
		{
			name:     "artifact and slice reuse archive name",
			document: strings.Replace(validFormat2IndexTOML, "limboai-1.4.0-shared-android.zip", "limboai-1.4.0-core.zip", 1),
			contains: "both declare file",
		},
		{
			name:     "unknown artifact field",
			document: strings.Replace(validFormat2IndexTOML, "size = 90210", "size = 90210\nmystery = true", 1),
			contains: "mystery",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			index, err := LoadIndex([]byte(testCase.document))
			require.Nil(t, index)
			requireFetchError(t, err)
			require.Contains(t, err.Error(), testCase.contains)
		})
	}
}

func TestIndexLoadKeepsBothSectionsDistinctForOneSharedKey(t *testing.T) {
	document := validIndexTOML + `
[slices."ios.arm64".libraries."limboai.gdextension"]
"ios.template_release.arm64" = "res://addons/limboai/bin/libai.ios.arm64.a"

[slices."ios.arm64".dependencies."limboai.gdextension"]
"ios.template_release.arm64" = { "res://addons/limboai/bin/dep.framework" = "Frameworks" }
`
	index, err := LoadIndex([]byte(document))
	require.NoError(t, err)

	platformSlice := index.Slices["ios.arm64"]
	require.Equal(t,
		"res://addons/limboai/bin/libai.ios.arm64.a",
		platformSlice.Libraries["limboai.gdextension"]["ios.template_release.arm64"],
	)
	require.Equal(t,
		ExtensionDependencyTargets{"res://addons/limboai/bin/dep.framework": "Frameworks"},
		platformSlice.Dependencies["limboai.gdextension"]["ios.template_release.arm64"],
	)
}

func TestIndexRejectsMalformedInput(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		document string
		contains []string
	}{
		{
			name: "format absent",
			document: `name = "limboai"
version = "1.4.0"

[slices.core]
file = "core.zip"
sha256 = "` + coreSliceDigest + `"
size = 1
`,
			contains: []string{"format"},
		},
		{
			name:     "format above supported maximum",
			document: strings.Replace(validIndexTOML, "format = 1", "format = 3", 1),
			contains: []string{"3", "2"},
		},
		{
			name:     "format above supported maximum is reported before any other problem",
			document: strings.Replace(validIndexTOML, "format = 1", "format = 3\nmystery = true", 1),
			contains: []string{"format", "3", "2"},
		},
		{
			name:     "format zero",
			document: strings.Replace(validIndexTOML, "format = 1", "format = 0", 1),
			contains: []string{"positive"},
		},
		{
			name:     "format negative",
			document: strings.Replace(validIndexTOML, "format = 1", "format = -1", 1),
			contains: []string{"positive"},
		},
		{
			name:     "format not an integer",
			document: strings.Replace(validIndexTOML, "format = 1", `format = "one"`, 1),
			contains: []string{"format"},
		},
		{
			name:     "unparseable TOML",
			document: "format = 1\nname = \n",
			contains: []string{"index"},
		},
		{
			name: "unknown top-level key",
			document: `format = 1
name = "limboai"
version = "1.4.0"
mystery = "value"

[slices.core]
file = "limboai-1.4.0-core.zip"
sha256 = "` + coreSliceDigest + `"
size = 182344
`,
			contains: []string{"mystery", "format, name, version"},
		},
		{
			name: "unknown per-slice key",
			document: `format = 1
name = "limboai"
version = "1.4.0"

[slices.core]
file = "limboai-1.4.0-core.zip"
sha256 = "` + coreSliceDigest + `"
size = 182344
mystery = "value"
`,
			contains: []string{"mystery"},
		},
		{
			name:     "unknown per-slice table",
			document: validIndexTOML + "\n[slices.\"ios.arm64\".frameworks.\"limboai.gdextension\"]\n\"ios\" = \"res://a\"\n",
			contains: []string{"frameworks", "libraries", "dependencies"},
		},
		{
			name:     "case-variant top-level key",
			document: strings.Replace(validIndexTOML, `name = "limboai"`, `Name = "limboai"`, 1),
			contains: []string{"Name"},
		},
		{
			name:     "case-variant per-slice key",
			document: strings.Replace(validIndexTOML, `file = "limboai-1.4.0-ios.arm64.zip"`, `File = "limboai-1.4.0-ios.arm64.zip"`, 1),
			contains: []string{"File"},
		},
		{
			name:     "case-variant partitioned section",
			document: validIndexTOML + "\n[slices.\"ios.arm64\".Libraries.\"limboai.gdextension\"]\n\"ios\" = \"res://addons/limboai/bin/libai.a\"\n",
			contains: []string{"Libraries"},
		},
		{
			name: "format above the supported maximum with an incompatible field type",
			document: `format = 3
name = "limboai"
version = "1.4.0"

[slices.core]
file = "limboai-1.4.0-core.zip"
sha256 = "` + coreSliceDigest + `"
size = "182344"
`,
			contains: []string{"format", "3", "2"},
		},
		{
			name: "slices absent",
			document: `format = 1
name = "limboai"
version = "1.4.0"
`,
			contains: []string{"slices"},
		},
		{
			name: "slices empty",
			document: `format = 1
name = "limboai"
version = "1.4.0"

[slices]
`,
			contains: []string{"slices"},
		},
		{
			name: "core slice absent",
			document: `format = 1
name = "limboai"
version = "1.4.0"

[slices."ios.arm64"]
file = "ios.zip"
sha256 = "` + platformSliceDigest + `"
size = 4
`,
			contains: []string{"core"},
		},
		{
			name:     "slice key is not a valid slice ID",
			document: strings.Replace(validIndexTOML, `[slices."ios.arm64"]`, `[slices."nintendo.arm64"]`, 1),
			contains: []string{"nintendo"},
		},
		{
			name:     "file empty",
			document: strings.Replace(validIndexTOML, `file = "limboai-1.4.0-ios.arm64.zip"`, `file = ""`, 1),
			contains: []string{"file"},
		},
		{
			name:     "file absolute",
			document: strings.Replace(validIndexTOML, `file = "limboai-1.4.0-ios.arm64.zip"`, `file = "/etc/passwd"`, 1),
			contains: []string{"file"},
		},
		{
			name:     "file containing a parent traversal",
			document: strings.Replace(validIndexTOML, `file = "limboai-1.4.0-ios.arm64.zip"`, `file = ".."`, 1),
			contains: []string{"file"},
		},
		{
			name:     "file containing a path separator",
			document: strings.Replace(validIndexTOML, `file = "limboai-1.4.0-ios.arm64.zip"`, `file = "sub/ios.zip"`, 1),
			contains: []string{"file"},
		},
		{
			name:     "file containing a windows path separator",
			document: strings.Replace(validIndexTOML, `file = "limboai-1.4.0-ios.arm64.zip"`, `file = 'sub\\ios.zip'`, 1),
			contains: []string{"file"},
		},
		{
			name:     "sha256 absent",
			document: strings.Replace(validIndexTOML, "sha256 = \""+platformSliceDigest+"\"\n", "", 1),
			contains: []string{"sha256"},
		},
		{
			name:     "sha256 not 64 hex digits",
			document: strings.Replace(validIndexTOML, platformSliceDigest, "deadbeef", 1),
			contains: []string{"sha256"},
		},
		{
			name:     "sha256 uppercase",
			document: strings.Replace(validIndexTOML, platformSliceDigest, strings.ToUpper(platformSliceDigest), 1),
			contains: []string{"sha256"},
		},
		{
			name:     "size absent",
			document: strings.Replace(validIndexTOML, "size = 4821001\n", "", 1),
			contains: []string{"size"},
		},
		{
			name:     "size zero",
			document: strings.Replace(validIndexTOML, "size = 4821001", "size = 0", 1),
			contains: []string{"size"},
		},
		{
			name:     "size negative",
			document: strings.Replace(validIndexTOML, "size = 4821001", "size = -1", 1),
			contains: []string{"size"},
		},
		{
			name:     "two slices declaring the same file",
			document: strings.Replace(validIndexTOML, `file = "limboai-1.4.0-ios.arm64.zip"`, `file = "limboai-1.4.0-core.zip"`, 1),
			contains: []string{"limboai-1.4.0-core.zip"},
		},
		{
			name:     "name empty",
			document: strings.Replace(validIndexTOML, `name = "limboai"`, `name = ""`, 1),
			contains: []string{"name"},
		},
		{
			name:     "version empty",
			document: strings.Replace(validIndexTOML, `version = "1.4.0"`, `version = ""`, 1),
			contains: []string{"version"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			index, err := LoadIndex([]byte(testCase.document))
			require.Nil(t, index, "a rejected index must not be reachable")
			requireFetchError(t, err)
			for _, fragment := range testCase.contains {
				require.Contains(t, err.Error(), fragment)
			}
		})
	}
}

// TestIndexRejectsMalformedPartitionedSections asserts every row of the
// fail-closed contract that applies identically to each partitioned section, so
// a section added to PartitionedSections without validation fails here.
func TestIndexRejectsMalformedPartitionedSections(t *testing.T) {
	for _, section := range PartitionedSections() {
		t.Run(string(section), func(t *testing.T) {
			for _, testCase := range []struct {
				name     string
				document string
				contains []string
			}{
				{
					name:     "path key is absolute",
					document: platformSliceWithSection(section, "/limboai.gdextension", "ios.template_release", "res://addons/limboai/bin/libai.a"),
					contains: []string{"/limboai.gdextension"},
				},
				{
					name:     "path key contains a parent traversal",
					document: platformSliceWithSection(section, "../limboai.gdextension", "ios.template_release", "res://addons/limboai/bin/libai.a"),
					contains: []string{"limboai.gdextension"},
				},
				{
					name:     "path key does not end in .gdextension",
					document: platformSliceWithSection(section, "limboai.cfg", "ios.template_release", "res://addons/limboai/bin/libai.a"),
					contains: []string{".gdextension"},
				},
				{
					name:     "entry value is not a res:// path",
					document: platformSliceWithSection(section, "limboai.gdextension", "ios.template_release", "/usr/lib/libai.a"),
					contains: []string{"res://"},
				},
				{
					name:     "entry value escapes the res:// root",
					document: platformSliceWithSection(section, "limboai.gdextension", "ios.template_release", "res://../../etc/passwd"),
					contains: []string{"res://"},
				},
				{
					name:     "entry key platform disagrees with the slice ID",
					document: platformSliceWithSection(section, "limboai.gdextension", "windows.template_release", "res://addons/limboai/bin/libai.dll"),
					contains: []string{"windows.template_release", "ios.arm64"},
				},
				{
					name:     "entry key architecture disagrees with the slice ID",
					document: platformSliceWithSection(section, "limboai.gdextension", "ios.template_release.x86_64", "res://addons/limboai/bin/libai.a"),
					contains: []string{"ios.arm64"},
				},
				{
					name:     "entry key is not a valid .gdextension key",
					document: platformSliceWithSection(section, "limboai.gdextension", "ios.mystery", "res://addons/limboai/bin/libai.a"),
					contains: []string{"mystery"},
				},
				{
					name:     "core slice carries entries",
					document: coreSliceWithSection(section),
					contains: []string{"core", string(section)},
				},
				{
					name:     "section table is present but empty",
					document: validIndexTOML + "\n[slices.\"ios.arm64\"." + string(section) + "]\n",
					contains: []string{string(section)},
				},
				{
					name:     "inner table is empty",
					document: validIndexTOML + "\n[slices.\"ios.arm64\"." + string(section) + ".\"limboai.gdextension\"]\n",
					contains: []string{"limboai.gdextension"},
				},
				{
					// One level past the section's own maximum depth: a
					// [libraries] entry bottoms out at its res:// value, while a
					// [dependencies] entry holds one more table of dependency
					// paths. Either way the key is rejected and the message names
					// the offending key path. Strict decoding reports it first,
					// as a leaf that is a table where the schema declares a
					// string; indexKeyDepths is the backstop that keeps an
					// over-deep key from being read as a known one, and is the
					// single declaration of both maxima.
					name: "a key one level deeper than the section allows",
					document: platformSliceWithSection(
						section, "limboai.gdextension", "ios.template_release", "res://addons/limboai/bin/libai.a",
					) + "\n[slices.\"ios.arm64\"." + string(section) +
						".\"limboai.gdextension\".\"ios.template_debug\"." + deeperKeyPath(section) + "]\n" +
						"\"extra\" = \"res://addons/limboai/bin/libai.a\"\n",
					contains: []string{string(section), "limboai.gdextension", "ios.template_debug"},
				},
			} {
				t.Run(testCase.name, func(t *testing.T) {
					index, err := LoadIndex([]byte(testCase.document))
					require.Nil(t, index, "a rejected index must not be reachable")
					requireFetchError(t, err)
					for _, fragment := range testCase.contains {
						require.Contains(t, err.Error(), fragment)
					}
				})
			}
		})
	}
}

func TestIndexFormatMismatchMessageNamesBothVersions(t *testing.T) {
	_, err := LoadIndex([]byte(strings.Replace(validIndexTOML, "format = 1", "format = 3", 1)))
	requireFetchError(t, err)
	require.Contains(t, err.Error(), "3")
	require.Contains(t, err.Error(), "2")
	require.Contains(t, err.Error(), "format")
}

func TestPartitionedSectionsIsClosed(t *testing.T) {
	require.Equal(t, []ExtensionSection{SectionLibraries, SectionDependencies}, PartitionedSections())
	require.Len(t, PartitionedSections(), 2)
}

// TestIndexSliceExtensionPathsResolvesEverySection replaces the test that
// covered (*IndexSlice).Section. That accessor is gone: a Go method cannot be
// generic, so a single accessor would have to widen one section's value back to
// the lossy leaf type this schema exists to remove. Its structural job — which
// .gdextension files does this slice ship, for a caller walking
// PartitionedSections — is what ExtensionPaths serves, and its leaf job is
// served by the two statically typed fields.
func TestIndexSliceExtensionPathsResolvesEverySection(t *testing.T) {
	indexSlice := &IndexSlice{
		Libraries: ExtensionSectionTable[string]{
			"second.gdextension": {"ios": "res://second"},
			"a.gdextension":      {"ios": "res://a"},
		},
		Dependencies: ExtensionSectionTable[ExtensionDependencyTargets]{
			"b.gdextension": {"ios": {"res://b": ""}},
		},
	}
	expected := map[ExtensionSection][]string{
		SectionLibraries:    {"a.gdextension", "second.gdextension"},
		SectionDependencies: {"b.gdextension"},
	}
	for _, section := range PartitionedSections() {
		require.Equal(t, expected[section], indexSlice.ExtensionPaths(section),
			"section %q must resolve to its path keys in ascending order", section)
	}
	require.Nil(t, indexSlice.ExtensionPaths(ExtensionSection("frameworks")),
		"an unknown section resolves to no paths")
	require.Nil(t, (&IndexSlice{}).ExtensionPaths(SectionDependencies),
		"a section a slice declares nothing for resolves to no paths")
}

// deeperKeyPath names the extra key component that takes a section's key one
// level past its schema: a [libraries] entry bottoms out at its value, while a
// [dependencies] entry holds one more table of dependency paths.
func deeperKeyPath(section ExtensionSection) string {
	if section == SectionDependencies {
		return `"res://addons/limboai/bin/libai.a"`
	}
	return `"deeper"`
}

func TestIndexPublishedSliceIDsFeedsSelectSlices(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "gpm-index-unsorted.toml"))
	require.NoError(t, err)
	index, err := LoadIndex(data)
	require.NoError(t, err)

	published := index.PublishedSliceIDs()
	require.Equal(t, []SliceID{
		CoreSliceID(),
		{Platform: "android", Architecture: "arm64"},
		{Platform: "ios", Architecture: "arm64"},
	}, published)

	selection, err := SelectSlices([]string{"ios.arm64"}, Host{OperatingSystem: "linux", Architecture: "amd64"}, published, SelectDeclaredPlatforms)
	require.NoError(t, err)
	require.False(t, selection.HostSupported)
	require.Equal(t, []SliceID{CoreSliceID(), {Platform: "ios", Architecture: "arm64"}}, selection.Slices)
}

func TestIndexSaveIsDeterministicAndRoundTrips(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "gpm-index-unsorted.toml"))
	require.NoError(t, err)
	index, err := LoadIndex(data)
	require.NoError(t, err)

	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.toml")
	secondPath := filepath.Join(directory, "second.toml")
	require.NoError(t, index.Save(firstPath))
	require.NoError(t, index.Save(secondPath))

	first, err := os.ReadFile(firstPath)
	require.NoError(t, err)
	second, err := os.ReadFile(secondPath)
	require.NoError(t, err)
	require.Equal(t, string(first), string(second), "Save must be byte-deterministic")

	reloaded, err := LoadIndex(first)
	require.NoError(t, err)
	require.Equal(t, index, reloaded, "load then save then load must yield an equal index")

	thirdPath := filepath.Join(directory, "third.toml")
	require.NoError(t, reloaded.Save(thirdPath))
	third, err := os.ReadFile(thirdPath)
	require.NoError(t, err)
	require.Equal(t, string(first), string(third), "load then save then load must be byte-stable")
}

func TestIndexSaveSortsKeysDeterministically(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "gpm-index-unsorted.toml"))
	require.NoError(t, err)
	index, err := LoadIndex(data)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "gpm-index.toml")
	require.NoError(t, index.Save(path))
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	rendered := string(saved)

	// The six levels the index is deterministic at, outermost to innermost.
	require.True(t, strings.HasPrefix(rendered, "format = 1\nname = "),
		"the top-level fields must be written in struct-declaration order:\n%s", rendered)
	requireAscendingOrder(t, rendered, []string{`[slices."android.arm64"]`, `[slices.core]`, `[slices."ios.arm64"]`})
	requireAscendingOrder(t, rendered, []string{
		`[slices."ios.arm64".libraries]`,
		`[slices."ios.arm64".dependencies]`,
	})
	requireAscendingOrder(t, rendered, []string{
		`[slices."ios.arm64".libraries."limboai.gdextension"]`,
		`[slices."ios.arm64".libraries."nested/second.gdextension"]`,
	})
	requireAscendingOrder(t, rendered, []string{`"ios.template_debug"`, `"ios.template_release"`})
	requireAscendingOrder(t, rendered, []string{`"ios.template_debug.arm64"`, `"ios.template_release.arm64"`})
	// The sixth level, which only [dependencies] reaches: the dependency paths
	// inside one entry. The fixture lists them in the reverse order, so this
	// asserts the sort rather than the order they were written in.
	requireAscendingOrder(t, rendered, []string{
		`[slices."ios.arm64".dependencies."nested/second.gdextension"."ios.template_debug.arm64"]`,
		`[slices."ios.arm64".dependencies."nested/second.gdextension"."ios.template_release.arm64"]`,
		`"res://addons/limboai/nested/bin/aux.a"`,
		`"res://addons/limboai/nested/bin/dep.framework"`,
	})
}

// TestIndexSaveWritesLibrariesBeforeDependencies pins the order the partitioned
// tables are emitted in. Save writes a struct's fields in declaration order, and
// TestIndexSliceFieldOrderMatchesPartitionedSections pins that order against
// PartitionedSections, so the emitted index, validation, and reassembly all
// agree on which section comes first.
func TestIndexSaveWritesLibrariesBeforeDependencies(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "gpm-index.toml"))
	require.NoError(t, err)
	index, err := LoadIndex(data)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "gpm-index.toml")
	require.NoError(t, index.Save(path))
	saved, err := os.ReadFile(path)
	require.NoError(t, err)

	fragments := make([]string, 0, len(PartitionedSections()))
	for _, section := range PartitionedSections() {
		fragments = append(fragments, `[slices."ios.arm64".`+string(section)+`]`)
	}
	requireAscendingOrder(t, string(saved), fragments)
}

func requireAscendingOrder(t *testing.T, rendered string, fragments []string) {
	t.Helper()
	previous := -1
	for _, fragment := range fragments {
		at := strings.Index(rendered, fragment)
		require.GreaterOrEqual(t, at, 0, "expected %q in:\n%s", fragment, rendered)
		require.Greater(t, at, previous, "expected %q after the preceding key in:\n%s", fragment, rendered)
		previous = at
	}
}

func TestIndexSaveWritesAtomicallyLeavingNoTempFile(t *testing.T) {
	index, err := LoadIndex([]byte(validIndexTOML))
	require.NoError(t, err)

	directory := t.TempDir()
	path := filepath.Join(directory, "gpm-index.toml")
	require.NoError(t, index.Save(path))

	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "gpm-index.toml", entries[0].Name())
}

func TestIndexSaveReportsAnUnwritableDirectory(t *testing.T) {
	index, err := LoadIndex([]byte(validIndexTOML))
	require.NoError(t, err)

	err = index.Save(filepath.Join(t.TempDir(), "absent", "gpm-index.toml"))
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.ErrorAs(t, err, &manifestError)
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
}

func TestIndexChecksumHashesRawBytesBeforeParsing(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "gpm-index.toml"))
	require.NoError(t, err)

	digest := sha256.Sum256(data)
	require.Equal(t, hex.EncodeToString(digest[:]), IndexChecksum(data))

	malformed := []byte("format = ")
	_, loadErr := LoadIndex(malformed)
	require.Error(t, loadErr, "the checksum must be computable for bytes that do not parse")
	malformedDigest := sha256.Sum256(malformed)
	require.Equal(t, hex.EncodeToString(malformedDigest[:]), IndexChecksum(malformed))
}

func TestIndexChecksumIsSensitiveToWhitespaceOnlyChanges(t *testing.T) {
	first := IndexChecksum([]byte(validIndexTOML))
	second := IndexChecksum([]byte(validIndexTOML + "\n"))
	require.NotEqual(t, first, second, "the pin is over raw bytes, not over the parsed index")
}

func TestSupportedIndexFormatIsTheSingleDeclaredMaximum(t *testing.T) {
	require.Equal(t, 2, SupportedIndexFormat)
	index, err := LoadIndex([]byte(validIndexTOML))
	require.NoError(t, err)
	require.Equal(t, 1, index.Format)
}

// TestIndexSectionKeyOwnership pins which platform-tagged keys a slice may
// carry: a key with no architecture belongs to every architecture of its
// platform, which is how Godot writes iOS and macOS keys, while a key naming an
// architecture belongs only to that architecture's slice.
func TestIndexSectionKeyOwnership(t *testing.T) {
	for _, testCase := range []struct {
		sliceKey string
		entryKey string
		accepted bool
	}{
		{sliceKey: "ios.arm64", entryKey: "ios.template_release", accepted: true},
		{sliceKey: "ios.arm64", entryKey: "ios.template_release.arm64", accepted: true},
		{sliceKey: "ios.arm64", entryKey: "ios", accepted: true},
		{sliceKey: "macos", entryKey: "macos.template_debug", accepted: true},
		{sliceKey: "macos", entryKey: "macos.universal", accepted: false},
		{sliceKey: "macos.universal", entryKey: "macos.arm64", accepted: false},
		{sliceKey: "ios.arm64", entryKey: "android.arm64", accepted: false},
	} {
		for _, section := range PartitionedSections() {
			name := testCase.sliceKey + "/" + string(section) + "/" + testCase.entryKey
			t.Run(name, func(t *testing.T) {
				document := strings.Replace(validIndexTOML, `[slices."ios.arm64"]`, `[slices."`+testCase.sliceKey+`"]`, 1) +
					renderSectionEntry(
						testCase.sliceKey, section, "limboai.gdextension",
						testCase.entryKey, "res://addons/limboai/bin/libai.a",
					)
				index, err := LoadIndex([]byte(document))
				if testCase.accepted {
					require.NoError(t, err)
					require.NotNil(t, index)
					return
				}
				require.Nil(t, index)
				requireFetchError(t, err)
				require.Contains(t, err.Error(), testCase.entryKey)
			})
		}
	}
}

// dependencyIndexWith renders the ios.arm64 slice carrying one [dependencies]
// entry whose Dictionary is written verbatim, so a row can state the exact
// malformed shape it is about.
func dependencyIndexWith(entry string) string {
	return validIndexTOML + "\n[slices.\"ios.arm64\".dependencies.\"limboai.gdextension\"]\n" +
		"\"ios.template_release\" = " + entry + "\n"
}

// TestIndexRejectsMalformedDependencyTargets asserts every row of the
// fail-closed contract specific to a [dependencies] entry's Godot Dictionary.
// The index is remote content, and both halves of the entry are later used to
// place a file — the key locates it in the installed tree, the destination names
// the export subdirectory Godot writes it into — so both are validated.
func TestIndexRejectsMalformedDependencyTargets(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		document string
		contains []string
	}{
		{
			name:     "the dictionary is empty",
			document: dependencyIndexWith(`{}`),
			contains: []string{"ios.template_release", "limboai.gdextension", "empty"},
		},
		{
			name:     "a dependency path is not a res:// path",
			document: dependencyIndexWith(`{ "bin/dep.a" = "" }`),
			contains: []string{"dependency path", "bin/dep.a", "res://"},
		},
		{
			name:     "a dependency path escapes the res:// root",
			document: dependencyIndexWith(`{ "res://../../etc/passwd" = "" }`),
			contains: []string{"dependency path", "res://"},
		},
		{
			name:     "a dependency path is absolute",
			document: dependencyIndexWith(`{ "/usr/lib/dep.a" = "" }`),
			contains: []string{"dependency path", "res://"},
		},
		{
			name:     "a dependency path uses windows separators",
			document: dependencyIndexWith(`{ 'res://addons\limboai\dep.a' = "" }`),
			contains: []string{"dependency path", "separators"},
		},
		{
			name:     "a destination is absolute",
			document: dependencyIndexWith(`{ "res://addons/limboai/bin/dep.a" = "/usr/local/lib" }`),
			contains: []string{"destination", "must be relative"},
		},
		{
			name:     "a destination traverses",
			document: dependencyIndexWith(`{ "res://addons/limboai/bin/dep.a" = "../elsewhere" }`),
			contains: []string{"destination", "must not escape"},
		},
		{
			name:     "a destination is not in its simplest form",
			document: dependencyIndexWith(`{ "res://addons/limboai/bin/dep.a" = "./Frameworks" }`),
			contains: []string{"destination", "simplest form"},
		},
		{
			name:     "a destination uses windows separators",
			document: dependencyIndexWith(`{ "res://addons/limboai/bin/dep.a" = 'libs\arm64' }`),
			contains: []string{"destination", "separators"},
		},
		{
			name:     "a destination names a host drive",
			document: dependencyIndexWith(`{ "res://addons/limboai/bin/dep.a" = "C:/libs" }`),
			contains: []string{"destination", "must be relative"},
		},
		{
			name:     "a destination is a res:// path",
			document: dependencyIndexWith(`{ "res://addons/limboai/bin/dep.a" = "res://addons/limboai/libs" }`),
			contains: []string{"destination", "relative to the export directory"},
		},
		{
			name:     "a destination contains a control character",
			document: dependencyIndexWith("{ \"res://addons/limboai/bin/dep.a\" = \"libs\\u0001\" }"),
			contains: []string{"destination", "control character"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			index, err := LoadIndex([]byte(testCase.document))
			require.Nil(t, index, "a rejected index must not be reachable")
			requireFetchError(t, err)
			for _, fragment := range testCase.contains {
				require.Contains(t, err.Error(), fragment)
			}
		})
	}
}

// TestIndexAcceptsEveryWellFormedDependencyDestination pins the accepted half of
// the destination contract: the empty destination is the overwhelmingly common
// real case and means the dependency is copied beside the exported binary, and a
// relative subdirectory is what an addon shipping an iOS framework writes.
func TestIndexAcceptsEveryWellFormedDependencyDestination(t *testing.T) {
	for _, destination := range []string{"", "Frameworks", "libs/arm64"} {
		t.Run("destination "+strconv.Quote(destination), func(t *testing.T) {
			index, err := LoadIndex([]byte(dependencyIndexWith(
				`{ "res://addons/limboai/bin/dep.a" = ` + strconv.Quote(destination) + ` }`,
			)))
			require.NoError(t, err)
			require.Equal(t,
				ExtensionDependencyTargets{"res://addons/limboai/bin/dep.a": destination},
				index.Slices["ios.arm64"].Dependencies["limboai.gdextension"]["ios.template_release"],
			)
		})
	}
}

// TestIndexAcceptsADependencyEntryNamingSeveralDependencies covers the shape a
// GDExtension that ships more than one file beside one platform's library
// writes: one entry whose Dictionary names every one of them.
func TestIndexAcceptsADependencyEntryNamingSeveralDependencies(t *testing.T) {
	index, err := LoadIndex([]byte(dependencyIndexWith(
		`{ "res://addons/limboai/bin/dep.a" = "", "res://addons/limboai/bin/aux.framework" = "Frameworks" }`,
	)))
	require.NoError(t, err)
	require.Equal(t, ExtensionDependencyTargets{
		"res://addons/limboai/bin/dep.a":         "",
		"res://addons/limboai/bin/aux.framework": "Frameworks",
	}, index.Slices["ios.arm64"].Dependencies["limboai.gdextension"]["ios.template_release"])
}

// TestIndexReportsTheFirstSectionsProblem pins the order the two sections are
// validated in. A slice with a fault in both must report the [libraries] one,
// because validateIndexSlice runs its validators in PartitionedSections order,
// so an index with several problems always produces the same diagnostic.
func TestIndexReportsTheFirstSectionsProblem(t *testing.T) {
	document := validIndexTOML +
		"\n[slices.\"ios.arm64\".libraries.\"limboai.gdextension\"]\n" +
		"\"windows.template_release\" = \"res://addons/limboai/bin/libai.dll\"\n" +
		"\n[slices.\"ios.arm64\".dependencies.\"limboai.gdextension\"]\n" +
		"\"android.template_release\" = { \"res://addons/limboai/bin/dep.a\" = \"\" }\n"

	index, err := LoadIndex([]byte(document))
	require.Nil(t, index)
	requireFetchError(t, err)
	require.Contains(t, err.Error(), string(SectionLibraries))
	require.NotContains(t, err.Error(), string(SectionDependencies))
}

// TestIndexSliceFieldOrderMatchesPartitionedSections pins that the struct
// declares its partitioned tables in PartitionedSections order, because Save
// writes a struct's fields in declaration order: if the two disagreed, the
// emitted index would not match the order validation and reassembly use.
func TestIndexSliceFieldOrderMatchesPartitionedSections(t *testing.T) {
	sliceType := reflect.TypeOf(IndexSlice{})

	declared := []ExtensionSection{}
	for index := range sliceType.NumField() {
		name := strings.Split(sliceType.Field(index).Tag.Get("toml"), ",")[0]
		if slices.Contains(partitionedSections, ExtensionSection(name)) {
			declared = append(declared, ExtensionSection(name))
		}
	}
	require.Equal(t, PartitionedSections(), declared,
		"IndexSlice must declare one field per partitioned section, in PartitionedSections order")
}

// TestIndexSectionKeyDepthCoversEveryPartitionedSection proves the per-section
// key depth maximum is closed over the vocabulary and derived from one
// declaration: a section added to PartitionedSections without declaring whether
// its entries are tables would let an over-deep key be read as a known one.
func TestIndexSectionKeyDepthCoversEveryPartitionedSection(t *testing.T) {
	require.Len(t, indexSectionEntryIsTable, len(PartitionedSections()))
	for _, section := range PartitionedSections() {
		depth, partitioned := indexSectionKeyDepth(section)
		require.True(t, partitioned, "section %q declares no entry shape", section)
		require.GreaterOrEqual(t, depth, indexEntryKeyDepth,
			"a partitioned section key reaches at least slices.<id>.<section>.<path>.<key>")
	}
	require.Equal(t, 5, indexEntryKeyDepth)
	libraryDepth, _ := indexSectionKeyDepth(SectionLibraries)
	require.Equal(t, indexEntryKeyDepth, libraryDepth)
	dependencyDepth, _ := indexSectionKeyDepth(SectionDependencies)
	require.Equal(t, indexEntryKeyDepth+1, dependencyDepth,
		"a dependencies entry holds one more table, of dependency paths")

	_, partitioned := indexSectionKeyDepth(ExtensionSection("frameworks"))
	require.False(t, partitioned, "an unknown section is not partitioned")
}

// TestIndexRejectsAnEntryWrittenInTheWrongTomlType pins the rule strict decoding
// does not cover on its own: a string where a table is declared decodes into an
// empty map with neither an error nor an undecoded key, so the pre-nesting shape
// has to be rejected by its TOML type rather than left to the empty-table rule,
// which would misdiagnose it.
func TestIndexRejectsAnEntryWrittenInTheWrongTomlType(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		document string
		contains []string
	}{
		{
			name:     "a dependencies entry written as a string",
			document: dependencyIndexWith(`"res://addons/limboai/bin/dep.a"`),
			contains: []string{"String", "dependencies", "export destinations"},
		},
		{
			name:     "a dependencies entry written as an array",
			document: dependencyIndexWith(`["res://addons/limboai/bin/dep.a"]`),
			contains: []string{"Array", "dependencies"},
		},
		{
			name:     "a dependencies entry written as an integer",
			document: dependencyIndexWith(`3`),
			contains: []string{"Integer", "dependencies"},
		},
		{
			name: "a libraries entry written as a table",
			document: validIndexTOML + "\n[slices.\"ios.arm64\".libraries.\"limboai.gdextension\"]\n" +
				"\"ios.template_release\" = { \"res://addons/limboai/bin/libai.a\" = \"\" }\n",
			contains: []string{"libraries"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			index, err := LoadIndex([]byte(testCase.document))
			require.Nil(t, index, "a rejected index must not be reachable")
			requireFetchError(t, err)
			for _, fragment := range testCase.contains {
				require.Contains(t, err.Error(), fragment)
			}
			require.Contains(t, err.Error(), "ios.template_release",
				"the message must name the offending key path")
		})
	}
}

// TestIndexRejectsARelativeEntryValue pins the asymmetry the single published
// form rests on. A .gdextension's author may write an entry value relative to
// the .gdextension and gpm resolves it, but the index's one value form is a
// clean res:// path, so a producer publishing a relative value published
// something gpm package does not emit. The index is remote content, so that is a
// FetchError rather than a ManifestError, and the rule is asserted at every
// depth a path occupies: a [libraries] value and a [dependencies] Dictionary key.
func TestIndexRejectsARelativeEntryValue(t *testing.T) {
	require.Equal(t, 2, SupportedIndexFormat)

	for _, testCase := range []struct {
		name     string
		document string
		contains []string
	}{
		{
			name: "a libraries value is relative to the .gdextension",
			document: validIndexTOML +
				"\n[slices.\"ios.arm64\".libraries.\"limboai.gdextension\"]\n" +
				"\"ios.template_release\" = \"bin/libai.a\"\n",
			contains: []string{"ios.template_release", "limboai.gdextension", "bin/libai.a", resourcePrefix},
		},
		{
			name:     "a dependency path is relative to the .gdextension",
			document: dependencyIndexWith(`{ "bin/dep.a" = "" }`),
			contains: []string{"dependency path", "bin/dep.a", resourcePrefix},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			index, err := LoadIndex([]byte(testCase.document))
			require.Nil(t, index)
			requireFetchError(t, err)
			for _, fragment := range testCase.contains {
				require.Contains(t, err.Error(), fragment)
			}
		})
	}
}
