package slice

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/cafecito-games/gdparser/configfile"
	"github.com/cafecito-games/gdparser/configfile/ast"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

const demoAddonRoot = "res://addons/demo"

// demoExtensionPath is the demo addon's .gdextension path relative to its addon
// root, which is the form PartitionExtension takes and the form the index uses
// as that file's section path key.
const demoExtensionPath = "demo.gdextension"

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
	core, removed, err := PartitionExtension([]byte(minimalExtension), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)

	require.Equal(t, "[configuration]\n"+
		"entry_symbol=\"demo_init\"\n"+
		"compatibility_minimum=\"4.2\"\n"+
		"\n"+
		"[libraries]\n", string(core))

	require.Equal(t, map[SliceID]ExtensionEntries{
		{Platform: "ios"}: {
			Libraries: ExtensionEntryTable[string]{
				"ios.debug": "res://addons/demo/bin/libdemo.ios.debug.xcframework",
			},
		},
		{Platform: "windows", Architecture: "x86_64"}: {
			Libraries: ExtensionEntryTable[string]{
				"windows.release.x86_64": "res://addons/demo/bin/libdemo.windows.release.x86_64.dll",
			},
		},
	}, removed)
}

// TestExtensionPartitionKeepsOneKeyPresentInBothSectionsDistinct is the reason
// ExtensionEntries carries a section dimension: a flat map from slice ID to
// entries would drop one of the two entries this fixture declares.
func TestExtensionPartitionKeepsOneKeyPresentInBothSectionsDistinct(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)

	iosSlice := SliceID{Platform: "ios", Architecture: "arm64"}
	require.Equal(t, ExtensionEntries{
		Libraries: ExtensionEntryTable[string]{
			"ios.template_release.arm64": "res://addons/demo/bin/libdemo.ios.arm64.xcframework",
		},
		Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
			"ios.template_release.arm64": {"res://addons/demo/bin/libsupport.ios.arm64.a": ""},
		},
	}, removed[iosSlice])
	require.Equal(t, ExtensionEntries{
		Libraries: ExtensionEntryTable[string]{
			"macos.debug": "res://addons/demo/bin/libdemo.macos.framework",
		},
	}, removed[SliceID{Platform: "macos"}])

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Equal(t,
		"res://addons/demo/bin/libdemo.ios.arm64.xcframework",
		pathsOf(t, reassembled)["libraries"]["ios.template_release.arm64"],
	)
	require.Equal(t,
		ExtensionDependencyTargets{"res://addons/demo/bin/libsupport.ios.arm64.a": ""},
		targetsOf(t, reassembled)["ios.template_release.arm64"],
	)
}

// TestExtensionRoundTripReproducesEveryFixture asserts the central contract: the
// installed .gdextension of a project that installed every slice describes
// exactly what the author published, section for section and key for key, with
// every untouched section's bytes intact.
//
// Every fixture here writes its entry values as res:// paths, which is the one
// published form, so parsed-content identity is exact. A fixture whose values
// are written relative to the .gdextension round-trips semantically instead —
// each value becomes its res:// resolution — and is asserted by
// TestExtensionRelativeValuesRoundTripAsCanonicalResourcePaths rather than
// weakening the claim made here.
//
// Each fixture's addon root and .gdextension path are spelled out rather than
// derived from its first value, because a relative value carries no root to
// derive one from and one table cannot have two rules.
func TestExtensionRoundTripReproducesEveryFixture(t *testing.T) {
	for _, fixture := range []struct {
		path          string
		addonRoot     string
		extensionPath string
	}{
		{
			path:          "testdata/limboai.gdextension",
			addonRoot:     "res://addons/limboai",
			extensionPath: "limboai.gdextension",
		},
		{
			path:          "testdata/real/terrabrush.gdextension",
			addonRoot:     "res://addons/terrabrush",
			extensionPath: "terrabrush.gdextension",
		},
		{
			path:          "testdata/synthetic/both_sections.gdextension",
			addonRoot:     demoAddonRoot,
			extensionPath: demoExtensionPath,
		},
		{
			path:          "testdata/synthetic/crlf_bom.gdextension",
			addonRoot:     demoAddonRoot,
			extensionPath: demoExtensionPath,
		},
		{
			path:          "testdata/synthetic/preserved_sections.gdextension",
			addonRoot:     demoAddonRoot,
			extensionPath: demoExtensionPath,
		},
	} {
		t.Run(filepath.Base(fixture.path), func(t *testing.T) {
			original, err := os.ReadFile(fixture.path)
			require.NoError(t, err)

			core, removed, err := PartitionExtension(original, fixture.addonRoot, fixture.extensionPath)
			require.NoError(t, err)
			require.NotEmpty(t, removed, "fixture %s partitions no entries", fixture.path)

			reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
			require.NoError(t, err)
			require.Equal(t, sectionsOf(t, original), sectionsOf(t, reassembled),
				"reassembling every slice must reproduce the original's parsed content")

			// Partitioning the reassembled file must land back on the same core body
			// and the same entries, which is what makes a repeat install a no-op.
			secondCore, secondRemoved, err := PartitionExtension(reassembled, fixture.addonRoot, fixture.extensionPath)
			require.NoError(t, err)
			require.Equal(t, string(core), string(secondCore))
			require.Equal(t, removed, secondRemoved)
		})
	}
}

// TestExtensionRoundTripPreservesUntouchedSectionsAndComments pins the content
// of the sections partition never interprets. Emission is canonical, so a
// section's spelling may be normalized, but no section, key, value, or comment
// may be dropped or altered, and their order must survive.
func TestExtensionRoundTripPreservesUntouchedSectionsAndComments(t *testing.T) {
	original, err := os.ReadFile("testdata/synthetic/preserved_sections.gdextension")
	require.NoError(t, err)

	core, removed, err := PartitionExtension(original, demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)

	for _, fragment := range []string{
		"; an author's comment above the configuration",
		"[configuration]",
		`entry_symbol="demo_init"`,
		`compatibility_minimum="4.2"`,
		"reloadable=true",
		"[icons]",
		"# icons are not platform tagged and are never partitioned",
		`DemoNode="res://addons/demo/demo_node.svg"`,
		`DemoResource="res://addons/demo/demo_resource.svg"`,
		"[author_invented]",
		`anything={`,
		"[libraries]",
		"; desktop",
	} {
		require.Contains(t, string(core), fragment, "core must preserve %q", fragment)
	}
	require.NotContains(t, string(core), "libdemo", "core must carry no platform-tagged entry")
	require.Equal(t,
		[]string{"configuration", "icons", "author_invented", "libraries"},
		sectionNamesOf(t, core),
		"the author's section order must survive")

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Equal(t, commentsOf(t, original), commentsOf(t, reassembled),
		"every comment must survive the round trip, in order")
	require.Equal(t, sectionNamesOf(t, original), sectionNamesOf(t, reassembled))
	require.Equal(t, sectionsOf(t, original), sectionsOf(t, reassembled))
}

