package packager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

func TestWalkAddonTreeCollectsRegularFilesInOrder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin", "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin.gd"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "library.so"), []byte("b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "nested", "data.bin"), []byte("c"), 0o644))

	tree, err := walkAddonTree(root, ".")
	require.NoError(t, err)
	require.Equal(t, []string{"bin/library.so", "bin/nested/data.bin", "plugin.gd"}, tree.paths())
	require.True(t, tree.files[0].executable)
	require.False(t, tree.files[1].executable)
}

func TestWalkAddonTreeRejectsASymlink(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin.gd"), []byte("a"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(root, "plugin.gd"), filepath.Join(root, "alias.gd")))

	_, err := walkAddonTree(root, ".")
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.ErrorAs(t, err, &manifestError)
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
	require.Contains(t, err.Error(), "alias.gd")
}

func TestWalkAddonTreeRejectsAnEmptyTree(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))

	_, err := walkAddonTree(root, ".")
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.ErrorAs(t, err, &manifestError)
	require.Contains(t, err.Error(), "no files")
}

func TestAddonTreeResolvesADirectoryBundleToItsFiles(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "bin", "addon.framework", "Versions", "A")
	require.NoError(t, os.MkdirAll(bundle, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(bundle, "addon"), []byte("a"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "addon.framework", "Info.plist"), []byte("b"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin.gd"), []byte("c"), 0o644))

	tree, err := walkAddonTree(root, ".")
	require.NoError(t, err)
	require.Equal(t,
		[]string{"bin/addon.framework/Info.plist", "bin/addon.framework/Versions/A/addon"},
		tree.resolve("bin/addon.framework"),
	)
	require.Equal(t, []string{"plugin.gd"}, tree.resolve("plugin.gd"))
	require.Nil(t, tree.resolve("bin/absent.so"))
	require.Nil(t, tree.resolve("plugin"))
}

func TestAddonTreeRefusesAFileReachedThroughADirectorySwappedAfterTheWalk(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bin", "library.so"), []byte("mine"), 0o755))

	tree, err := walkAddonTree(root, ".")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, tree.close()) })
	require.Equal(t, []string{"bin/library.so"}, tree.paths())

	// A directory the walk already descended into is replaced by a link to a
	// directory outside the addon root that holds a same-named file. Every
	// component of "bin/library.so" still resolves, so nothing but an
	// escape-proof open refuses it.
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "library.so"), []byte("secret"), 0o755))
	require.NoError(t, os.RemoveAll(filepath.Join(root, "bin")))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "bin")))

	archivePath := filepath.Join(t.TempDir(), "addon-core.zip")
	err = writeArchive(archivePath, archiveFilesFor(tree, tree.paths()))
	require.Error(t, err)
	_, statErr := os.Stat(archivePath)
	require.True(t, os.IsNotExist(statErr))
}

// archiveFilesFor builds archive entries for walked paths, the way the packager
// does for a platform slice.
func archiveFilesFor(tree *addonTree, relativePaths []string) []archiveFile {
	files := make([]archiveFile, 0, len(relativePaths))
	for _, relativePath := range relativePaths {
		files = append(files, tree.archiveFileAt(relativePath))
	}
	return files
}
