package slice

import (
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

const demoAddonRoot = "res://addons/demo"

const minimalExtension = `[configuration]

entry_symbol = "demo_init"
compatibility_minimum = "4.2"

[libraries]

ios.debug = "res://addons/demo/bin/libdemo.ios.debug.xcframework"
windows.release.x86_64 = "res://addons/demo/bin/libdemo.windows.release.x86_64.dll"
`

// bothSectionsExtension carries the key "ios.template_release.arm64" in both
// partitioned sections, which is the shape Godot's format expects: one names the
// platform's library, the other the files shipped beside it.
const bothSectionsExtension = `[configuration]

entry_symbol = "demo_init"

[libraries]

ios.template_release.arm64 = "res://addons/demo/bin/libdemo.ios.arm64.xcframework"
macos.debug = "res://addons/demo/bin/libdemo.macos.framework"

[dependencies]

ios.template_release.arm64 = "res://addons/demo/bin/libsupport.ios.arm64.a"
`

func TestExtensionPartitionEmptiesLibrariesAndGroupsEntriesBySlice(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(minimalExtension), demoAddonRoot)
	require.NoError(t, err)

	require.Equal(t, `[configuration]

entry_symbol = "demo_init"
compatibility_minimum = "4.2"

[libraries]
`, string(core))

	require.Equal(t, map[SliceID]ExtensionEntries{
		{Platform: "ios"}: {
			SectionLibraries: {"ios.debug": "res://addons/demo/bin/libdemo.ios.debug.xcframework"},
		},
		{Platform: "windows", Architecture: "x86_64"}: {
			SectionLibraries: {"windows.release.x86_64": "res://addons/demo/bin/libdemo.windows.release.x86_64.dll"},
		},
	}, removed)
}

// TestExtensionPartitionKeepsOneKeyPresentInBothSectionsDistinct is the reason
// ExtensionEntries carries a section dimension: a flat map from slice ID to
// entries would drop one of the two entries this fixture declares.
func TestExtensionPartitionKeepsOneKeyPresentInBothSectionsDistinct(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot)
	require.NoError(t, err)

	iosSlice := SliceID{Platform: "ios", Architecture: "arm64"}
	require.Equal(t, ExtensionEntries{
		SectionLibraries:    {"ios.template_release.arm64": "res://addons/demo/bin/libdemo.ios.arm64.xcframework"},
		SectionDependencies: {"ios.template_release.arm64": "res://addons/demo/bin/libsupport.ios.arm64.a"},
	}, removed[iosSlice])
	require.Equal(t, ExtensionEntries{
		SectionLibraries: {"macos.debug": "res://addons/demo/bin/libdemo.macos.framework"},
	}, removed[SliceID{Platform: "macos"}])

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Equal(t,
		"res://addons/demo/bin/libdemo.ios.arm64.xcframework",
		sectionsOf(t, reassembled)["libraries"]["ios.template_release.arm64"],
	)
	require.Equal(t,
		"res://addons/demo/bin/libsupport.ios.arm64.a",
		sectionsOf(t, reassembled)["dependencies"]["ios.template_release.arm64"],
	)
}

// TestExtensionRoundTripReproducesEveryFixture asserts the central contract: the
// installed .gdextension of a project that installed every slice describes
// exactly what the author published, section for section and key for key, with
// every untouched section's bytes intact.
func TestExtensionRoundTripReproducesEveryFixture(t *testing.T) {
	for _, path := range []string{
		"testdata/real/terrabrush.gdextension",
		"testdata/synthetic/both_sections.gdextension",
		"testdata/synthetic/crlf_bom.gdextension",
		"testdata/synthetic/preserved_sections.gdextension",
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			original, err := os.ReadFile(path)
			require.NoError(t, err)

			root := addonRootOfFixture(t, original)
			core, removed, err := PartitionExtension(original, root)
			require.NoError(t, err)
			require.NotEmpty(t, removed, "fixture %s partitions no entries", path)

			reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
			require.NoError(t, err)
			require.Equal(t, sectionsOf(t, original), sectionsOf(t, reassembled),
				"reassembling every slice must reproduce the original's parsed content")

			// Partitioning the reassembled file must land back on the same core body
			// and the same entries, which is what makes a repeat install a no-op.
			secondCore, secondRemoved, err := PartitionExtension(reassembled, root)
			require.NoError(t, err)
			require.Equal(t, string(core), string(secondCore))
			require.Equal(t, removed, secondRemoved)
		})
	}
}

