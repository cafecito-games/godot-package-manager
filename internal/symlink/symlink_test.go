package symlink

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTreeValidatesFourSymlinkFrameworkLayout(t *testing.T) {
	tree := NewTree()
	require.NoError(t, tree.AddFile("bin/macos/SwiftGodot.framework/Versions/A/SwiftGodot"))
	require.NoError(t, tree.AddFile("bin/macos/SwiftGodot.framework/Versions/A/Resources/Info.plist"))
	require.NoError(t, tree.AddFile("bin/macos/SwiftGodot.framework/Versions/A/Headers/SwiftGodot.h"))
	require.NoError(t, tree.AddSymlink("bin/macos/SwiftGodot.framework/SwiftGodot", "Versions/Current/SwiftGodot"))
	require.NoError(t, tree.AddSymlink("bin/macos/SwiftGodot.framework/Resources", "Versions/Current/Resources"))
	require.NoError(t, tree.AddSymlink("bin/macos/SwiftGodot.framework/Headers", "Versions/Current/Headers"))
	require.NoError(t, tree.AddSymlink("bin/macos/SwiftGodot.framework/Versions/Current", "A"))

	ordered, err := tree.Validate()
	require.NoError(t, err)
	require.Len(t, ordered, 4)
	require.Equal(t, "bin/macos/SwiftGodot.framework/Versions/Current", ordered[0].Path,
		"referenced target symlink is ordered before links that chain through it")

	root := t.TempDir()
	for _, filePath := range []string{
		"bin/macos/SwiftGodot.framework/Versions/A/SwiftGodot",
		"bin/macos/SwiftGodot.framework/Versions/A/Resources/Info.plist",
		"bin/macos/SwiftGodot.framework/Versions/A/Headers/SwiftGodot.h",
	} {
		destination := filepath.Join(root, filepath.FromSlash(filePath))
		require.NoError(t, os.MkdirAll(filepath.Dir(destination), 0o755))
		require.NoError(t, os.WriteFile(destination, []byte("payload"), 0o644))
	}
	require.NoError(t, tree.Materialize(root))

	target, err := os.Readlink(filepath.Join(root, "bin", "macos", "SwiftGodot.framework", "SwiftGodot"))
	require.NoError(t, err)
	require.Equal(t, filepath.FromSlash("Versions/Current/SwiftGodot"), target)

	content, err := os.ReadFile(filepath.Join(root, "bin", "macos", "SwiftGodot.framework", "SwiftGodot"))
	require.NoError(t, err)
	require.Equal(t, "payload", string(content))
}

func TestTreePermitsDanglingContainedSymlink(t *testing.T) {
	tree := NewTree()
	require.NoError(t, tree.AddSymlink("SwiftGodot.framework/Versions/Current", "A"))
	require.NoError(t, tree.AddSymlink("SwiftGodot.framework/Headers", "Versions/Current/Headers"))

	root := t.TempDir()
	require.NoError(t, tree.Materialize(root))

	info, err := os.Lstat(filepath.Join(root, "SwiftGodot.framework", "Headers"))
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestTreeRejectsUnsafeTargetsAndPaths(t *testing.T) {
	cases := map[string]struct {
		setup   func(t *testing.T, tree *Tree) error
		wantErr string
	}{
		"absolute unix target": {
			setup: func(_ *testing.T, tree *Tree) error {
				return tree.AddSymlink("evil", "/etc/passwd")
			},
			wantErr: "absolute",
		},
		"windows drive target": {
			setup: func(_ *testing.T, tree *Tree) error {
				return tree.AddSymlink("evil", "C:/Windows/System32")
			},
			wantErr: "absolute",
		},
		"backslash in target": {
			setup: func(_ *testing.T, tree *Tree) error {
				return tree.AddSymlink("a/b/evil", `..\..\secret`)
			},
			wantErr: "backslash",
		},
		"empty target": {
			setup: func(_ *testing.T, tree *Tree) error {
				return tree.AddSymlink("evil", "")
			},
			wantErr: "empty",
		},
		"direct parent traversal escape": {
			setup: func(_ *testing.T, tree *Tree) error {
				return tree.AddSymlink("nested/evil", "../../outside")
			},
			wantErr: "escapes",
		},
		"momentary parent escape that re-enters": {
			setup: func(_ *testing.T, tree *Tree) error {
				return tree.AddSymlink("evil", "../addon/plugin.cfg")
			},
			wantErr: "escapes",
		},
		"escape reachable only by chaining two individually contained links": {
			setup: func(t *testing.T, tree *Tree) error {
				require.NoError(t, tree.AddSymlink("a/b/up", "../.."))
				require.NoError(t, tree.AddSymlink("a/b/escape", "up/../secret"))
				_, err := tree.Validate()
				return err
			},
			wantErr: "escapes",
		},
		"case-folded chain escape": {
			setup: func(t *testing.T, tree *Tree) error {
				require.NoError(t, tree.AddSymlink("a/b/UP", "../.."))
				require.NoError(t, tree.AddSymlink("a/b/escape", "up/../secret"))
				_, err := tree.Validate()
				return err
			},
			wantErr: "escapes",
		},
		"target escaping via materialized parent link": {
			setup: func(t *testing.T, tree *Tree) error {
				require.NoError(t, tree.AddSymlink("a/b/parent", "../.."))
				require.NoError(t, tree.AddSymlink("a/b/parent/escape", "../secret"))
				_, err := tree.Validate()
				return err
			},
			wantErr: "ancestor",
		},
		"regular file beneath symlinked ancestor": {
			setup: func(t *testing.T, tree *Tree) error {
				require.NoError(t, tree.AddSymlink("a/parent", ".."))
				require.NoError(t, tree.AddFile("a/parent/file.txt"))
				_, err := tree.Validate()
				return err
			},
			wantErr: "directory",
		},
		"symlink colliding with regular file": {
			setup: func(t *testing.T, tree *Tree) error {
				require.NoError(t, tree.AddFile("plugin.cfg"))
				require.NoError(t, tree.AddSymlink("PLUGIN.CFG", "other.cfg"))
				_, err := tree.Validate()
				return err
			},
			wantErr: "collides",
		},
		"symlink cycle": {
			setup: func(t *testing.T, tree *Tree) error {
				require.NoError(t, tree.AddSymlink("first", "second"))
				require.NoError(t, tree.AddSymlink("second", "first"))
				_, err := tree.Validate()
				return err
			},
			wantErr: "cycle",
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			tree := NewTree()
			err := testCase.setup(t, tree)
			require.Error(t, err)
			require.Contains(t, err.Error(), testCase.wantErr)
		})
	}
}