// TestExtensionReassemblyOfOneSliceOmitsEveryOtherPlatform is the partial
// install: a project that needs only iOS must end up with a .gdextension naming
// only the iOS binaries that are actually on disk.
func TestExtensionReassemblyOfOneSliceOmitsEveryOtherPlatform(t *testing.T) {
	original, err := os.ReadFile("testdata/real/terrabrush.gdextension")
	require.NoError(t, err)

	core, removed, err := PartitionExtension(original, "res://addons/terrabrush", "terrabrush.gdextension")
	require.NoError(t, err)

	iosSlice := SliceID{Platform: "ios"}
	require.Contains(t, removed, iosSlice)

	reassembled, err := ReassembleExtension(core, removed, []SliceID{CoreSliceID(), iosSlice})
	require.NoError(t, err)

	libraries := pathsOf(t, reassembled)["libraries"]
	require.Equal(t, ExtensionEntryTable[string]{
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
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot, demoExtensionPath)
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

	libraries := pathsOf(t, first)["libraries"]
	require.Len(t, libraries, 2)
	require.True(t, strings.Index(string(first), "ios.template_release.arm64=") < strings.Index(string(first), "macos.debug="),
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
	core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Contains(t, removed, SliceID{Platform: "macos"})
	require.Contains(t, removed, SliceID{Platform: "macos", Architecture: "arm64"})

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Less(t,
		strings.Index(string(reassembled), "macos.arm64="),
		strings.Index(string(reassembled), "macos.template_debug="),
		"entries of one section are sorted by key even when their slices sort the other way")
}

func TestExtensionPartitionDoesNotMutateItsInput(t *testing.T) {
	original, err := os.ReadFile("testdata/real/terrabrush.gdextension")
	require.NoError(t, err)
	untouched := bytes.Clone(original)

	_, _, err = PartitionExtension(original, "res://addons/terrabrush", "terrabrush.gdextension")
	require.NoError(t, err)
	require.Equal(t, untouched, original, "partition must not write through its input buffer")
}

// TestExtensionNormalizesAByteOrderMarkAndCarriageReturns asserts the decided
// behavior for the two byte-level shapes an author's editor adds. Both are
// accepted, and both are normalized away in the emitted body: emission is
// canonical, and the two files this produces are generated by gpm — the core
// body goes into a slice archive, the reassembled file into the install tree —
// rather than being edits of the author's own working file. Content fidelity is
// what is required, and it is asserted here against the identical file written
// with a plain line feed and no mark.
func TestExtensionNormalizesAByteOrderMarkAndCarriageReturns(t *testing.T) {
	decorated, err := os.ReadFile("testdata/synthetic/crlf_bom.gdextension")
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(decorated, []byte("\ufeff")), "the fixture must carry a byte order mark")
	require.Contains(t, string(decorated), "\r\n", "the fixture must use CRLF endings")

	plain := bytes.ReplaceAll(bytes.TrimPrefix(decorated, []byte("\ufeff")), []byte("\r\n"), []byte("\n"))

	decoratedCore, decoratedRemoved, err := PartitionExtension(decorated, demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	plainCore, plainRemoved, err := PartitionExtension(plain, demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)

	require.Equal(t, string(plainCore), string(decoratedCore),
		"a byte order mark and CRLF endings must not change the emitted core body")
	require.Equal(t, plainRemoved, decoratedRemoved)
	require.NotContains(t, string(decoratedCore), "\ufeff")
	require.NotContains(t, string(decoratedCore), "\r")

	reassembled, err := ReassembleExtension(decoratedCore, decoratedRemoved, allSlices(decoratedRemoved))
	require.NoError(t, err)
	require.NotContains(t, string(reassembled), "\ufeff")
	require.NotContains(t, string(reassembled), "\r")
	require.Equal(t, sectionsOf(t, plain), sectionsOf(t, reassembled),
		"normalizing the bytes must not change the content")
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

	firstCore, firstRemoved, err := PartitionExtension([]byte(first), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	secondCore, secondRemoved, err := PartitionExtension([]byte(second), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)

	iosSlice := SliceID{Platform: "ios"}
	require.Equal(t, "res://addons/demo/bin/libfirst.ios.xcframework",
		firstRemoved[iosSlice].Libraries["ios.debug"])
	require.Equal(t, "res://addons/demo/bin/libsecond.ios.xcframework",
		secondRemoved[iosSlice].Libraries["ios.debug"])
	require.NotContains(t, firstRemoved, SliceID{Platform: "linux", Architecture: "x86_64"})

	firstReassembled, err := ReassembleExtension(firstCore, firstRemoved, allSlices(firstRemoved))
	require.NoError(t, err)
	require.Equal(t, ExtensionEntryTable[string]{"ios.debug": "res://addons/demo/bin/libfirst.ios.xcframework"},
		pathsOf(t, firstReassembled)["libraries"])
	require.Contains(t, string(firstReassembled), "first_init")

	secondReassembled, err := ReassembleExtension(secondCore, secondRemoved, allSlices(secondRemoved))
	require.NoError(t, err)
	require.Equal(t, sectionsOf(t, []byte(second)), sectionsOf(t, secondReassembled))
}

// TestExtensionEntriesAreAssignableToAnIndexSlice asserts the shape contract
// between partition and the index: a section's table is written straight into an
// IndexSlice with no conversion, and survives a save and a load.
func TestExtensionEntriesAreAssignableToAnIndexSlice(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)

	const extensionPath = "demo.gdextension"
	index := &Index{
		Format:  1,
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
		// ExtensionEntryTable[Value] is literally the element type of
		// ExtensionSectionTable[Value] for each section's own value type.
		if entries.Libraries != nil {
			indexSlice.Libraries = ExtensionSectionTable[string]{extensionPath: entries.Libraries}
		}
		if entries.Dependencies != nil {
			indexSlice.Dependencies = ExtensionSectionTable[ExtensionDependencyTargets]{
				extensionPath: entries.Dependencies,
			}
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
		entries := ExtensionEntries{
			Libraries:    indexSlice.Libraries[extensionPath],
			Dependencies: indexSlice.Dependencies[extensionPath],
		}
		if entries.Libraries == nil && entries.Dependencies == nil {
			continue
		}
		reloaded[id] = entries
	}
	require.Equal(t, removed, reloaded, "a partition result must survive the index unchanged")

	reassembled, err := ReassembleExtension(core, reloaded, allSlices(reloaded))
	require.NoError(t, err)
	require.Equal(t, sectionsOf(t, []byte(bothSectionsExtension)), sectionsOf(t, reassembled))
}

// TestExtensionPartitionAndReassemblyTouchNoFilesystem asserts the property the
// addonRoot parameter exists for: both functions run on in-memory bytes and a
// res:// root alone, with no temporary directory and nothing read from disk.
func TestExtensionPartitionAndReassemblyTouchNoFilesystem(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.NotEmpty(t, removed)

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Equal(t, sectionsOf(t, []byte(bothSectionsExtension)), sectionsOf(t, reassembled))
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

	core, removed, err := PartitionExtension([]byte(builder.String()), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)

	for _, section := range sections {
		require.NotContains(t, string(core), string(section)+".framework",
			"[%s] must be emptied in the core body", section)
		require.Contains(t, string(core), "["+string(section)+"]",
			"[%s] must keep its header in the core body", section)
	}
	// Each section's expectation is spelled in that section's own value type,
	// which is the point: a section whose leaf type is not handled cannot be
	// written here at all.
	require.Equal(t, map[SliceID]ExtensionEntries{{Platform: "macos"}: {
		Libraries: ExtensionEntryTable[string]{
			"macos.debug": fmt.Sprintf("res://addons/demo/bin/%s.framework", SectionLibraries),
		},
		Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
			"macos.debug": {fmt.Sprintf("res://addons/demo/bin/%s.framework", SectionDependencies): ""},
		},
	}}, removed)
	require.Len(t, sections, 2, "every partitioned section must have an expectation above")

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Equal(t, ExtensionEntryTable[string]{
		"macos.debug": fmt.Sprintf("res://addons/demo/bin/%s.framework", SectionLibraries),
	}, pathsOf(t, reassembled)[string(SectionLibraries)])
	require.Equal(t, map[string]ExtensionDependencyTargets{
		"macos.debug": {fmt.Sprintf("res://addons/demo/bin/%s.framework", SectionDependencies): ""},
	}, targetsOf(t, reassembled))
}

// TestExtensionPartitionIsFailClosedPerSection asserts every entry-level row of
// the issue's fail-closed contract, for each partitioned section, because the
// rules are identical in each and must stay identical.
func TestExtensionPartitionIsFailClosedPerSection(t *testing.T) {
	rows := []struct {
		name      string
		body      string
		addonRoot string
		// expectedMessage applies to every section the row runs in.
		expectedMessage []string
		// onlySection restricts a row to one section, for the two shapes the
		// sections no longer agree on: Godot accepts a Dictionary in
		// [dependencies] and not in [libraries], so the Dictionary rows pin a
		// [libraries] rejection only.
		onlySection ExtensionSection
		// messageBySection overrides expectedMessage where the sections reject
		// the same input with different wording, because the two readers name
		// different sets of accepted Variant kinds.
		messageBySection map[ExtensionSection][]string
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
			// A relative value is now accepted and resolved against the
			// .gdextension's own directory, so the row that stood here is replaced
			// by a value that is neither accepted form. The relative form's own
			// rules are asserted per section by
			// TestExtensionPartitionIsFailClosedOnRelativeValues.
			name:            "value is in another uri scheme",
			body:            `windows.release.x86_64 = "user://libdemo.dll"`,
			expectedMessage: []string{"user://libdemo.dll", "res://", "relative to the"},
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
			name:            "value is a StringName rather than a string",
			body:            `macos.debug = &"res://addons/demo/bin/libdemo.framework"`,
			expectedMessage: []string{"StringName", "plain string"},
		},
		{
			name:            "value is a NodePath rather than a string",
			body:            `macos.debug = ^"res://addons/demo/bin/libdemo.framework"`,
			expectedMessage: []string{"NodePath", "plain string"},
		},
		{
			name:            "value is a godot dictionary",
			body:            `ios.release = { "res://addons/demo/bin/libdemo.a": "" }`,
			onlySection:     SectionLibraries,
			expectedMessage: []string{"dictionary", "exactly one quoted"},
		},
		{
			name: "value is a dictionary written across several lines",
			body: `ios.release = {
	"res://addons/demo/bin/libdemo.a" : "",
	"res://addons/demo/bin/libother.a" : ""
}`,
			onlySection:     SectionLibraries,
			expectedMessage: []string{"dictionary", "ios.release"},
		},
		{
			name:            "value is not quoted",
			body:            `windows.release.x86_64 = 42`,
			expectedMessage: []string{"42", "number", "exactly one quoted"},
			messageBySection: map[ExtensionSection][]string{
				SectionDependencies: {"42", "number", "export destinations"},
			},
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
			if row.onlySection != "" && row.onlySection != section {
				continue
			}
			t.Run(string(section)+"/"+row.name, func(t *testing.T) {
				content := fmt.Sprintf("[configuration]\n\nentry_symbol = \"demo_init\"\n\n[%s]\n\n%s\n", section, row.body)
				addonRoot := row.addonRoot
				if addonRoot == "" {
					addonRoot = demoAddonRoot
				}
				expectedMessage := row.expectedMessage
				if override, found := row.messageBySection[section]; found {
					expectedMessage = override
				}

				core, removed, err := PartitionExtension([]byte(content), addonRoot, demoExtensionPath)
				requireManifestError(t, err, expectedMessage...)
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
			expectedMessage: []string{"not a valid Godot configuration file", "1:15", "expected ']'"},
		},
		{
			name:            "empty section name",
			content:         "[]\nentry_symbol = \"demo_init\"\n",
			expectedMessage: []string{"1:2", "section name must not be empty"},
		},
		{
			name:            "line that is not a statement",
			content:         "[configuration]\nentry_symbol\n",
			expectedMessage: []string{"2:13", "expected '='"},
		},
		{
			name:            "statement with no key",
			content:         "[configuration]\n= \"demo_init\"\n",
			expectedMessage: []string{"2:1", "expected configuration key"},
		},
		{
			name:            "value that is never closed",
			content:         "[libraries]\nmacos.debug = \"res://addons/demo/bin/libdemo.framework\n",
			expectedMessage: []string{"2:15", "unterminated string literal"},
		},
		{
			name:            "dictionary that is never closed",
			content:         "[dependencies]\nmacos.debug = {\n  \"res://addons/demo/bin/x.dylib\": \"\"\n",
			expectedMessage: []string{"4:1", "expected ',' or '}'"},
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
			core, removed, err := PartitionExtension([]byte(row.content), demoAddonRoot, demoExtensionPath)
			requireManifestError(t, err, row.expectedMessage...)
			require.Nil(t, core)
			require.Nil(t, removed)
		})
	}
}

func TestExtensionReassemblyIsFailClosed(t *testing.T) {
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot, demoExtensionPath)
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
			expectedMessage: []string{"not a valid Godot configuration file", "expected ']'"},
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
				macosSlice: {Libraries: ExtensionEntryTable[string]{
					"windows.release.x86_64": "res://addons/demo/bin/libdemo.dll",
				}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"windows.release.x86_64", "belongs to slice", "macos"},
		},
		{
			name: "an entry key is not a platform tag",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {Libraries: ExtensionEntryTable[string]{
					"libdemo": "res://addons/demo/bin/libdemo.dylib",
				}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"libdemo", "unknown platform"},
		},
		{
			name: "an entry value is not a resource path",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {Libraries: ExtensionEntryTable[string]{"macos.debug": "bin/libdemo.dylib"}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"bin/libdemo.dylib", "res://"},
		},
		{
			name: "an entry value would not survive being written back",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {Libraries: ExtensionEntryTable[string]{
					"macos.debug": `res://addons/demo/bin/lib"demo.dylib`,
				}},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice},
			expectedMessage: []string{"must not contain a quote"},
		},
		{
			name: "two slices declare the same key in one section",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {Libraries: ExtensionEntryTable[string]{
					"macos.debug": "res://addons/demo/bin/a.framework",
				}},
				{Platform: "macos", Architecture: "universal"}: {
					Libraries: ExtensionEntryTable[string]{"macos.debug": "res://addons/demo/bin/b.framework"},
				},
			},
			selected:        []SliceID{CoreSliceID(), macosSlice, {Platform: "macos", Architecture: "universal"}},
			expectedMessage: []string{"macos.debug", "both declare key"},
		},
		{
			// Two architecture slices of one platform may share a key, because
			// that is what the fan-out creates, but only when they agree about its
			// value: either slice may be selected alone, so a disagreement has no
			// authoritative side.
			name: "two architecture slices declare the same key with different values",
			core: core,
			entries: map[SliceID]ExtensionEntries{
				{Platform: "macos", Architecture: "arm64"}: {
					Libraries: ExtensionEntryTable[string]{"macos.debug": "res://addons/demo/bin/a.framework"},
				},
				{Platform: "macos", Architecture: "universal"}: {
					Libraries: ExtensionEntryTable[string]{"macos.debug": "res://addons/demo/bin/b.framework"},
				},
			},
			selected: []SliceID{
				CoreSliceID(),
				{Platform: "macos", Architecture: "arm64"},
				{Platform: "macos", Architecture: "universal"},
			},
			expectedMessage: []string{"macos.debug", "with different values"},
		},
		{
			name: "the core body declares no section for an entry",
			core: []byte("[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]\n"),
			entries: map[SliceID]ExtensionEntries{
				macosSlice: {Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
					"macos.debug": {"res://addons/demo/bin/support.dylib": ""},
				}},
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
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot, demoExtensionPath)
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
	core, removed, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot, demoExtensionPath)
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