// TestExtensionRoundTripPreservesUntouchedSectionsVerbatim pins the bytes of the
// sections partition never interprets, comments and ordering included.
func TestExtensionRoundTripPreservesUntouchedSectionsVerbatim(t *testing.T) {
	original, err := os.ReadFile("testdata/synthetic/preserved_sections.gdextension")
	require.NoError(t, err)

	core, removed, err := PartitionExtension(original, demoAddonRoot)
	require.NoError(t, err)

	for _, fragment := range []string{
		"; an author's comment above the configuration\n",
		"entry_symbol = \"demo_init\"\n",
		"compatibility_minimum = \"4.2\"\n",
		"[icons]\n",
		"# icons are not platform tagged and are never partitioned\n",
		"DemoNode = \"res://addons/demo/demo_node.svg\"\n",
		"[author_invented]\n",
		"anything = { \"nested\": [1, 2, 3] }\n",
	} {
		require.Contains(t, string(core), fragment, "core must preserve %q verbatim", fragment)
	}
	require.NotContains(t, string(core), "libdemo", "core must carry no platform-tagged entry")

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	for _, fragment := range []string{
		"; an author's comment above the configuration\n",
		"# icons are not platform tagged and are never partitioned\n",
		"[author_invented]\n",
	} {
		require.Contains(t, string(reassembled), fragment)
	}
}

// TestExtensionReassemblyOfOneSliceOmitsEveryOtherPlatform is the partial
// install: a project that needs only iOS must end up with a .gdextension naming
// only the iOS binaries that are actually on disk.
func TestExtensionReassemblyOfOneSliceOmitsEveryOtherPlatform(t *testing.T) {
	original, err := os.ReadFile("testdata/real/terrabrush.gdextension")
	require.NoError(t, err)

	core, removed, err := PartitionExtension(original, "res://addons/terrabrush")
	require.NoError(t, err)

	iosSlice := SliceID{Platform: "ios"}
	require.Contains(t, removed, iosSlice)

	reassembled, err := ReassembleExtension(core, removed, []SliceID{CoreSliceID(), iosSlice})
	require.NoError(t, err)

	libraries := sectionsOf(t, reassembled)["libraries"]
	require.Equal(t, map[string]string{
		"ios.debug":   "res://addons/terrabrush/bin/libterrabrush.ios.debug.a",
		"ios.release": "res://addons/terrabrush/bin/libterrabrush.ios.release.a",
	}, libraries)
	for _, platform := range KnownPlatforms() {
		if platform == "ios" {
			continue
		}
		require.NotContains(t, string(reassembled), platform+".",
			"a partial reassembly must name no %s entry", platform)
	}
}

// TestExtensionReassemblyIsDeterministicAndIdempotent covers the two properties a
// repeat install depends on: the same inputs always produce the same bytes,
// whatever order the caller lists the slices in.
func TestExtensionReassemblyIsDeterministicAndIdempotent(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot)
	require.NoError(t, err)

	selected := allSlices(removed)
	first, err := ReassembleExtension(core, removed, selected)
	require.NoError(t, err)
	second, err := ReassembleExtension(core, removed, selected)
	require.NoError(t, err)
	require.Equal(t, string(first), string(second), "reassembling twice must produce identical bytes")

	reversed := make([]SliceID, len(selected))
	for index, id := range selected {
		reversed[len(selected)-1-index] = id
	}
	reversed = append(reversed, reversed...)
	shuffled, err := ReassembleExtension(core, removed, reversed)
	require.NoError(t, err)
	require.Equal(t, string(first), string(shuffled),
		"the order and multiplicity of the selected set must not change the output")

	libraries := sectionsOf(t, first)["libraries"]
	require.Len(t, libraries, 2)
	require.True(t, strings.Index(string(first), "ios.template_release.arm64 =") < strings.Index(string(first), "macos.debug ="),
		"entries within a section are emitted sorted by key")
	require.True(t, strings.Index(string(first), "[libraries]") < strings.Index(string(first), "[dependencies]"),
		"sections are emitted in the order of PartitionedSections")
}

