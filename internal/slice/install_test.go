package slice

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

func TestAddonRootsDeriveFromOneDeclaration(t *testing.T) {
	require.Equal(t, "addons/limboai", AddonInstallPath("limboai"))
	require.Equal(t, "res://addons/limboai", AddonResourceRoot("limboai"))
}

// TestIndexFileNameHasOneSpelling pins that the index asset name, which a
// producer writes and a consumer discovers an addon by, is declared exactly
// once. Two spellings of one wire-format name that must agree forever is the
// defect this declaration exists to prevent, and it is only prevented while
// nothing else repeats the literal.
func TestIndexFileNameHasOneSpelling(t *testing.T) {
	literal := []byte(`"` + IndexFileName + `"`)
	var declaring []string
	require.NoError(t, filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(content, literal) {
			declaring = append(declaring, filepath.ToSlash(path))
		}
		return nil
	}))
	require.Equal(t, []string{"../slice/install.go"}, declaring)
}

func TestRebaseEntriesRewritesBothSections(t *testing.T) {
	entries := ExtensionEntries{
		Libraries: ExtensionEntryTable[string]{
			"ios.template_release.arm64": "res://addons/published/bin/addon.dylib",
		},
		Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
			"ios.template_release.arm64": {"res://addons/published/bin/libgodot.a": "Frameworks"},
		},
	}
	rebased, err := RebaseEntries(
		SliceID{Platform: "ios", Architecture: "arm64"}, "addon.gdextension", entries,
		AddonResourceRoot("published"), AddonResourceRoot("installed"))
	require.NoError(t, err)
	require.Equal(t, ExtensionEntryTable[string]{
		"ios.template_release.arm64": "res://addons/installed/bin/addon.dylib",
	}, rebased.Libraries)
	require.Equal(t, ExtensionEntryTable[ExtensionDependencyTargets]{
		"ios.template_release.arm64": {"res://addons/installed/bin/libgodot.a": "Frameworks"},
	}, rebased.Dependencies)

	// The input is never modified, so a caller may re-root the same index for
	// two projects.
	require.Equal(t, "res://addons/published/bin/addon.dylib", entries.Libraries["ios.template_release.arm64"])
}

// TestRebaseEntriesIsIdentityForTheSameRoot pins that an addon installed under
// its published name is byte-identical to what the index declared, so the
// re-rooting adds nothing to the overwhelmingly common case.
func TestRebaseEntriesIsIdentityForTheSameRoot(t *testing.T) {
	entries := ExtensionEntries{
		Libraries: ExtensionEntryTable[string]{
			"macos.template_release": "res://addons/same/bin/addon.dylib",
		},
	}
	rebased, err := RebaseEntries(SliceID{Platform: "macos"}, "addon.gdextension", entries,
		AddonResourceRoot("same"), AddonResourceRoot("same"))
	require.NoError(t, err)
	require.Equal(t, entries.Libraries, rebased.Libraries)
	require.Nil(t, rebased.Dependencies, "an absent section stays absent rather than becoming empty")
}

func TestRebaseEntriesRejectsPathsOutsideThePublishedRoot(t *testing.T) {
	cases := map[string]string{
		"another addon":  "res://addons/other/bin/addon.dylib",
		"the root files": "res://project.godot",
		// Shares the root as a string prefix without being inside it, which only
		// the shared containment comparison rejects: it is a clean res:// path, so
		// every grammar rule accepts it.
		"a sibling sharing the root's prefix": "res://addons/publishedx/bin/addon.dylib",
		// The root itself names a directory rather than a file in the addon.
		"the root itself": "res://addons/published",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := RebaseEntries(
				SliceID{Platform: "macos"}, "addon.gdextension",
				ExtensionEntries{Libraries: ExtensionEntryTable[string]{"macos.template_release": value}},
				AddonResourceRoot("published"), AddonResourceRoot("installed"))
			require.Error(t, err)
			var fetchError *output.FetchError
			require.ErrorAs(t, err, &fetchError)
			require.Equal(t, output.ExitFetch, output.CodeFor(err))
			require.Contains(t, err.Error(), `index slice "macos"`)
			require.Contains(t, err.Error(), `libraries entry "macos.template_release"`)
			require.Contains(t, err.Error(), "addon.gdextension")
			require.Contains(t, err.Error(), value)
		})
	}
}

func TestRebaseEntriesRejectsDependencyKeyOutsideThePublishedRoot(t *testing.T) {
	_, err := RebaseEntries(
		SliceID{Platform: "ios", Architecture: "arm64"}, "addon.gdextension",
		ExtensionEntries{
			Dependencies: ExtensionEntryTable[ExtensionDependencyTargets]{
				"ios.template_release.arm64": {"res://addons/other/bin/libgodot.a": ""},
			},
		},
		AddonResourceRoot("published"), AddonResourceRoot("installed"))
	require.Error(t, err)
	var fetchError *output.FetchError
	require.ErrorAs(t, err, &fetchError)
	require.Contains(t, err.Error(), `dependencies entry "ios.template_release.arm64"`)
	require.Contains(t, err.Error(), "res://addons/other/bin/libgodot.a")
}

func TestRebaseEntriesRejectsAnInvalidRoot(t *testing.T) {
	_, err := RebaseEntries(SliceID{Platform: "macos"}, "addon.gdextension", ExtensionEntries{},
		"addons/published", AddonResourceRoot("installed"))
	require.Error(t, err)
	var fetchError *output.FetchError
	require.ErrorAs(t, err, &fetchError)
	require.Contains(t, err.Error(), "published invalid addon root")
}