// sectionsOf parses a .gdextension into section name -> key -> comparable value,
// which is the semantic content a round trip has to preserve.
//
// Every section but [dependencies] is compared by the Variant's spelling, so
// two documents comparing equal really carry the same value.
//
// [dependencies] is compared by meaning instead, because Godot accepts two
// Godot-equivalent spellings for one entry — a bare res:// path and a Dictionary
// mapping res:// paths to export destinations — and gpm normalizes the bare
// spelling into the single-entry Dictionary at the parser boundary so the index
// holds exactly one representation. Reassembly therefore always emits the
// Dictionary spelling, which is in contract with emission being canonical, and
// what the round trip must preserve is the dependency-path-to-destination
// mapping rather than the punctuation the author chose for it.
func sectionsOf(t *testing.T, content []byte) map[string]ExtensionEntryTable[string] {
	t.Helper()
	file := parseFixture(t, content)

	sections := map[string]ExtensionEntryTable[string]{}
	for _, section := range file.Sections {
		table := sections[section.Name]
		if table == nil {
			table = ExtensionEntryTable[string]{}
			sections[section.Name] = table
		}
		for _, statement := range section.Statements {
			assignment, isAssignment := statement.(*ast.Assignment)
			if !isAssignment {
				continue
			}
			if section.Name == string(SectionDependencies) {
				table[assignment.Key] = describeDependencyMeaning(t, assignment)
				continue
			}
			table[assignment.Key] = formatFixtureValue(assignment)
		}
	}
	return sections
}

// describeDependencyMeaning renders a [dependencies] value as its canonical
// dependency-path-to-destination mapping, with both spellings Godot accepts
// collapsing onto the same text.
func describeDependencyMeaning(t *testing.T, assignment *ast.Assignment) string {
	t.Helper()
	targets := dependencyTargetsOfFixtureValue(t, assignment.Value)
	rendered := make([]string, 0, len(targets))
	for _, path := range sortedKeys(targets) {
		rendered = append(rendered, fmt.Sprintf("%q -> %q", path, targets[path]))
	}
	return strings.Join(rendered, ", ")
}

// dependencyTargetsOfFixtureValue decodes either spelling of a [dependencies]
// value from a parsed fixture, independently of the production reader, so the
// round-trip assertion is not comparing the implementation against itself.
func dependencyTargetsOfFixtureValue(t *testing.T, expression ast.Expression) ExtensionDependencyTargets {
	t.Helper()
	targets := ExtensionDependencyTargets{}
	switch value := expression.(type) {
	case *ast.StringLiteral:
		targets[value.Value] = ""
	case *ast.DictionaryLiteral:
		for _, item := range value.Items {
			entry, isEntry := item.(*ast.DictionaryEntry)
			if !isEntry {
				continue
			}
			key, isString := entry.Key.(*ast.StringLiteral)
			require.True(t, isString, "fixture dependency key is not a string")
			destination, isString := entry.Value.(*ast.StringLiteral)
			require.True(t, isString, "fixture dependency destination is not a string")
			targets[key.Value] = destination.Value
		}
	default:
		t.Fatalf("fixture [dependencies] value is neither a string nor a dictionary: %T", expression)
	}
	return targets
}

// targetsOf parses a .gdextension's [dependencies] section into key ->
// dependency path -> export destination, which is the shape the index stores.
func targetsOf(t *testing.T, content []byte) map[string]ExtensionDependencyTargets {
	t.Helper()
	entries := map[string]ExtensionDependencyTargets{}
	for _, section := range parseFixture(t, content).Sections {
		if section.Name != string(SectionDependencies) {
			continue
		}
		for _, statement := range section.Statements {
			if assignment, isAssignment := statement.(*ast.Assignment); isAssignment {
				entries[assignment.Key] = dependencyTargetsOfFixtureValue(t, assignment.Value)
			}
		}
	}
	return entries
}

// pathsOf parses a .gdextension into section name -> key -> decoded string
// value, for the tests that compare against a res:// path rather than against a
// Variant's spelling. An entry holding another Variant type, such as
// [configuration]'s reloadable, is not a path and is left out.
func pathsOf(t *testing.T, content []byte) map[string]ExtensionEntryTable[string] {
	t.Helper()
	paths := map[string]ExtensionEntryTable[string]{}
	for _, section := range parseFixture(t, content).Sections {
		table := ExtensionEntryTable[string]{}
		for _, statement := range section.Statements {
			assignment, isAssignment := statement.(*ast.Assignment)
			if !isAssignment {
				continue
			}
			if literal, isString := assignment.Value.(*ast.StringLiteral); isString {
				table[assignment.Key] = literal.Value
			}
		}
		paths[section.Name] = table
	}
	return paths
}

// sectionNamesOf returns a document's section names in source order, so a test
// can assert the author's ordering survived.
func sectionNamesOf(t *testing.T, content []byte) []string {
	t.Helper()
	names := []string{}
	for _, section := range parseFixture(t, content).Sections {
		names = append(names, section.Name)
	}
	return names
}

// commentsOf returns every comment of a document in source order, section
// comments and preamble comments alike.
func commentsOf(t *testing.T, content []byte) []string {
	t.Helper()
	file := parseFixture(t, content)

	comments := []string{}
	collect := func(statements []ast.Statement) {
		for _, statement := range statements {
			if comment, isComment := statement.(*ast.Comment); isComment {
				comments = append(comments, comment.Text)
			}
		}
	}
	collect(file.Preamble)
	for _, section := range file.Sections {
		collect(section.Statements)
	}
	return comments
}

func parseFixture(t *testing.T, content []byte) *ast.File {
	t.Helper()
	file, err := configfile.Parse(bytes.TrimPrefix(content, []byte("\ufeff")))
	require.NoError(t, err)
	return file
}

// formatFixtureValue renders an assignment's value through the parser's own
// formatter, so two documents comparing equal really carry the same value.
func formatFixtureValue(assignment *ast.Assignment) string {
	var file ast.File
	file.Sections = []*ast.Section{{Name: "value", Statements: []ast.Statement{assignment}}}
	return strings.TrimSuffix(strings.TrimPrefix(configfile.Format(&file), "[value]\n"+assignment.Key+"="), "\n")
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

// TestExtensionPartitionAcceptsAFileWithNoFinalNewline covers the edge of a file
// whose last line has no ending. Canonical emission always terminates the last
// line, and no content is lost either way.
func TestExtensionPartitionAcceptsAFileWithNoFinalNewline(t *testing.T) {
	t.Run("nothing to partition", func(t *testing.T) {
		const content = "[configuration]\n\nentry_symbol = \"demo_init\"\n\n[icons]\n\nDemoNode = \"res://addons/demo/demo_node.svg\""
		core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
		require.NoError(t, err)
		require.Empty(t, removed)
		require.Equal(t, sectionsOf(t, []byte(content)), sectionsOf(t, core))
		require.True(t, strings.HasSuffix(string(core), "\n"), "the emitted body always terminates its last line")

		reassembled, err := ReassembleExtension(core, removed, []SliceID{CoreSliceID()})
		require.NoError(t, err)
		require.Equal(t, string(core), string(reassembled))
	})

	t.Run("partitioned section ends the file", func(t *testing.T) {
		const content = "[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]\n\nmacos.debug = \"res://addons/demo/bin/libdemo.framework\""
		core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
		require.NoError(t, err)
		require.Equal(t, "[configuration]\nentry_symbol=\"demo_init\"\n\n[libraries]\n", string(core))

		reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
		require.NoError(t, err)
		require.Equal(t, sectionsOf(t, []byte(content)), sectionsOf(t, reassembled))

		secondCore, secondRemoved, err := PartitionExtension(reassembled, demoAddonRoot, demoExtensionPath)
		require.NoError(t, err)
		require.Equal(t, string(core), string(secondCore))
		require.Equal(t, removed, secondRemoved)
	})

	t.Run("partitioned header ends the file", func(t *testing.T) {
		const content = "[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]"
		core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
		require.NoError(t, err)
		require.Empty(t, removed)
		require.Equal(t, "[configuration]\nentry_symbol=\"demo_init\"\n\n[libraries]\n", string(core))
	})
}

// TestExtensionPartitionDoesNotReadCommentsAsValueSyntax pins a comment's text as
// text. An opening delimiter or an unpaired quote inside a trailing comment used
// to be read as the start of a multi-line value, which swallowed the section
// headers that followed it: platform-tagged entries then either survived into the
// core body unrecognized, where they would name binaries no slice installs, or
// disappeared from both the core body and the partitioned entries.
func TestExtensionPartitionDoesNotReadCommentsAsValueSyntax(t *testing.T) {
	rows := []struct {
		name    string
		comment string
	}{
		{"unbalanced brace", "; TODO: see {upstream"},
		{"unbalanced parenthesis", "; note (see upstream"},
		{"unbalanced bracket", "; note [see upstream"},
		{"unpaired quote", `; see "upstream`},
		{"hash introducer", "# note (see upstream"},
	}

	for _, row := range rows {
		t.Run("before a partitioned section/"+row.name, func(t *testing.T) {
			content := fmt.Sprintf(
				"[configuration]\n\nentry_symbol = \"demo_init\" %s\n\n[libraries]\n\nmacos.debug = \"res://addons/demo/bin/a.framework\"\n",
				row.comment,
			)
			core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
			require.NoError(t, err)
			require.Equal(t, map[SliceID]ExtensionEntries{
				{Platform: "macos"}: {Libraries: ExtensionEntryTable[string]{
					"macos.debug": "res://addons/demo/bin/a.framework",
				}},
			}, removed)
			require.NotContains(t, string(core), "a.framework",
				"a comment must not hide a platform-tagged entry in the core body")
			require.Contains(t, string(core), row.comment, "the comment itself is preserved verbatim")
		})

		t.Run("inside a partitioned section/"+row.name, func(t *testing.T) {
			content := fmt.Sprintf(
				"[libraries]\n\nmacos.debug = \"res://addons/demo/bin/a.framework\" %s\n\n[dependencies]\n\nmacos.debug = \"res://addons/demo/bin/b.dylib\"\n",
				row.comment,
			)
			_, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
			require.NoError(t, err)
			require.Equal(t, map[SliceID]ExtensionEntries{
				{Platform: "macos"}: {
					Libraries: ExtensionEntryTable[string]{
						"macos.debug": "res://addons/demo/bin/a.framework",
					},
					Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
						"macos.debug": {"res://addons/demo/bin/b.dylib": ""},
					},
				},
			}, removed, "a comment must not swallow the section that follows it")
		})
	}
}