// TestExtensionReassemblySortsEntriesAcrossSlices pins the sort that determinism
// actually depends on. Within one slice the entries are already visited in key
// order, so the sort only shows itself where two slices contribute keys to one
// section whose order disagrees with the order of the slices themselves: the
// "macos" slice owns "macos.template_debug" while the "macos.arm64" slice owns
// "macos.arm64", and the slice order is the reverse of the key order.
func TestExtensionReassemblySortsEntriesAcrossSlices(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[libraries]

macos.template_debug = "res://addons/demo/bin/libdemo.macos.framework"
macos.arm64 = "res://addons/demo/bin/libdemo.macos.arm64.framework"
`
	core, removed, err := PartitionExtension([]byte(content), demoAddonRoot)
	require.NoError(t, err)
	require.Contains(t, removed, SliceID{Platform: "macos"})
	require.Contains(t, removed, SliceID{Platform: "macos", Architecture: "arm64"})

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Less(t,
		strings.Index(string(reassembled), "macos.arm64 ="),
		strings.Index(string(reassembled), "macos.template_debug ="),
		"entries of one section are sorted by key even when their slices sort the other way")
}

func TestExtensionPartitionDoesNotMutateItsInput(t *testing.T) {
	original, err := os.ReadFile("testdata/real/terrabrush.gdextension")
	require.NoError(t, err)
	untouched := bytes.Clone(original)

	_, _, err = PartitionExtension(original, "res://addons/terrabrush")
	require.NoError(t, err)
	require.Equal(t, untouched, original, "partition must not write through its input buffer")
}

func TestExtensionRoundTripPreservesCarriageReturnsAndByteOrderMark(t *testing.T) {
	original, err := os.ReadFile("testdata/synthetic/crlf_bom.gdextension")
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(original, []byte("\ufeff")), "the fixture must carry a BOM")
	require.Contains(t, string(original), "\r\n")

	core, removed, err := PartitionExtension(original, demoAddonRoot)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(core, []byte("\ufeff")), "core must keep the BOM")
	require.NotContains(t, strings.ReplaceAll(string(core), "\r\n", ""), "\n",
		"every line of core must keep its CRLF ending")

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(reassembled, []byte("\ufeff")), "the installed file must keep the BOM")
	require.NotContains(t, strings.ReplaceAll(string(reassembled), "\r\n", ""), "\n",
		"generated lines must use the document's own line ending")
}

// TestExtensionPartitionsEachFileIndependently asserts an addon shipping several
// .gdextension files has no cross-talk between them: each partitions against its
// own bytes, and reassembling one never pulls in the other's entries.
func TestExtensionPartitionsEachFileIndependently(t *testing.T) {
	first := `[configuration]

entry_symbol = "first_init"

[libraries]

ios.debug = "res://addons/demo/bin/libfirst.ios.xcframework"
`
	second := `[configuration]

entry_symbol = "second_init"

[libraries]

ios.debug = "res://addons/demo/bin/libsecond.ios.xcframework"
linux.release.x86_64 = "res://addons/demo/bin/libsecond.linux.so"
`

	firstCore, firstRemoved, err := PartitionExtension([]byte(first), demoAddonRoot)
	require.NoError(t, err)
	secondCore, secondRemoved, err := PartitionExtension([]byte(second), demoAddonRoot)
	require.NoError(t, err)

	iosSlice := SliceID{Platform: "ios"}
	require.Equal(t, "res://addons/demo/bin/libfirst.ios.xcframework",
		firstRemoved[iosSlice][SectionLibraries]["ios.debug"])
	require.Equal(t, "res://addons/demo/bin/libsecond.ios.xcframework",
		secondRemoved[iosSlice][SectionLibraries]["ios.debug"])
	require.NotContains(t, firstRemoved, SliceID{Platform: "linux", Architecture: "x86_64"})

	firstReassembled, err := ReassembleExtension(firstCore, firstRemoved, allSlices(firstRemoved))
	require.NoError(t, err)
	require.Equal(t, map[string]string{"ios.debug": "res://addons/demo/bin/libfirst.ios.xcframework"},
		sectionsOf(t, firstReassembled)["libraries"])
	require.Contains(t, string(firstReassembled), "first_init")

	secondReassembled, err := ReassembleExtension(secondCore, secondRemoved, allSlices(secondRemoved))
	require.NoError(t, err)
	require.Equal(t, sectionsOf(t, []byte(second)), sectionsOf(t, secondReassembled))
}

// TestExtensionEntriesAreAssignableToAnIndexSlice asserts the shape contract
// between partition and the index: a section's table is written straight into an
// IndexSlice with no conversion, and survives a save and a load.
func TestExtensionEntriesAreAssignableToAnIndexSlice(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot)
	require.NoError(t, err)

	const extensionPath = "demo.gdextension"
	index := &Index{
		Format:  SupportedIndexFormat,
		Name:    "demo",
		Version: "1.0.0",
		Slices: map[string]*IndexSlice{
			CorePlatform: {
				File:   "demo-core.zip",
				SHA256: strings.Repeat("a", 64),
				Size:   1,
			},
		},
	}
	for id, entries := range removed {
		indexSlice := &IndexSlice{
			File:   "demo-" + strings.ReplaceAll(id.String(), ".", "-") + ".zip",
			SHA256: strings.Repeat("b", 64),
			Size:   2,
		}
		// The assignment under test: no conversion, no translation layer.
		if libraries := entries[SectionLibraries]; libraries != nil {
			indexSlice.Libraries = map[string]map[string]string{extensionPath: libraries}
		}
		if dependencies := entries[SectionDependencies]; dependencies != nil {
			indexSlice.Dependencies = map[string]map[string]string{extensionPath: dependencies}
		}
		index.Slices[id.String()] = indexSlice
	}

	path := filepath.Join(t.TempDir(), "gpm-index.toml")
	require.NoError(t, index.Save(path))
	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	loaded, err := LoadIndex(saved)
	require.NoError(t, err)

	reloaded := map[SliceID]ExtensionEntries{}
	for key, indexSlice := range loaded.Slices {
		id, err := ParseSliceID(key)
		require.NoError(t, err)
		for _, section := range PartitionedSections() {
			table := indexSlice.Section(section)[extensionPath]
			if table == nil {
				continue
			}
			if reloaded[id] == nil {
				reloaded[id] = ExtensionEntries{}
			}
			reloaded[id][section] = table
		}
	}
	require.Equal(t, removed, reloaded, "a partition result must survive the index unchanged")

	reassembled, err := ReassembleExtension(core, reloaded, allSlices(reloaded))
	require.NoError(t, err)
	require.Equal(t, sectionsOf(t, []byte(bothSectionsExtension)), sectionsOf(t, reassembled))
}

// TestExtensionPartitionAndReassemblyTouchNoFilesystem asserts the property the
// addonRoot parameter exists for. The behavioral half runs both functions on
// in-memory bytes alone; the structural half pins it by rejecting any filesystem
// import in the two files that implement them, so the property cannot regress
// into a disk lookup that happens to work in a test.
func TestExtensionPartitionAndReassemblyTouchNoFilesystem(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot)
	require.NoError(t, err)
	_, err = ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)

	forbidden := []string{"os", "io", "io/ioutil", "path/filepath", "net", "net/http", "os/exec"}
	fileSet := token.NewFileSet()
	for _, name := range []string{"gdextension.go", "godotconfig.go"} {
		parsed, err := parser.ParseFile(fileSet, name, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, declared := range parsed.Imports {
			path, err := strconv.Unquote(declared.Path.Value)
			require.NoError(t, err)
			require.NotContains(t, forbidden, path,
				"%s imports %s; partition and reassembly reach no filesystem, which is why the addon root is a parameter",
				name, path)
		}
	}
}

// TestExtensionPartitionHandlesEverySectionOfTheVocabulary proves the
// implementation is driven by PartitionedSections rather than by a list of its
// own: the fixture and the expectations are both generated from that single
// declaration, so a section added there without being handled here fails.
func TestExtensionPartitionHandlesEverySectionOfTheVocabulary(t *testing.T) {
	sections := PartitionedSections()
	require.NotEmpty(t, sections)

	var builder strings.Builder
	builder.WriteString("[configuration]\n\nentry_symbol = \"demo_init\"\n")
	for _, section := range sections {
		fmt.Fprintf(&builder, "\n[%s]\n\nmacos.debug = \"res://addons/demo/bin/%s.framework\"\n", section, section)
	}

	core, removed, err := PartitionExtension([]byte(builder.String()), demoAddonRoot)
	require.NoError(t, err)

	expected := ExtensionEntries{}
	for _, section := range sections {
		expected[section] = map[string]string{
			"macos.debug": fmt.Sprintf("res://addons/demo/bin/%s.framework", section),
		}
		require.NotContains(t, string(core), string(section)+".framework",
			"[%s] must be emptied in the core body", section)
		require.Contains(t, string(core), "["+string(section)+"]",
			"[%s] must keep its header in the core body", section)
	}
	require.Equal(t, map[SliceID]ExtensionEntries{{Platform: "macos"}: expected}, removed)

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	parsedSections := sectionsOf(t, reassembled)
	for _, section := range sections {
		require.Equal(t, map[string]string{
			"macos.debug": fmt.Sprintf("res://addons/demo/bin/%s.framework", section),
		}, parsedSections[string(section)])
	}
}

// TestExtensionPartitionIsFailClosedPerSection asserts every entry-level row of
// the issue's fail-closed contract, for each partitioned section, because the
// rules are identical in each and must stay identical.
func TestExtensionPartitionIsFailClosedPerSection(t *testing.T) {
	rows := []struct {
		name            string
		body            string
		addonRoot       string
		expectedMessage []string
	}{
		{
			name:            "unknown platform component",
			body:            `solaris.x86_64 = "res://addons/demo/bin/libdemo.so"`,
			expectedMessage: []string{"solaris", "unknown platform", "windows"},
		},
		{
			name:            "unknown architecture component",
			body:            `windows.release.sparc = "res://addons/demo/bin/libdemo.dll"`,
			expectedMessage: []string{"sparc", "x86_64"},
		},
		{
			name:            "no platform component at all",
			body:            `libdemo = "res://addons/demo/bin/libdemo.so"`,
			expectedMessage: []string{"libdemo", "unknown platform"},
		},
		{
			name:            "value is not a resource path",
			body:            `windows.release.x86_64 = "bin/libdemo.dll"`,
			expectedMessage: []string{"bin/libdemo.dll", "res://"},
		},
		{
			name:            "value is absolute on the host filesystem",
			body:            `windows.release.x86_64 = "/usr/local/lib/libdemo.so"`,
			expectedMessage: []string{"/usr/local/lib/libdemo.so", "res://"},
		},
		{
			name:            "value names a host drive",
			body:            `windows.release.x86_64 = "res://C:/libdemo.dll"`,
			expectedMessage: []string{"must be relative"},
		},
		{
			name:            "value traverses out of the addon",
			body:            `windows.release.x86_64 = "res://addons/demo/../other/libdemo.dll"`,
			expectedMessage: []string{"must not escape the addon root"},
		},
		{
			name:            "value uses windows separators",
			body:            `windows.release.x86_64 = "res://addons/demo\\bin\\libdemo.dll"`,
			expectedMessage: []string{"separators"},
		},
		{
			name:            "value points outside the addon root",
			body:            `windows.release.x86_64 = "res://addons/other/bin/libdemo.dll"`,
			expectedMessage: []string{"outside the addon root", demoAddonRoot},
		},
		{
			name:            "value is the addon root itself",
			body:            fmt.Sprintf("windows.release.x86_64 = %q", demoAddonRoot),
			expectedMessage: []string{"outside the addon root"},
		},
		{
			name: "duplicate key in one section",
			body: `windows.release.x86_64 = "res://addons/demo/bin/libdemo.dll"
windows.release.x86_64 = "res://addons/demo/bin/libother.dll"`,
			expectedMessage: []string{"windows.release.x86_64", "more than once"},
		},
		{
			name:            "value is a godot dictionary",
			body:            `ios.release = { "res://addons/demo/bin/libdemo.a": "" }`,
			expectedMessage: []string{"dictionary", "exactly one quoted"},
		},
		{
			name: "value is a dictionary written across several lines",
			body: `ios.release = {
	"res://addons/demo/bin/libdemo.a" : "",
	"res://addons/demo/bin/libother.a" : ""
}`,
			expectedMessage: []string{"dictionary", "ios.release"},
		},
		{
			name:            "value is not quoted",
			body:            `windows.release.x86_64 = 42`,
			expectedMessage: []string{"42", "not a quoted"},
		},
		{
			name:            "value is followed by something other than a comment",
			body:            `windows.release.x86_64 = "res://addons/demo/bin/libdemo.dll" trailing`,
			expectedMessage: []string{"trailing"},
		},
		{
			name:            "addon root is not a resource path",
			body:            `windows.release.x86_64 = "res://addons/demo/bin/libdemo.dll"`,
			addonRoot:       "addons/demo",
			expectedMessage: []string{"invalid addon root", "res://"},
		},
		{
			name:            "addon root traverses",
			body:            `windows.release.x86_64 = "res://addons/demo/bin/libdemo.dll"`,
			addonRoot:       "res://addons/../demo",
			expectedMessage: []string{"invalid addon root"},
		},
	}

	for _, section := range PartitionedSections() {
		for _, row := range rows {
			t.Run(string(section)+"/"+row.name, func(t *testing.T) {
				content := fmt.Sprintf("[configuration]\n\nentry_symbol = \"demo_init\"\n\n[%s]\n\n%s\n", section, row.body)
				addonRoot := row.addonRoot
				if addonRoot == "" {
					addonRoot = demoAddonRoot
				}

				core, removed, err := PartitionExtension([]byte(content), addonRoot)
				requireManifestError(t, err, row.expectedMessage...)
				require.Nil(t, core, "a rejected .gdextension must yield no core body")
				require.Nil(t, removed, "a rejected .gdextension must yield no entries")
			})
		}
	}
}

// TestExtensionPartitionIsFailClosedOnTheDocument asserts the document-level rows
// of the contract: shapes that are not a Godot config at all.
func TestExtensionPartitionIsFailClosedOnTheDocument(t *testing.T) {
	rows := []struct {
		name            string
		content         string
		expectedMessage []string
	}{
		{
			name:            "unclosed section header",
			content:         "[configuration\nentry_symbol = \"demo_init\"\n",
			expectedMessage: []string{"line 1", "not closed"},
		},
		{
			name:            "empty section name",
			content:         "[]\nentry_symbol = \"demo_init\"\n",
			expectedMessage: []string{"line 1", "names no section"},
		},
		{
			name:            "line that is not a statement",
			content:         "[configuration]\nentry_symbol\n",
			expectedMessage: []string{"line 2", "key = value"},
		},
		{
			name:            "statement with no key",
			content:         "[configuration]\n= \"demo_init\"\n",
			expectedMessage: []string{"line 2", "names no key"},
		},
		{
			name:            "value that is never closed",
			content:         "[libraries]\nmacos.debug = \"res://addons/demo/bin/libdemo.framework\n",
			expectedMessage: []string{"line 2", "never closed"},
		},
		{
			name:            "dictionary that is never closed",
			content:         "[dependencies]\nmacos.debug = {\n  \"res://addons/demo/bin/x.dylib\": \"\"\n",
			expectedMessage: []string{"line 2", "never closed"},
		},
		{
			name:            "partitioned section spelled with different case",
			content:         "[Libraries]\n\nmacos.debug = \"res://addons/demo/bin/a.framework\"\n",
			expectedMessage: []string{"[Libraries]", "[libraries]", "case-sensitive"},
		},
		{
			name:            "dependencies section spelled with different case",
			content:         "[libraries]\n\n[DEPENDENCIES]\n\nmacos.debug = \"res://addons/demo/bin/a.dylib\"\n",
			expectedMessage: []string{"[DEPENDENCIES]", "[dependencies]", "case-sensitive"},
		},
		{
			name:            "partitioned section declared twice",
			content:         "[libraries]\n\nmacos.debug = \"res://addons/demo/bin/a.framework\"\n\n[libraries]\n\nios.debug = \"res://addons/demo/bin/b.xcframework\"\n",
			expectedMessage: []string{"[libraries]", "more than once"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			core, removed, err := PartitionExtension([]byte(row.content), demoAddonRoot)
			requireManifestError(t, err, row.expectedMessage...)
			require.Nil(t, core)
			require.Nil(t, removed)
		})
	}
}

// TestExtensionPartitionRejectsRealWorldShapesTheIndexCannotCarry pins the two
// checked-in real .gdextension files whose values the index schema cannot
// represent. They are rejected rather than partially partitioned, so a producer
// learns at packaging time instead of shipping a .gdextension that misdescribes
// the installed tree.
func TestExtensionPartitionRejectsRealWorldShapesTheIndexCannotCarry(t *testing.T) {
	t.Run("dictionary dependencies", func(t *testing.T) {
		content, err := os.ReadFile("testdata/limboai.gdextension")
		require.NoError(t, err)
		_, _, err = PartitionExtension(content, "res://addons/limboai")
		requireManifestError(t, err, "dependencies", "dictionary")
	})

	t.Run("library paths relative to the .gdextension", func(t *testing.T) {
		content, err := os.ReadFile("testdata/godot_jolt.gdextension")
		require.NoError(t, err)
		_, _, err = PartitionExtension(content, "res://addons/godot_jolt")
		requireManifestError(t, err, "libraries", "res://")
	})
}

func TestExtensionReassemblyIsFailClosed(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot)
	require.NoError(t, err)
	iosSlice := SliceID{Platform: "ios", Architecture: "arm64"}
	macosSlice := SliceID{Platform: "macos"}

	rows := []struct {
		name            string
		core            []byte
		entries         map[SliceID]ExtensionEntries
		selected        []SliceID
		expectedMessage []string
	}{
		{
			name:            "core body does not parse",
			core:            []byte("[libraries\n"),
			selected:        []SliceID{CoreSliceID()},
			expectedMessage: []string{"line 1", "not closed"},
		},
		{
			name:            "core body still carries platform-tagged entries",
			core:            []byte(minimalExtension),
			selected:        []SliceID{CoreSliceID()},
			expectedMessage: []string{"without being partitioned", "libraries"},
		},
		{
			name:            "core body declares a partitioned section twice",
			core:            []byte("[libraries]\n\n[dependencies]\n\n[libraries]\n"),
			selected:        []SliceID{CoreSliceID()},
			expectedMessage: []string{"[libraries]", "more than once"},
		},
		{
			name:            "core body spells a partitioned section with different case",
			core:            []byte("[Libraries]\n"),
			selected:        []SliceID{CoreSliceID()},
			expectedMessage: []string{"[Libraries]", "case-sensitive"},
		},
		{
			name:            "a selected slice has no entries in the index",
			core:            core,
			entries:         map[SliceID]ExtensionEntries{macosSlice: removed[macosSlice]},
			selected:        []SliceID{CoreSliceID(), macosSlice, iosSlice},
			expectedMessage: []string{"ios.arm64", "declares no entries"},
		},
		{
			name:            "the core slice declares entries",
			core:            core,
			entries:         map[SliceID]ExtensionEntries{CoreSliceID(): removed[macosSlice]},
			selected:        []SliceID{CoreSliceID()},
			expectedMessage: []string{CorePlatform, "belong to platform slices"},
		},
		{
			name: "an entry key belongs to another slice",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {SectionLibraries: {"windows.release.x86_64": "res://addons/demo/bin/libdemo.dll"}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"windows.release.x86_64", "belongs to slice", "macos"},
		},
		{
			name: "an entry key is not a platform tag",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {SectionLibraries: {"libdemo": "res://addons/demo/bin/libdemo.dylib"}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"libdemo", "unknown platform"},
		},
		{
			name: "an entry value is not a resource path",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {SectionLibraries: {"macos.debug": "bin/libdemo.dylib"}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"bin/libdemo.dylib", "res://"},
		},
		{
			name: "an entry value would not survive being written back",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {SectionLibraries: {"macos.debug": `res://addons/demo/bin/lib"demo.dylib`}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"must not contain a quote"},
		},
		{
			name: "two slices declare the same key in one section",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {SectionLibraries: {"macos.debug": "res://addons/demo/bin/a.framework"}},
				{Platform: "macos", Architecture: "universal"}: {
					SectionLibraries: {"macos.debug": "res://addons/demo/bin/b.framework"},
				},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice, {Platform: "macos", Architecture: "universal"}},
			expectedMessage: []string{"macos.debug", "both declare key"},
		},
		{
			name: "the core body declares no section for an entry",
			core: []byte("[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]\n"),
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {SectionDependencies: {"macos.debug": "res://addons/demo/bin/support.dylib"}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"dependencies", "declares no [dependencies] section"},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			reassembled, err := ReassembleExtension(row.core, row.entries, row.selected)
			require.Nil(t, reassembled, "a rejected reassembly must yield no file")

			var fetchError *output.FetchError
			require.Error(t, err)
			require.True(t, errors.As(err, &fetchError),
				"a reassembly failure is remote content being wrong, so it is a FetchError: got %T", err)
			require.Equal(t, output.ExitFetch, output.CodeFor(err),
				"a reassembly failure must report exit code %d", output.ExitFetch)
			for _, fragment := range row.expectedMessage {
				require.Contains(t, err.Error(), fragment)
			}
		})
	}
}

