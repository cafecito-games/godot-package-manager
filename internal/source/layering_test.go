package source

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// forbiddenInternalImports are this module's packages internal/source must not
// reach. internal/packager is the producer half of the slices feature: it writes
// the archives and the index this package reads, and the producer is not a
// dependency of the consumer. Everything the two must agree on — the index
// schema, the index asset name, the addon root's layout — lives in
// internal/slice, which both import.
//
// The fixtures in this package's tests do drive the real packager, which is
// deliberate and does not create this edge: a test import is not a dependency
// of the package, so `go list -deps ./internal/source` names no producer.
var forbiddenInternalImports = []string{
	"github.com/cafecito-games/godot-package-manager/internal/packager",
}

func TestSourceDoesNotImportTheProducer(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fileSet := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileSet, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, declaredImport := range parsed.Imports {
			path, err := strconv.Unquote(declaredImport.Path.Value)
			require.NoError(t, err)
			for _, forbidden := range forbiddenInternalImports {
				require.NotEqual(t, forbidden, path,
					"%s imports %s; the producer of an addon's slices is not a dependency of the consumer that installs them",
					name, path)
			}
		}
	}
}