// TestExtensionPartitionAcceptsATrailingCommentOnAnEntry asserts the comment is
// dropped from the value rather than becoming part of the path.
func TestExtensionPartitionAcceptsATrailingCommentOnAnEntry(t *testing.T) {
	const content = `[libraries]

macos.debug = "res://addons/demo/bin/a.framework" ; the debug build
ios.debug = "res://addons/demo/bin/b.xcframework" # the debug build
`
	_, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, map[SliceID]ExtensionEntries{
		{Platform: "macos"}: {Libraries: ExtensionEntryTable[string]{
			"macos.debug": "res://addons/demo/bin/a.framework",
		}},
		{Platform: "ios"}: {Libraries: ExtensionEntryTable[string]{
			"ios.debug": "res://addons/demo/bin/b.xcframework",
		}},
	}, removed)
}

// TestExtensionPartitionKeepsCommentsInsideAPartitionedSection asserts a comment
// in [libraries] or [dependencies] is content rather than an entry: it stays in
// the core body, so no comment is lost, and it does not come back as an entry.
func TestExtensionPartitionKeepsCommentsInsideAPartitionedSection(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[libraries]

; desktop builds
macos.debug = "res://addons/demo/bin/libdemo.macos.framework"
# windows.debug.arm64 is not built yet
linux.release.x86_64 = "res://addons/demo/bin/libdemo.linux.so"

[dependencies]

; nothing is shipped beside the libraries yet
`
	core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, commentsOf(t, []byte(content)), commentsOf(t, core),
		"every comment of a partitioned section stays in the core body")
	require.Empty(t, pathsOf(t, core)["libraries"], "no entry stays in the core body")
	require.Len(t, removed, 2)

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Equal(t, commentsOf(t, []byte(content)), commentsOf(t, reassembled))
	require.Equal(t, sectionsOf(t, []byte(content)), sectionsOf(t, reassembled))

	secondCore, secondRemoved, err := PartitionExtension(reassembled, demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, string(core), string(secondCore))
	require.Equal(t, removed, secondRemoved)
}

// TestExtensionIsTheOnlyGodotConfigParser asserts the single-source-of-truth rule
// the issue states: the Godot config parser is reached from exactly one place in
// the repository, so no second parser of .gdextension content can appear.
func TestExtensionIsTheOnlyGodotConfigParser(t *testing.T) {
	root := filepath.Join("..", "..")
	callers := map[string]int{}
	require.NoError(t, filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "website" || entry.Name() == ".worktrees" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(content), "gdparser/configfile") {
			callers[filepath.ToSlash(path)]++
		}
		return nil
	}))
	require.Equal(t, []string{"../../internal/slice/gdextension.go"}, sortedCallerPaths(callers),
		"only internal/slice may reach the Godot config parser")
}

func sortedCallerPaths(callers map[string]int) []string {
	paths := make([]string, 0, len(callers))
	for path := range callers {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// TestExtensionReassemblyWritesEntriesAfterASectionsComments pins where a
// partitioned section's comments end up, so the placement is a contract rather
// than an accident.
//
// A comment's position relative to the entries cannot be preserved: the entries
// are re-emitted sorted by key, which is what makes the output deterministic, so
// none of them is at its original index. The comments are therefore kept as one
// block in their own original order and the entries follow them. Nothing is
// dropped, and a comment never turns into an entry.
func TestExtensionReassemblyWritesEntriesAfterASectionsComments(t *testing.T) {
	const content = `[libraries]

; desktop
windows.release.x86_64 = "res://addons/demo/bin/libdemo.dll"
; mobile
android.release.arm64 = "res://addons/demo/bin/libdemo.android.so"
; nothing else is built yet
`
	core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)

	require.Equal(t, "[libraries]\n"+
		"; desktop\n"+
		"; mobile\n"+
		"; nothing else is built yet\n"+
		"android.release.arm64=\"res://addons/demo/bin/libdemo.android.so\"\n"+
		"windows.release.x86_64=\"res://addons/demo/bin/libdemo.dll\"\n", string(reassembled))
	require.Equal(t, commentsOf(t, []byte(content)), commentsOf(t, reassembled),
		"every comment survives, in its own original order")
	require.Equal(t, sectionsOf(t, []byte(content)), sectionsOf(t, reassembled),
		"every entry survives with its value unchanged")
}

// TestExtensionPartitionGroupsEntriesByExactlyTheirReducedSliceID pins the
// grouping contract for a platform whose entries carry an architecture only
// sometimes, which is the shape real addons ship: godot_jolt writes macos.editor
// beside macos.template_release.universal, nobodywho writes macos.debug beside
// macos.debug.arm64.
//
// Each entry is reported under exactly the slice ID its key reduces to. Note that
// the host candidate chain in host.go installs the FIRST published slice it
// matches rather than every matching one, so a platform published across both a
// generic and an architecture-specific slice needs the packager to decide how it
// is finally published — the index schema accepts an architecture-less key inside
// an architecture-specific slice precisely so it can fan one out. That decision
// needs the whole addon's published slice set, so it is not taken here.
func TestExtensionPartitionGroupsEntriesByExactlyTheirReducedSliceID(t *testing.T) {
	const content = `[libraries]

macos.editor = "res://addons/demo/bin/libdemo.macos.editor.framework"
macos.template_debug = "res://addons/demo/bin/libdemo.macos.debug.framework"
macos.template_release.universal = "res://addons/demo/bin/libdemo.macos.universal.framework"
`
	_, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, map[SliceID]ExtensionEntries{
		{Platform: "macos"}: {Libraries: ExtensionEntryTable[string]{
			"macos.editor":         "res://addons/demo/bin/libdemo.macos.editor.framework",
			"macos.template_debug": "res://addons/demo/bin/libdemo.macos.debug.framework",
		}},
		{Platform: "macos", Architecture: "universal"}: {Libraries: ExtensionEntryTable[string]{
			"macos.template_release.universal": "res://addons/demo/bin/libdemo.macos.universal.framework",
		}},
	}, removed)

	// The generic and the architecture-specific slice are distinct members of the
	// published set, which is what makes the packager's decision necessary.
	require.Len(t, removed, 2)
}

// TestExtensionPartitionCarriesDictionaryDependencyDestinations partitions the
// checked-in limboai fixture, whose [dependencies] values are Godot
// Dictionaries mapping each dependency to the export subdirectory Godot copies
// it into. It replaces the subtest that asserted this real producer's file was
// rejected: the index now carries the destinations, so the file is accepted.
func TestExtensionPartitionCarriesDictionaryDependencyDestinations(t *testing.T) {
	content, err := os.ReadFile("testdata/limboai.gdextension")
	require.NoError(t, err)

	core, removed, err := PartitionExtension(content, "res://addons/limboai", "limboai.gdextension")
	require.NoError(t, err)
	require.NotContains(t, string(core), "libgodot-cpp", "the core body must carry no dependency entry")

	iosSlice := SliceID{Platform: "ios"}
	require.Contains(t, removed, iosSlice)
	require.Equal(t,
		ExtensionDependencyTargets{"res://addons/limboai/bin/libgodot-cpp.ios.template_debug.a": ""},
		removed[iosSlice].Dependencies["ios.debug"],
	)
	require.Equal(t,
		ExtensionDependencyTargets{"res://addons/limboai/bin/libgodot-cpp.ios.template_release.a": ""},
		removed[iosSlice].Dependencies["ios.release"],
	)
	require.Equal(t,
		"res://addons/limboai/bin/liblimboai.ios.template_debug.xcframework",
		removed[iosSlice].Libraries["ios.debug"],
	)
}

// TestExtensionPartitionIsFailClosedOnDependencyDictionaries asserts every row
// of the fail-closed contract that is specific to a [dependencies] value, which
// is the one section whose value is a Godot Dictionary rather than a single
// res:// path. The rows shared with [libraries] are asserted for both sections
// by TestExtensionPartitionIsFailClosedPerSection.
func TestExtensionPartitionIsFailClosedOnDependencyDictionaries(t *testing.T) {
	for _, row := range []struct {
		name            string
		body            string
		expectedMessage []string
	}{
		{
			name:            "dictionary is empty",
			body:            `ios.release = {}`,
			expectedMessage: []string{"ios.release", "names no dependency"},
		},
		{
			name:            "dictionary key is not a string",
			body:            `ios.release = { 42 : "" }`,
			expectedMessage: []string{"dependency path", "number"},
		},
		{
			name:            "dictionary key is a StringName",
			body:            `ios.release = { &"res://addons/demo/bin/libsupport.a" : "" }`,
			expectedMessage: []string{"dependency path", "StringName"},
		},
		{
			name:            "dictionary key is in another uri scheme",
			body:            `ios.release = { "user://libsupport.a" : "" }`,
			expectedMessage: []string{"user://libsupport.a", "res://", "relative to the"},
		},
		{
			name:            "dictionary key traverses out of the addon",
			body:            `ios.release = { "res://addons/demo/../other/libsupport.a" : "" }`,
			expectedMessage: []string{"must not escape the addon root"},
		},
		{
			name:            "dictionary key is outside the addon root",
			body:            `ios.release = { "res://addons/other/bin/libsupport.a" : "" }`,
			expectedMessage: []string{"outside the addon root", demoAddonRoot},
		},
		{
			name:            "dictionary key is not in its simplest form",
			body:            `ios.release = { "res://addons/demo/./bin/libsupport.a" : "" }`,
			expectedMessage: []string{"simplest form"},
		},
		{
			name:            "dictionary key uses windows separators",
			body:            `ios.release = { "res://addons/demo\\bin\\libsupport.a" : "" }`,
			expectedMessage: []string{"separators"},
		},
		{
			name:            "dictionary destination is not a string",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : 42 }`,
			expectedMessage: []string{"destination of dependency", "number"},
		},
		{
			name:            "dictionary destination is a NodePath",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : ^"Frameworks" }`,
			expectedMessage: []string{"destination of dependency", "NodePath"},
		},
		{
			name:            "dictionary destination is absolute",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : "/usr/local/lib" }`,
			expectedMessage: []string{"destination", "must be relative"},
		},
		{
			name:            "dictionary destination traverses",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : "../elsewhere" }`,
			expectedMessage: []string{"destination", "must not escape"},
		},
		{
			name:            "dictionary destination is not in its simplest form",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : "./Frameworks" }`,
			expectedMessage: []string{"destination", "simplest form"},
		},
		{
			name:            "dictionary destination uses windows separators",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : "libs\\arm64" }`,
			expectedMessage: []string{"destination", "separators"},
		},
		{
			name:            "dictionary destination names a host drive",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : "C:/libs" }`,
			expectedMessage: []string{"destination", "must be relative"},
		},
		{
			name:            "dictionary destination is a resource path",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : "res://addons/demo/libs" }`,
			expectedMessage: []string{"destination", "relative to the export directory"},
		},
		{
			name:            "dictionary destination contains a control character",
			body:            "ios.release = { \"res://addons/demo/bin/libsupport.a\" : \"libs\\u0001\" }",
			expectedMessage: []string{"destination", "control character"},
		},
		{
			name:            "duplicate dictionary key",
			body:            `ios.release = { "res://addons/demo/bin/libsupport.a" : "", "res://addons/demo/bin/libsupport.a" : "Frameworks" }`,
			expectedMessage: []string{"libsupport.a", "more than once"},
		},
		{
			name:            "value is an array",
			body:            `ios.release = [ "res://addons/demo/bin/libsupport.a" ]`,
			expectedMessage: []string{"array", "export destinations"},
		},
		{
			name:            "value is a boolean",
			body:            `ios.release = true`,
			expectedMessage: []string{"boolean", "export destinations"},
		},
		{
			name:            "value is null",
			body:            `ios.release = null`,
			expectedMessage: []string{"null", "export destinations"},
		},
		{
			name:            "value is a bare identifier",
			body:            `ios.release = libsupport`,
			expectedMessage: []string{"bare identifier", "export destinations"},
		},
		{
			name:            "value is a constructor call",
			body:            `ios.release = Vector2(1, 2)`,
			expectedMessage: []string{"constructor call", "export destinations"},
		},
		{
			name:            "typed dictionary declares a non-string key type",
			body:            `ios.release = Dictionary[int, String]({ 1 : "" })`,
			expectedMessage: []string{"int", "export destinations"},
		},
		{
			name:            "typed dictionary declares a non-string value type",
			body:            `ios.release = Dictionary[String, int]({ "res://addons/demo/bin/libsupport.a" : 1 })`,
			expectedMessage: []string{"int", "export destinations"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			content := fmt.Sprintf(
				"[configuration]\n\nentry_symbol = \"demo_init\"\n\n[%s]\n\n%s\n", SectionDependencies, row.body,
			)
			core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
			requireManifestError(t, err, row.expectedMessage...)
			require.Nil(t, core, "a rejected .gdextension must yield no core body")
			require.Nil(t, removed, "a rejected .gdextension must yield no entries")
		})
	}
}

// TestExtensionPartitionNormalizesABareStringDependency pins the one deliberate
// behavior change of the nested schema. Godot accepts both a bare res:// path and
// a Dictionary as a [dependencies] value, and both stay accepted, but they
// converge at the parser boundary into the single-entry Dictionary with an empty
// destination that is the bare string's Godot equivalent. That keeps exactly one
// representation in the Go type and in the index, with no "which spelling was it"
// flag, and its consequence is that reassembly always emits the Dictionary
// spelling.
//
// The first emission is therefore not byte-identical to a bare-string input, by
// design: emission is already canonical, which is why a BOM and CRLF endings are
// normalized away rather than reproduced. From the second emission onward it is
// byte-stable, because the normalization is total and one-way.
func TestExtensionPartitionNormalizesABareStringDependency(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[dependencies]

macos.debug = "res://addons/demo/bin/libsupport.dylib"
`
	core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, ExtensionEntryTable[ExtensionDependencyTargets]{
		"macos.debug": {"res://addons/demo/bin/libsupport.dylib": ""},
	}, removed[SliceID{Platform: "macos"}].Dependencies)

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Equal(t,
		"[configuration]\n"+
			"entry_symbol=\"demo_init\"\n"+
			"\n"+
			"[dependencies]\n"+
			"macos.debug={\"res://addons/demo/bin/libsupport.dylib\": \"\"}\n",
		string(reassembled),
		"reassembly emits the dictionary spelling, which is the single representation")

	// Byte-stable from the second emission onward.
	secondCore, secondRemoved, err := PartitionExtension(reassembled, demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, string(core), string(secondCore))
	require.Equal(t, removed, secondRemoved)
	again, err := ReassembleExtension(secondCore, secondRemoved, allSlices(secondRemoved))
	require.NoError(t, err)
	require.Equal(t, string(reassembled), string(again))
}