// TestExtensionReassemblyAcceptsASliceWithNoEntriesForThisFile covers the
// legitimate shape the "no entries" rule has to leave room for: an addon shipping
// several .gdextension files has slices that carry entries in one of them and
// none in another, and declaring an empty set says so explicitly.
func TestExtensionReassemblyAcceptsASliceWithNoEntriesForThisFile(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot)
	require.NoError(t, err)

	windowsSlice := SliceID{Platform: "windows", Architecture: "x86_64"}
	entries := map[SliceID]ExtensionEntries{windowsSlice: {}}
	for id, sliceEntries := range removed {
		entries[id] = sliceEntries
	}

	reassembled, err := ReassembleExtension(core, entries, append(allSlices(removed), windowsSlice))
	require.NoError(t, err)
	require.Equal(t, sectionsOf(t, []byte(bothSectionsExtension)), sectionsOf(t, reassembled))
	require.NotContains(t, string(reassembled), "windows")
}

// TestExtensionReassemblyIgnoresUnselectedSlices asserts entries for a slice the
// project did not install never reach the file, which is what keeps the installed
// .gdextension a description of what is on disk.
func TestExtensionReassemblyIgnoresUnselectedSlices(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot)
	require.NoError(t, err)

	reassembled, err := ReassembleExtension(core, removed, []SliceID{CoreSliceID()})
	require.NoError(t, err)
	require.Equal(t, string(core), string(reassembled),
		"selecting only core must reproduce the core body exactly")
}

