package slice

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/cafecito-games/godot-package-manager/"

// allowedInternalImports is the complete set of this module's packages that
// internal/slice may import. It is the lowest layer of the tree: every other
// package is free to depend on it, which is only possible while it depends on
// nothing but the standard library, third-party libraries, and the exit-code
// taxonomy.
var allowedInternalImports = []string{"internal/output"}

func TestSliceImportsOnlyLowerLayers(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fileSet := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, filepath.Join(".", entry.Name()), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, declaredImport := range parsed.Imports {
			path, err := strconv.Unquote(declaredImport.Path.Value)
			require.NoError(t, err)
			if !strings.HasPrefix(path, modulePath) {
				continue
			}
			internalPath := strings.TrimPrefix(path, modulePath)
			require.True(t, slices.Contains(allowedInternalImports, internalPath),
				"%s imports %s; internal/slice is the lowest layer and packages above it import it, so importing one back would be an import cycle",
				entry.Name(), internalPath)
		}
	}
}

// filesystemFreeSourceFiles are the files in this package whose contract is that
// they perform no filesystem access at all, which is what lets partition and
// reassembly resolve a path written relative to the .gdextension by string
// arithmetic alone. index.go is deliberately not among them: it loads and saves
// gpm-index.toml.
var filesystemFreeSourceFiles = []string{"gdextension.go"}

// TestExtensionSourceReachesNoFilesystemPackage pins the no-filesystem property
// at the import level rather than only by behavior. A path/filepath call would
// also apply the host's separator and cleaning rules to a Godot resource path,
// so a relative entry value is resolved with strings alone.
func TestExtensionSourceReachesNoFilesystemPackage(t *testing.T) {
	forbidden := []string{"os", "path/filepath", "io/fs", "io/ioutil"}

	fileSet := token.NewFileSet()
	for _, name := range filesystemFreeSourceFiles {
		parsed, err := parser.ParseFile(fileSet, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, declaredImport := range parsed.Imports {
			path, err := strconv.Unquote(declaredImport.Path.Value)
			require.NoError(t, err)
			require.False(t, slices.Contains(forbidden, path),
				"%s imports %s; partitioning and reassembling a .gdextension perform no filesystem access, and a relative entry value is resolved by string arithmetic alone",
				name, path)
		}
	}
}