// TestExtensionPartitionAcceptsATypedDictionaryDependency covers Godot's
// Dictionary[String, String]({...}) serialization of the same value.
func TestExtensionPartitionAcceptsATypedDictionaryDependency(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[dependencies]

macos.debug = Dictionary[String, String]({ "res://addons/demo/bin/libsupport.dylib" : "Frameworks" })
`
	_, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, ExtensionEntryTable[ExtensionDependencyTargets]{
		"macos.debug": {"res://addons/demo/bin/libsupport.dylib": "Frameworks"},
	}, removed[SliceID{Platform: "macos"}].Dependencies)
}

// TestExtensionPartitionAcceptsADependencyNamingSeveralFiles covers an entry that
// ships more than one file beside one platform's library, each into its own
// export subdirectory, and pins that the emitted dictionary sorts its dependency
// paths so two machines write identical bytes.
func TestExtensionPartitionAcceptsADependencyNamingSeveralFiles(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[dependencies]

macos.debug = { "res://addons/demo/bin/libz.dylib" : "libs", "res://addons/demo/bin/liba.dylib" : "" }
`
	core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, ExtensionDependencyTargets{
		"res://addons/demo/bin/liba.dylib": "",
		"res://addons/demo/bin/libz.dylib": "libs",
	}, removed[SliceID{Platform: "macos"}].Dependencies["macos.debug"])

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	require.Contains(t, string(reassembled),
		`macos.debug={"res://addons/demo/bin/liba.dylib": "", "res://addons/demo/bin/libz.dylib": "libs"}`,
		"a dependency entry's paths are emitted in ascending order")
}

// TestExtensionPartitionDropsACommentInsideADependencyDictionary pins the one
// place a comment is not preserved, and pins it for both spellings the parser
// produces: a comment standing among a Dictionary's items, and a comment between
// an entry's key and its value, which the parser attaches to the entry as an
// infix comment.
//
// Both are accepted and dropped. The index stores a dependency entry as its
// paths and destinations and carries no comment at that depth, and the enclosing
// assignment leaves the core body, so there is nowhere for such a comment to be
// kept and nothing to restore it from on reassembly. This is the decided
// behavior rather than an oversight: a partitioned section's own comments are
// what the format actually carries, and they keep their separate treatment in
// the core body, asserted by
// TestExtensionPartitionKeepsCommentsInsideAPartitionedSection.
func TestExtensionPartitionDropsACommentInsideADependencyDictionary(t *testing.T) {
	for _, row := range []struct {
		name    string
		content string
	}{
		{
			name: "a comment standing among the items",
			content: `[dependencies]

macos.debug = {
	# the support library
	"res://addons/demo/bin/libsupport.dylib" : ""
}
`,
		},
		{
			name: "a comment between an entry's key and its value",
			content: `[dependencies]

macos.debug = {
	"res://addons/demo/bin/libsupport.dylib" :
		# copied beside the exported binary
		""
}
`,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			core, removed, err := PartitionExtension([]byte(row.content), demoAddonRoot, demoExtensionPath)
			require.NoError(t, err)
			require.Equal(t, ExtensionDependencyTargets{"res://addons/demo/bin/libsupport.dylib": ""},
				removed[SliceID{Platform: "macos"}].Dependencies["macos.debug"])
			require.Empty(t, commentsOf(t, core),
				"the dictionary's own comment is dropped rather than left in the core body")

			// The drop is total and one-way, so the round trip is still stable
			// from the first emission of the partitioned form onward.
			reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
			require.NoError(t, err)
			secondCore, secondRemoved, err := PartitionExtension(reassembled, demoAddonRoot, demoExtensionPath)
			require.NoError(t, err)
			require.Equal(t, string(core), string(secondCore))
			require.Equal(t, removed, secondRemoved)
		})
	}
}