// allSlices returns every slice an index declares entries for, plus the core
// slice every project installs.
func allSlices(entries map[SliceID]ExtensionEntries) []SliceID {
	selected := []SliceID{CoreSliceID()}
	for id := range entries {
		selected = append(selected, id)
	}
	sort.Slice(selected, func(left, right int) bool {
		return selected[left].String() < selected[right].String()
	})
	return selected
}

// sectionsOf parses a .gdextension into section name → key → raw value, which is
// the semantic content a round trip has to preserve.
func sectionsOf(t *testing.T, content []byte) map[string]map[string]string {
	t.Helper()
	document, err := parseGodotConfig(content)
	require.NoError(t, err)

	sections := map[string]map[string]string{}
	for _, block := range document.blocks {
		if block.name == "" && block.headerLine == "" {
			continue
		}
		table := sections[block.name]
		if table == nil {
			table = map[string]string{}
			sections[block.name] = table
		}
		for _, entry := range block.entries {
			table[entry.key] = strings.Trim(entry.value, `"`)
		}
	}
	return sections
}

// addonRootOfFixture derives the res:// root a fixture's entries live under by
// taking the common "res://addons/<name>" prefix of its first entry. It exists so
// a fixture can be added without also hard-coding its root here.
func addonRootOfFixture(t *testing.T, content []byte) string {
	t.Helper()
	document, err := parseGodotConfig(content)
	require.NoError(t, err)

	for _, section := range PartitionedSections() {
		block := document.block(section)
		if block == nil || len(block.entries) == 0 {
			continue
		}
		value := strings.Trim(block.entries[0].value, `"`)
		components := strings.Split(strings.TrimPrefix(value, resourcePrefix), "/")
		require.GreaterOrEqual(t, len(components), 2, "fixture value %q has no addon root", value)
		return resourcePrefix + strings.Join(components[:2], "/")
	}
	t.Fatalf("fixture declares no partitioned entry")
	return ""
}

