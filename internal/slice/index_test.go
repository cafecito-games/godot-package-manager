package slice

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

const coreSliceDigest = "2bbed7f0a0218278217edd81a4a4d10f91362aa0c46f80f5931423947004e853"
const platformSliceDigest = "fe7ee35a4e429b6f0e0e07931a046f52b91e1530689ed6ad46fa83239d166711"

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

// platformSliceWithSection renders the ios.arm64 slice carrying one entry in the
// named section, so a contract row can be asserted identically for every
// partitioned section.
func platformSliceWithSection(section ExtensionSection, pathKey, entryKey, entryValue string) string {
	return validIndexTOML + "\n[slices.\"ios.arm64\"." + string(section) + ".\"" + pathKey + "\"]\n" +
		"\"" + entryKey + "\" = \"" + entryValue + "\"\n"
}

func coreSliceWithSection(section ExtensionSection) string {
	return validIndexTOML + "\n[slices.core." + string(section) + ".\"limboai.gdextension\"]\n" +
		"\"ios.template_release\" = \"res://addons/limboai/bin/libai.a\"\n"
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
	require.Len(t, platformSlice.Dependencies["limboai.gdextension"], 2)
}

func TestIndexLoadKeepsBothSectionsDistinctForOneSharedKey(t *testing.T) {
	document := validIndexTOML + `
[slices."ios.arm64".libraries."limboai.gdextension"]
"ios.template_release.arm64" = "res://addons/limboai/bin/libai.ios.arm64.a"

[slices."ios.arm64".dependencies."limboai.gdextension"]
"ios.template_release.arm64" = "res://addons/limboai/bin/dep.framework"
`
	index, err := LoadIndex([]byte(document))
	require.NoError(t, err)

	platformSlice := index.Slices["ios.arm64"]
	require.Equal(t,
		"res://addons/limboai/bin/libai.ios.arm64.a",
		platformSlice.Libraries["limboai.gdextension"]["ios.template_release.arm64"],
	)
	require.Equal(t,
		"res://addons/limboai/bin/dep.framework",
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
			document: strings.Replace(validIndexTOML, "format = 1", "format = 2", 1),
			contains: []string{"2", "1"},
		},
		{
			name:     "format above supported maximum is reported before any other problem",
			document: strings.Replace(validIndexTOML, "format = 1", "format = 2\nmystery = true", 1),
			contains: []string{"format", "2", "1"},
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
					name:     "inner table is empty",
					document: validIndexTOML + "\n[slices.\"ios.arm64\"." + string(section) + ".\"limboai.gdextension\"]\n",
					contains: []string{"limboai.gdextension"},
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
	_, err := LoadIndex([]byte(strings.Replace(validIndexTOML, "format = 1", "format = 2", 1)))
	requireFetchError(t, err)
	require.Contains(t, err.Error(), "2")
	require.Contains(t, err.Error(), "1")
	require.Contains(t, err.Error(), "format")
}

func TestPartitionedSectionsIsClosed(t *testing.T) {
	require.Equal(t, []ExtensionSection{SectionLibraries, SectionDependencies}, PartitionedSections())
	require.Len(t, PartitionedSections(), 2)
}

func TestIndexSliceSectionResolvesEverySection(t *testing.T) {
	indexSlice := &IndexSlice{
		Libraries:    map[string]map[string]string{"a.gdextension": {"ios": "res://a"}},
		Dependencies: map[string]map[string]string{"b.gdextension": {"ios": "res://b"}},
	}
	expected := map[ExtensionSection]string{
		SectionLibraries:    "a.gdextension",
		SectionDependencies: "b.gdextension",
	}
	for _, section := range PartitionedSections() {
		table := indexSlice.Section(section)
		require.NotNil(t, table, "section %q must resolve to a table", section)
		require.Contains(t, table, expected[section])
	}
	require.Nil(t, indexSlice.Section(ExtensionSection("frameworks")), "an unknown section resolves to no table")
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

	selection, err := SelectSlices([]string{"ios.arm64"}, Host{OperatingSystem: "linux", Architecture: "amd64"}, published, false)
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

	require.True(t, strings.HasPrefix(rendered, "format = 1\n"), "format must be written first:\n%s", rendered)
	requireAscendingOrder(t, rendered, []string{`[slices."android.arm64"]`, `[slices.core]`, `[slices."ios.arm64"]`})
	requireAscendingOrder(t, rendered, []string{
		`[slices."ios.arm64".libraries."limboai.gdextension"]`,
		`[slices."ios.arm64".libraries."nested/second.gdextension"]`,
	})
	requireAscendingOrder(t, rendered, []string{`"ios.template_debug"`, `"ios.template_release"`})
	requireAscendingOrder(t, rendered, []string{`"ios.template_debug.arm64"`, `"ios.template_release.arm64"`})
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
	require.Equal(t, 1, SupportedIndexFormat)
	index, err := LoadIndex([]byte(validIndexTOML))
	require.NoError(t, err)
	require.Equal(t, SupportedIndexFormat, index.Format)
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
					"\n[slices.\"" + testCase.sliceKey + "\"." + string(section) + ".\"limboai.gdextension\"]\n" +
					"\"" + testCase.entryKey + "\" = \"res://addons/limboai/bin/libai.a\"\n"
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