// TestExtensionReassemblyIsFailClosedOnDependencyTargets asserts the reassembly
// rows specific to a dependency Dictionary. LoadIndex already rejects each of
// these, and reassembly re-checks because an Index may also be assembled in
// memory by a producer rather than loaded from bytes.
func TestExtensionReassemblyIsFailClosedOnDependencyTargets(t *testing.T) {
	core, _, err := PartitionExtension([]byte(bothSectionsExtension), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	macosSlice := SliceID{Platform: "macos"}

	for _, row := range []struct {
		name            string
		targets         ExtensionDependencyTargets
		expectedMessage []string
	}{
		{
			name:            "the dictionary is empty",
			targets:         ExtensionDependencyTargets{},
			expectedMessage: []string{"macos.debug", "empty"},
		},
		{
			name:            "a dependency path is not a res:// path",
			targets:         ExtensionDependencyTargets{"bin/support.dylib": ""},
			expectedMessage: []string{"bin/support.dylib", "res://"},
		},
		{
			name:            "a dependency path traverses",
			targets:         ExtensionDependencyTargets{"res://addons/demo/../other/support.dylib": ""},
			expectedMessage: []string{"must not escape"},
		},
		{
			name:            "a dependency path would not survive being written back",
			targets:         ExtensionDependencyTargets{`res://addons/demo/bin/lib"support.dylib`: ""},
			expectedMessage: []string{"must not contain a quote"},
		},
		{
			name:            "a destination traverses",
			targets:         ExtensionDependencyTargets{"res://addons/demo/bin/support.dylib": "../elsewhere"},
			expectedMessage: []string{"destination", "must not escape"},
		},
		{
			name:            "a destination is a res:// path",
			targets:         ExtensionDependencyTargets{"res://addons/demo/bin/support.dylib": "res://addons/demo/libs"},
			expectedMessage: []string{"destination", "relative to the export directory"},
		},
		{
			name:            "a destination would not survive being written back",
			targets:         ExtensionDependencyTargets{"res://addons/demo/bin/support.dylib": `libs"arm64`},
			expectedMessage: []string{"destination", "must not contain a quote"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			entries := map[SliceID]ExtensionEntries{
				macosSlice: {Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
					"macos.debug": row.targets,
				}},
			}
			reassembled, err := ReassembleExtension(core, entries, []SliceID{CoreSliceID(), macosSlice})
			require.Nil(t, reassembled, "a rejected reassembly must yield no file")
			require.Error(t, err)
			var fetchError *output.FetchError
			require.True(t, errors.As(err, &fetchError),
				"a reassembly failure is remote content being wrong, so it is a FetchError: got %T", err)
			require.Equal(t, output.ExitFetch, output.CodeFor(err))
			require.Contains(t, err.Error(), string(SectionDependencies))
			for _, fragment := range row.expectedMessage {
				require.Contains(t, err.Error(), fragment)
			}
		})
	}
}

