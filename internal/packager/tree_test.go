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
	t.Run("absolute target", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "plugin.gd"), []byte("a"), 0o644))
		require.NoError(t, os.Symlink(filepath.Join(root, "plugin.gd"), filepath.Join(root, "alias.gd")))

		_, err := walkAddonTree(root, ".")
		require.Error(t, err)
		var manifestError *output.ManifestError
		require.ErrorAs(t, err, &manifestError)
		require.Equal(t, output.ExitManifest, output.CodeFor(err))
		require.Contains(t, err.Error(), "alias.gd")
	})

	t.Run("parent traversal escape", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "plugin.gd"), []byte("a"), 0o644))
		require.NoError(t, os.Symlink("../secret", filepath.Join(root, "escape")))

		_, err := walkAddonTree(root, ".")
		require.Error(t, err)
		var manifestError *output.ManifestError
		require.ErrorAs(t, err, &manifestError)
		require.Contains(t, err.Error(), "escape")
	})

	t.Run("chained escape across two individually contained links", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, "plugin.gd"), []byte("a"), 0o644))
		require.NoError(t, os.Symlink("../..", filepath.Join(root, "a", "b", "up")))
		require.NoError(t, os.Symlink("up/../secret", filepath.Join(root, "a", "b", "escape")))

		_, err := walkAddonTree(root, ".")
		require.Error(t, err)
		var manifestError *output.ManifestError
		require.ErrorAs(t, err, &manifestError)
		require.Contains(t, err.Error(), "a/b/escape")
	})
}

func TestWalkAddonTreeAcceptsContainedRelativeSymlinks(t *testing.T) {
	root := t.TempDir()
	frameworkPath := filepath.Join(root, "bin", "addon.framework")
	require.NoError(t, os.MkdirAll(filepath.Join(frameworkPath, "Versions", "A", "Resources"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(frameworkPath, "Versions", "A", "addon"), []byte("binary"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(frameworkPath, "Versions", "A", "Resources", "Info.plist"), []byte("plist"), 0o644))
	require.NoError(t, os.Symlink("A", filepath.Join(frameworkPath, "Versions", "Current")))
	require.NoError(t, os.Symlink("Versions/Current/addon", filepath.Join(frameworkPath, "addon")))
	require.NoError(t, os.Symlink("Versions/Current/Resources", filepath.Join(frameworkPath, "Resources")))
	require.NoError(t, os.Symlink("Versions/Current/Headers", filepath.Join(frameworkPath, "Headers")))

	tree, err := walkAddonTree(root, ".")
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.close()) }()

	require.Equal(t, []string{
		"bin/addon.framework/Headers",
		"bin/addon.framework/Resources",
		"bin/addon.framework/Versions/A/Resources/Info.plist",
		"bin/addon.framework/Versions/A/addon",
		"bin/addon.framework/Versions/Current",
		"bin/addon.framework/addon",
	}, tree.resolve("bin/addon.framework"))
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

func TestOpenAddonRootRefusesASymlinkedComponent(t *testing.T) {
	repository := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repository, "real", "addon"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(repository, "real"), filepath.Join(repository, "addons")))

	_, err := openAddonRoot(repository, "addons/addon")
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.ErrorAs(t, err, &manifestError)
	require.Contains(t, err.Error(), "symlink")
}

func TestOpenAddonRootReadsTheSubtreeThroughThePinnedHandle(t *testing.T) {
	repository := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repository, "addons", "addon"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "addons", "addon", "plugin.gd"), []byte("a"), 0o644))
	// A directory elsewhere in the repository that addon_path must not reach.
	require.NoError(t, os.MkdirAll(filepath.Join(repository, "private"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "private", "key.pem"), []byte("secret"), 0o600))

	tree, err := walkAddonTree(repository, "addons/addon")
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.close()) }()
	require.Equal(t, []string{"plugin.gd"}, tree.paths())

	// Nothing outside the pinned root is reachable through it, whatever the name.
	_, err = tree.root.Open("../private/key.pem")
	require.Error(t, err)
}