func requireManifestError(t *testing.T, err error, fragments ...string) {
	t.Helper()
	require.Error(t, err)

	var manifestError *output.ManifestError
	require.True(t, errors.As(err, &manifestError),
		"a packaging failure is the author's own file being wrong, so it is a ManifestError: got %T", err)
	require.Equal(t, output.ExitManifest, output.CodeFor(err),
		"a packaging failure must report exit code %d", output.ExitManifest)
	for _, fragment := range fragments {
		require.Contains(t, err.Error(), fragment)
	}
}

// TestExtensionPartitionPreservesAFileWithNoFinalNewline covers the byte-fidelity
// edge of a file whose last line has no ending: a document with nothing to
// partition comes back unchanged, and one whose last section is partitioned still
// ends with a line ending so its entries have a line of their own to go on.
func TestExtensionPartitionPreservesAFileWithNoFinalNewline(t *testing.T) {
	t.Run("nothing to partition", func(t *testing.T) {
		const content = "[configuration]\n\nentry_symbol = \"demo_init\"\n\n[icons]\n\nDemoNode = \"res://addons/demo/demo_node.svg\""
		core, removed, err := PartitionExtension([]byte(content), demoAddonRoot)
		require.NoError(t, err)
		require.Empty(t, removed)
		require.Equal(t, content, string(core), "a document with no partitioned entry is reproduced byte for byte")

		reassembled, err := ReassembleExtension(core, removed, []SliceID{CoreSliceID()})
		require.NoError(t, err)
		require.Equal(t, content, string(reassembled))
	})

	t.Run("partitioned section ends the file", func(t *testing.T) {
		const content = "[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]\n\nmacos.debug = \"res://addons/demo/bin/libdemo.framework\""
		core, removed, err := PartitionExtension([]byte(content), demoAddonRoot)
		require.NoError(t, err)
		require.Equal(t, "[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]\n", string(core))

		reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
		require.NoError(t, err)
		require.Equal(t, sectionsOf(t, []byte(content)), sectionsOf(t, reassembled))

		secondCore, secondRemoved, err := PartitionExtension(reassembled, demoAddonRoot)
		require.NoError(t, err)
		require.Equal(t, string(core), string(secondCore))
		require.Equal(t, removed, secondRemoved)
	})

	t.Run("partitioned header ends the file", func(t *testing.T) {
		const content = "[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]"
		core, removed, err := PartitionExtension([]byte(content), demoAddonRoot)
		require.NoError(t, err)
		require.Empty(t, removed)
		require.Equal(t, "[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]\n", string(core))
	})
}