// TestExtensionDictionaryDependenciesSurviveTheWholeIndexPath is the end-to-end
// assertion the nested schema exists for: a real producer's .gdextension with
// Godot Dictionary [dependencies] values goes through partition, straight into an
// IndexSlice with no conversion, through Save and LoadIndex, and back out of
// ReassembleExtension still naming every dependency and every export
// destination.
func TestExtensionDictionaryDependenciesSurviveTheWholeIndexPath(t *testing.T) {
	original, err := os.ReadFile("testdata/limboai.gdextension")
	require.NoError(t, err)

	const addonRoot = "res://addons/limboai"
	const extensionPath = "limboai.gdextension"
	core, removed, err := PartitionExtension(original, addonRoot, extensionPath)
	require.NoError(t, err)

	index := &Index{
		Format:  1,
		Name:    "limboai",
		Version: "1.4.0",
		Slices: map[string]*IndexSlice{
			CorePlatform: {File: "limboai-core.zip", SHA256: strings.Repeat("a", 64), Size: 1},
		},
	}
	for id, entries := range removed {
		indexSlice := &IndexSlice{
			File:   "limboai-" + strings.ReplaceAll(id.String(), ".", "-") + ".zip",
			SHA256: strings.Repeat("b", 64),
			Size:   2,
		}
		if entries.Libraries != nil {
			indexSlice.Libraries = ExtensionSectionTable[string]{extensionPath: entries.Libraries}
		}
		if entries.Dependencies != nil {
			indexSlice.Dependencies = ExtensionSectionTable[ExtensionDependencyTargets]{
				extensionPath: entries.Dependencies,
			}
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
		entries := ExtensionEntries{
			Libraries:    indexSlice.Libraries[extensionPath],
			Dependencies: indexSlice.Dependencies[extensionPath],
		}
		if entries.Libraries == nil && entries.Dependencies == nil {
			continue
		}
		reloaded[id] = entries
	}
	require.Equal(t, removed, reloaded, "a partition result must survive the index unchanged")

	reassembled, err := ReassembleExtension(core, reloaded, allSlices(reloaded))
	require.NoError(t, err)
	require.Equal(t, targetsOf(t, original), targetsOf(t, reassembled),
		"every dependency and every export destination must survive the whole path")
	require.Equal(t, sectionsOf(t, original), sectionsOf(t, reassembled))
}

// TestExtensionReassemblyOfOneSliceKeepsItsDependencyDestinations is the partial
// install over a real addon that ships dependencies: an iOS-only project must end
// up with a .gdextension naming exactly the iOS dependencies and nothing else.
func TestExtensionReassemblyOfOneSliceKeepsItsDependencyDestinations(t *testing.T) {
	original, err := os.ReadFile("testdata/limboai.gdextension")
	require.NoError(t, err)

	core, removed, err := PartitionExtension(original, "res://addons/limboai", "limboai.gdextension")
	require.NoError(t, err)

	iosSlice := SliceID{Platform: "ios"}
	reassembled, err := ReassembleExtension(core, removed, []SliceID{CoreSliceID(), iosSlice})
	require.NoError(t, err)
	require.Equal(t, map[string]ExtensionDependencyTargets{
		"ios.debug":   {"res://addons/limboai/bin/libgodot-cpp.ios.template_debug.a": ""},
		"ios.release": {"res://addons/limboai/bin/libgodot-cpp.ios.template_release.a": ""},
	}, targetsOf(t, reassembled))
	require.NotContains(t, string(reassembled), "android")
}

// TestExtensionPartitionsGodotJoltsRelativeLibraryPaths is the real-world
// evidence for relative entry values: every one of godot_jolt's fourteen
// [libraries] values is written relative to the .gdextension rather than as a
// res:// path, which is the form Godot documents beside res:// and the only form
// this widely used addon ships.
func TestExtensionPartitionsGodotJoltsRelativeLibraryPaths(t *testing.T) {
	content, err := os.ReadFile("testdata/godot_jolt.gdextension")
	require.NoError(t, err)

	const addonRoot = "res://addons/godot_jolt"
	core, removed, err := PartitionExtension(content, addonRoot, "godot_jolt.gdextension")
	require.NoError(t, err)
	require.NotContains(t, string(core), "godot-jolt_", "every [libraries] entry must leave the core body")

	// Spelled out in full rather than generated, so the slice each key reduces to
	// and the res:// path each relative value resolves to are both pinned. The
	// three Windows keys naming one file, and macos.editor sitting beside
	// macos.template_release.universal, are the shapes this fixture exists for.
	require.Equal(t, map[SliceID]ExtensionEntries{
		{Platform: "windows", Architecture: "x86_64"}: {Libraries: ExtensionEntryTable[string]{
			"windows.editor.x86_64":           addonRoot + "/bin/godot-jolt_windows_x64.dll",
			"windows.template_debug.x86_64":   addonRoot + "/bin/godot-jolt_windows_x64.dll",
			"windows.template_release.x86_64": addonRoot + "/bin/godot-jolt_windows_x64.dll",
		}},
		{Platform: "macos"}: {Libraries: ExtensionEntryTable[string]{
			"macos.editor":           addonRoot + "/bin/godot-jolt_macos.framework",
			"macos.template_debug":   addonRoot + "/bin/godot-jolt_macos.framework",
			"macos.template_release": addonRoot + "/bin/godot-jolt_macos.framework",
		}},
		{Platform: "macos", Architecture: "universal"}: {Libraries: ExtensionEntryTable[string]{
			"macos.template_release.universal": addonRoot + "/bin/godot-jolt_macos_universal.framework",
		}},
		{Platform: "linux", Architecture: "x86_64"}: {Libraries: ExtensionEntryTable[string]{
			"linux.editor.x86_64":           addonRoot + "/bin/godot-jolt_linux_x64.so",
			"linux.template_debug.x86_64":   addonRoot + "/bin/godot-jolt_linux_x64.so",
			"linux.template_release.x86_64": addonRoot + "/bin/godot-jolt_linux_x64.so",
		}},
		{Platform: "android", Architecture: "arm64"}: {Libraries: ExtensionEntryTable[string]{
			"android.template_debug.arm64":   addonRoot + "/bin/godot-jolt_android_arm64.so",
			"android.template_release.arm64": addonRoot + "/bin/godot-jolt_android_arm64.so",
		}},
		{Platform: "ios", Architecture: "arm64"}: {Libraries: ExtensionEntryTable[string]{
			"ios.template_debug.arm64":   addonRoot + "/bin/godot-jolt_ios_arm64.dylib",
			"ios.template_release.arm64": addonRoot + "/bin/godot-jolt_ios_arm64.dylib",
		}},
	}, removed)

	entryCount := 0
	for _, entries := range removed {
		entryCount += len(entries.Libraries)
	}
	require.Equal(t, 14, entryCount, "every one of the fixture's fourteen entries must be partitioned")
}

// TestExtensionRelativeValuesRoundTripAsCanonicalResourcePaths states the one
// deliberate difference between a relative-valued file and a res://-valued one:
// the round trip is semantic rather than byte-identical, because res:// is the
// single published form. Every section, key, comment, and ordering survives, and
// each value is the res:// resolution of what the author wrote.
//
// The fixpoint still holds from the first emission onward, because a res:// value
// resolves to itself, which is what keeps a repeat install from dirtying the
// working tree. That is why this fixture gets its own test rather than joining
// TestExtensionRoundTripReproducesEveryFixture, whose stronger
// parsed-content-identity claim keeps holding unweakened for res:// fixtures.
func TestExtensionRelativeValuesRoundTripAsCanonicalResourcePaths(t *testing.T) {
	for _, row := range []struct {
		path          string
		addonRoot     string
		extensionPath string
	}{
		{
			path:          "testdata/godot_jolt.gdextension",
			addonRoot:     "res://addons/godot_jolt",
			extensionPath: "godot_jolt.gdextension",
		},
		{
			path:          "testdata/synthetic/relative_subdirectory.gdextension",
			addonRoot:     demoAddonRoot,
			extensionPath: "extensions/demo.gdextension",
		},
		{
			path:          "testdata/synthetic/mixed_values.gdextension",
			addonRoot:     demoAddonRoot,
			extensionPath: demoExtensionPath,
		},
	} {
		t.Run(filepath.Base(row.path), func(t *testing.T) {
			original, err := os.ReadFile(row.path)
			require.NoError(t, err)

			core, removed, err := PartitionExtension(original, row.addonRoot, row.extensionPath)
			require.NoError(t, err)
			require.NotEmpty(t, removed)

			reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
			require.NoError(t, err)

			// Section names, key sets, and key ordering within a section must be
			// the original's; only a relative value's spelling changes.
			require.Equal(t, sectionNamesOf(t, original), sectionNamesOf(t, reassembled))
			require.Equal(t, canonicalSectionsOf(t, original, row.addonRoot, row.extensionPath),
				sectionsOf(t, reassembled),
				"each value must be the res:// resolution of the original's value")

			// The fixpoint: partitioning the emitted file lands back on the same
			// core body and the same entries.
			secondCore, secondRemoved, err := PartitionExtension(reassembled, row.addonRoot, row.extensionPath)
			require.NoError(t, err)
			require.Equal(t, string(core), string(secondCore))
			require.Equal(t, removed, secondRemoved)

			secondReassembled, err := ReassembleExtension(secondCore, secondRemoved, allSlices(secondRemoved))
			require.NoError(t, err)
			require.Equal(t, string(reassembled), string(secondReassembled),
				"emission must be idempotent from the first emission onward")
		})
	}
}

// TestExtensionPartitionResolvesValuesRelativeToTheExtensionFile pins the
// resolution rule itself at both positions the extension file can sit in: at the
// addon root, where the value is joined straight to the root, and in a
// subdirectory, where the file's own directory sits between them.
func TestExtensionPartitionResolvesValuesRelativeToTheExtensionFile(t *testing.T) {
	const body = `[configuration]

entry_symbol = "demo_init"

[libraries]

macos.debug = "bin/libdemo.framework"
windows.release.x86_64 = "nested/bin/libdemo.dll"
`
	for _, row := range []struct {
		name          string
		extensionPath string
		expected      ExtensionEntryTable[string]
	}{
		{
			name:          "extension file at the addon root",
			extensionPath: demoExtensionPath,
			expected: ExtensionEntryTable[string]{
				"macos.debug":            demoAddonRoot + "/bin/libdemo.framework",
				"windows.release.x86_64": demoAddonRoot + "/nested/bin/libdemo.dll",
			},
		},
		{
			name:          "extension file in a subdirectory",
			extensionPath: "extensions/inner/demo.gdextension",
			expected: ExtensionEntryTable[string]{
				"macos.debug":            demoAddonRoot + "/extensions/inner/bin/libdemo.framework",
				"windows.release.x86_64": demoAddonRoot + "/extensions/inner/nested/bin/libdemo.dll",
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, removed, err := PartitionExtension([]byte(body), demoAddonRoot, row.extensionPath)
			require.NoError(t, err)
			require.Equal(t, map[SliceID]ExtensionEntries{
				{Platform: "macos"}: {Libraries: ExtensionEntryTable[string]{
					"macos.debug": row.expected["macos.debug"],
				}},
				{Platform: "windows", Architecture: "x86_64"}: {Libraries: ExtensionEntryTable[string]{
					"windows.release.x86_64": row.expected["windows.release.x86_64"],
				}},
			}, removed)
		})
	}
}

// TestExtensionPartitionResolvesRelativeDependencyKeys asserts the rule reaches
// the deepest position a path occupies: every key of a [dependencies] Dictionary,
// with the destination beside it untouched because a destination is an export
// subdirectory rather than a path into the addon.
func TestExtensionPartitionResolvesRelativeDependencyKeys(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[dependencies]

macos.debug = { "bin/libsupport.dylib" : "", "data/extra.bin" : "data" }
ios.release = "bin/libsupport.a"
`
	_, removed, err := PartitionExtension([]byte(content), demoAddonRoot, "extensions/demo.gdextension")
	require.NoError(t, err)
	require.Equal(t, map[SliceID]ExtensionEntries{
		{Platform: "macos"}: {Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
			"macos.debug": {
				demoAddonRoot + "/extensions/bin/libsupport.dylib": "",
				demoAddonRoot + "/extensions/data/extra.bin":       "data",
			},
		}},
		{Platform: "ios"}: {Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
			"ios.release": {demoAddonRoot + "/extensions/bin/libsupport.a": ""},
		}},
	}, removed)
}

// TestExtensionPartitionRejectsDependencyKeysResolvingToOnePath pins the one
// place resolution makes a previously distinct pair collide. One Dictionary is
// one map from a dependency to its destination, so two spellings of one
// dependency carry two possibly different destinations for one file and are
// reported rather than silently resolved last-wins.
func TestExtensionPartitionRejectsDependencyKeysResolvingToOnePath(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[dependencies]

macos.debug = { "bin/libsupport.dylib" : "", "res://addons/demo/bin/libsupport.dylib" : "frameworks" }
`
	core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	requireManifestError(t, err, "bin/libsupport.dylib", "both name", demoAddonRoot+"/bin/libsupport.dylib")
	require.Nil(t, core)
	require.Nil(t, removed)
}

// TestExtensionPartitionAcceptsEntryKeysResolvingToOnePath is the sibling rule
// one level up: two platform tags legitimately name one file, which is what
// godot_jolt writes across its three Windows targets, so two entry keys whose
// values resolve to the same path are accepted however each was spelled.
// Duplicate entry keys stay rejected.
func TestExtensionPartitionAcceptsEntryKeysResolvingToOnePath(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[libraries]

windows.editor.x86_64 = "bin/libdemo.dll"
windows.template_release.x86_64 = "res://addons/demo/bin/libdemo.dll"
`
	_, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, ExtensionEntryTable[string]{
		"windows.editor.x86_64":           demoAddonRoot + "/bin/libdemo.dll",
		"windows.template_release.x86_64": demoAddonRoot + "/bin/libdemo.dll",
	}, removed[SliceID{Platform: "windows", Architecture: "x86_64"}].Libraries)
}

// TestExtensionPartitionAcceptsMixedResourceAndRelativeValues asserts the two
// input forms mix freely — within one section, across both sections, and within
// one [dependencies] Dictionary — and converge on the single published form.
func TestExtensionPartitionAcceptsMixedResourceAndRelativeValues(t *testing.T) {
	original, err := os.ReadFile("testdata/synthetic/mixed_values.gdextension")
	require.NoError(t, err)

	core, removed, err := PartitionExtension(original, demoAddonRoot, demoExtensionPath)
	require.NoError(t, err)
	require.Equal(t, map[SliceID]ExtensionEntries{
		{Platform: "macos"}: {
			Libraries: ExtensionEntryTable[string]{
				"macos.debug": demoAddonRoot + "/bin/libdemo.framework",
			},
			Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
				"macos.debug": {
					demoAddonRoot + "/bin/libsupport.dylib": "",
					demoAddonRoot + "/bin/libother.dylib":   "frameworks",
				},
			},
		},
		{Platform: "windows", Architecture: "x86_64"}: {
			Libraries: ExtensionEntryTable[string]{
				"windows.release.x86_64": demoAddonRoot + "/bin/libdemo.dll",
			},
			Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
				"windows.release.x86_64": {demoAddonRoot + "/bin/libsupport.dll": ""},
			},
		},
	}, removed)

	reassembled, err := ReassembleExtension(core, removed, allSlices(removed))
	require.NoError(t, err)
	for key, value := range pathsOf(t, reassembled)[string(SectionLibraries)] {
		require.True(t, strings.HasPrefix(value, resourcePrefix),
			"entry %q was emitted as %q rather than in the single published form", key, value)
	}
	for key, targets := range targetsOf(t, reassembled) {
		for path := range targets {
			require.True(t, strings.HasPrefix(path, resourcePrefix),
				"dependency %q of entry %q was emitted as a relative path", path, key)
		}
	}
}

// TestExtensionPartitionIsFailClosedOnRelativeValues asserts every row of the
// contract's relative-value table at each of the three depths a path occupies —
// a [libraries] value, a bare-string [dependencies] value, and a key of a
// [dependencies] Dictionary — because one rule governs all three and a rule that
// stops one level short of the data is the defect this table exists to prevent.
func TestExtensionPartitionIsFailClosedOnRelativeValues(t *testing.T) {
	// Each position renders one authored value into the body of its section, so
	// the same row runs unchanged at every depth.
	positions := []struct {
		name    string
		section ExtensionSection
		bodyOf  func(value string) string
	}{
		{
			name:    "libraries value",
			section: SectionLibraries,
			bodyOf:  func(value string) string { return fmt.Sprintf("macos.debug = %q", value) },
		},
		{
			name:    "dependencies bare string",
			section: SectionDependencies,
			bodyOf:  func(value string) string { return fmt.Sprintf("macos.debug = %q", value) },
		},
		{
			name:    "dependencies dictionary key",
			section: SectionDependencies,
			bodyOf:  func(value string) string { return fmt.Sprintf("macos.debug = { %q : \"\" }", value) },
		},
	}

	for _, row := range []struct {
		name            string
		value           string
		extensionPath   string
		expectedMessage []string
	}{
		{
			name:          "traversal that would resolve inside the addon root",
			value:         "bin/../other/libdemo.dylib",
			extensionPath: "extensions/demo.gdextension",
			expectedMessage: []string{
				"bin/../other/libdemo.dylib", `".."`, "rejected rather than simplified", resourcePrefix,
			},
		},
		{
			name:          "traversal that would escape the addon root",
			value:         "../../other/bin/libdemo.dylib",
			extensionPath: demoExtensionPath,
			expectedMessage: []string{
				"../../other/bin/libdemo.dylib", `".."`, "rejected rather than simplified", resourcePrefix,
			},
		},
		{
			name:            "dot component",
			value:           "./bin/libdemo.dylib",
			expectedMessage: []string{"./bin/libdemo.dylib", "simplest form"},
		},
		{
			name:            "empty component",
			value:           "bin//libdemo.dylib",
			expectedMessage: []string{"bin//libdemo.dylib", "empty component"},
		},
		{
			name:            "absolute host path",
			value:           "/usr/local/lib/libdemo.dylib",
			expectedMessage: []string{"/usr/local/lib/libdemo.dylib", resourcePrefix, "relative to the"},
		},
		{
			name:  "windows separators",
			value: `bin\libdemo.dll`,
			// The diagnostic quotes the value, so the separator appears escaped.
			expectedMessage: []string{`bin\\libdemo.dll`, resourcePrefix, "relative to the", "separators"},
		},
		{
			name:            "another uri scheme",
			value:           "user://libdemo.dylib",
			expectedMessage: []string{"user://libdemo.dylib", resourcePrefix, "relative to the"},
		},
		{
			name:            "empty value",
			value:           "",
			expectedMessage: []string{resourcePrefix, "relative to the"},
		},
		{
			// validatePartitionedValue runs on the resolved value, so a value that
			// would not survive being written back is caught whichever form it was
			// written in.
			name:            "quote in a relative value",
			value:           `bin/lib"demo.dylib`,
			expectedMessage: []string{"must not contain a quote", demoAddonRoot},
		},
		// A control character has no row here: the config lexer rejects the escape
		// that would carry one before any value rule runs. The rule itself is
		// asserted on the reassembly side, where an index assembled in memory can
		// carry one.
	} {
		for _, position := range positions {
			t.Run(row.name+"/"+position.name, func(t *testing.T) {
				content := fmt.Sprintf(
					"[configuration]\n\nentry_symbol = \"demo_init\"\n\n[%s]\n\n%s\n",
					position.section, position.bodyOf(row.value),
				)
				extensionPath := row.extensionPath
				if extensionPath == "" {
					extensionPath = demoExtensionPath
				}
				core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, extensionPath)
				requireManifestError(t, err, row.expectedMessage...)
				require.Nil(t, core, "a rejected .gdextension must yield no core body")
				require.Nil(t, removed, "a rejected .gdextension must yield no entries")
			})
		}
	}
}

// TestExtensionPartitionReportsAResolutionOutsideTheAddonRoot asserts the
// containment rule still names the value, the resolved path, and the root.
//
// A clean relative value cannot reach it: resolution prefixes the addon root and
// every traversal component is refused before resolution, so containment holds
// by construction, which
// TestExtensionResolutionOfACleanRelativeValueStaysInsideTheAddonRoot states as
// a property. The rule is therefore exercised through the res:// spelling, which
// is the same single containment rule and the spelling an author is told to use
// when a relative one cannot express their file.
func TestExtensionPartitionReportsAResolutionOutsideTheAddonRoot(t *testing.T) {
	const content = `[configuration]

entry_symbol = "demo_init"

[libraries]

macos.debug = "res://addons/other/bin/libdemo.dylib"
`
	core, removed, err := PartitionExtension([]byte(content), demoAddonRoot, demoExtensionPath)
	requireManifestError(t, err,
		"res://addons/other/bin/libdemo.dylib", "outside the addon root", demoAddonRoot)
	require.Nil(t, core)
	require.Nil(t, removed)
}

// TestExtensionResolutionOfACleanRelativeValueStaysInsideTheAddonRoot states the
// by-construction property the containment re-check guards: for every accepted
// extension path and every accepted relative value, the resolution is inside the
// addon root. It is what makes "a relative value is not a licence to leave the
// subtree" true by the shape of the rule rather than by a check that happens to
// run.
func TestExtensionResolutionOfACleanRelativeValueStaysInsideTheAddonRoot(t *testing.T) {
	for _, extensionPath := range []string{
		"demo.gdextension",
		"extensions/demo.gdextension",
		"a/b/c/demo.gdextension",
	} {
		for _, value := range []string{
			"libdemo.dylib",
			"bin/libdemo.dylib",
			"bin/nested/deeper/libdemo.dylib",
			"bin/..name/libdemo.dylib",
		} {
			t.Run(extensionPath+"|"+value, func(t *testing.T) {
				content := fmt.Sprintf(
					"[configuration]\n\nentry_symbol = \"demo_init\"\n\n[%s]\n\nmacos.debug = %q\n",
					SectionLibraries, value,
				)
				_, removed, err := PartitionExtension([]byte(content), demoAddonRoot, extensionPath)
				require.NoError(t, err)
				resolved := removed[SliceID{Platform: "macos"}].Libraries["macos.debug"]
				require.True(t, strings.HasPrefix(resolved, demoAddonRoot+"/"),
					"value %q under %q resolved to %q, outside the addon root", value, extensionPath, resolved)
			})
		}
	}
}

// TestExtensionPartitionRequiresTheExtensionsOwnPath asserts every row of the
// contract's table for the new parameter. The file's position is never defaulted
// to the addon root, because defaulting it would resolve a relative value
// against a directory the file does not sit in and publish a path naming a file
// that is not there.
func TestExtensionPartitionRequiresTheExtensionsOwnPath(t *testing.T) {
	for _, row := range []struct {
		name            string
		extensionPath   string
		expectedMessage []string
	}{
		{
			name:            "empty",
			extensionPath:   "",
			expectedMessage: []string{extensionSuffix, "is required"},
		},
		{
			name:            "absolute",
			extensionPath:   "/addons/demo/demo.gdextension",
			expectedMessage: []string{"must be relative"},
		},
		{
			name:            "windows separators",
			extensionPath:   `extensions\demo.gdextension`,
			expectedMessage: []string{"separators"},
		},
		{
			name:            "names a drive",
			extensionPath:   "C:/demo.gdextension",
			expectedMessage: []string{"must be relative"},
		},
		{
			name:            "a resource path rather than a path relative to the addon root",
			extensionPath:   demoAddonRoot + "/demo.gdextension",
			expectedMessage: []string{"must be relative"},
		},
		{
			name:            "empty component",
			extensionPath:   "extensions//demo.gdextension",
			expectedMessage: []string{"empty component"},
		},
		{
			name:            "dot component",
			extensionPath:   "./demo.gdextension",
			expectedMessage: []string{"simplest form"},
		},
		{
			name:            "traversal component",
			extensionPath:   "../demo.gdextension",
			expectedMessage: []string{"must not escape the addon root"},
		},
		{
			name:            "does not name a gdextension file",
			extensionPath:   "demo.cfg",
			expectedMessage: []string{"demo.cfg", extensionSuffix},
		},
		{
			name:            "is exactly the suffix",
			extensionPath:   extensionSuffix,
			expectedMessage: []string{extensionSuffix},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			core, removed, err := PartitionExtension([]byte(minimalExtension), demoAddonRoot, row.extensionPath)
			requireManifestError(t, err, row.expectedMessage...)
			require.Nil(t, core, "a rejected .gdextension must yield no core body")
			require.Nil(t, removed, "a rejected .gdextension must yield no entries")
		})
	}
}

// TestExtensionPartitionAcceptsAnExtensionPathNamingAnotherFile pins the one row
// of that table that is accepted and undetectable: the parameter is the caller's
// assertion about the bytes it handed over, and nothing on disk is consulted, so
// a mismatch cannot be reported here. The packager owns deriving the path from
// the same tree walk that read the content.
func TestExtensionPartitionAcceptsAnExtensionPathNamingAnotherFile(t *testing.T) {
	_, removed, err := PartitionExtension([]byte(minimalExtension), demoAddonRoot, "somewhere/else.gdextension")
	require.NoError(t, err)
	require.NotEmpty(t, removed)
}

// canonicalSectionsOf renders a fixture's own content into the form a round trip
// must produce: identical in every respect except that each partitioned entry's
// path is in the single published res:// form. It reads the fixture rather than
// hand-copying a list, so a relative-valued fixture's round trip is asserted
// against the author's file.
//
// A [dependencies] destination is deliberately left untouched: it is an export
// subdirectory rather than a path into the addon, so it is never resolved
// against the extension's position.
func canonicalSectionsOf(t *testing.T, content []byte, addonRoot, extensionPath string) map[string]ExtensionEntryTable[string] {
	t.Helper()
	directory := ""
	if separator := strings.LastIndex(extensionPath, "/"); separator >= 0 {
		directory = extensionPath[:separator] + "/"
	}
	canonicalize := func(value string) string {
		if strings.HasPrefix(value, resourcePrefix) {
			return value
		}
		return addonRoot + "/" + directory + value
	}

	sections := map[string]ExtensionEntryTable[string]{}
	for _, section := range parseFixture(t, content).Sections {
		table := sections[section.Name]
		if table == nil {
			table = ExtensionEntryTable[string]{}
			sections[section.Name] = table
		}
		partitioned := slices.Contains(PartitionedSections(), ExtensionSection(section.Name))
		for _, statement := range section.Statements {
			assignment, isAssignment := statement.(*ast.Assignment)
			if !isAssignment {
				continue
			}
			if !partitioned {
				table[assignment.Key] = formatFixtureValue(assignment)
				continue
			}
			if section.Name == string(SectionDependencies) {
				targets := dependencyTargetsOfFixtureValue(t, assignment.Value)
				canonical := ExtensionDependencyTargets{}
				for path, destination := range targets {
					canonical[canonicalize(path)] = destination
				}
				rendered := make([]string, 0, len(canonical))
				for _, path := range sortedKeys(canonical) {
					rendered = append(rendered, fmt.Sprintf("%q -> %q", path, canonical[path]))
				}
				table[assignment.Key] = strings.Join(rendered, ", ")
				continue
			}
			literal, isString := assignment.Value.(*ast.StringLiteral)
			require.True(t, isString, "partitioned entry %q is not a string", assignment.Key)
			table[assignment.Key] = formatFixtureValue(&ast.Assignment{
				Key:   assignment.Key,
				Value: &ast.StringLiteral{Value: canonicalize(literal.Value)},
			})
		}
	}
	return sections
}

// TestExtensionReassemblyAcceptsOneKeySharedByFannedOutSlices covers the
// selection `gpm package`'s fan-out produces: a platform's architecture-less
// entries are copied into every architecture slice of that platform so each one
// reassembles alone, so a project selecting two of those architectures sees the
// shared key in both. Both copies carry the same value, and the entry is written
// once.
func TestExtensionReassemblyAcceptsOneKeySharedByFannedOutSlices(t *testing.T) {
	core := []byte("[configuration]\n\nentry_symbol = \"demo_init\"\n\n[libraries]\n")
	arm64 := SliceID{Platform: "macos", Architecture: "arm64"}
	universal := SliceID{Platform: "macos", Architecture: "universal"}
	entries := map[SliceID]ExtensionEntries{
		arm64: {Libraries: ExtensionEntryTable[string]{
			"macos.editor":               "res://addons/demo/bin/demo.framework",
			"macos.template_debug.arm64": "res://addons/demo/bin/demo_arm64.framework",
		}},
		universal: {Libraries: ExtensionEntryTable[string]{
			"macos.editor":                     "res://addons/demo/bin/demo.framework",
			"macos.template_release.universal": "res://addons/demo/bin/demo_universal.framework",
		}},
	}

	reassembled, err := ReassembleExtension(core, entries, []SliceID{CoreSliceID(), arm64, universal})
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(reassembled), "macos.editor"))
	require.Contains(t, string(reassembled), "res://addons/demo/bin/demo_arm64.framework")
	require.Contains(t, string(reassembled), "res://addons/demo/bin/demo_universal.framework")
}
