package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDiscoverWalksUp(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot"), nil, 0o644))
	nested := filepath.Join(root, "scenes", "ui")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	p, err := Discover(nested)
	require.NoError(t, err)
	require.Equal(t, root, p.Root)
	require.Equal(t, filepath.Join(root, "addons.toml"), p.ManifestPath)
	require.Equal(t, filepath.Join(root, "addons.lock"), p.LockPath)
	require.Equal(t, filepath.Join(root, "addons"), p.AddonsDir)
}

func TestDiscoverNotFound(t *testing.T) {
	_, err := Discover(t.TempDir())
	require.Error(t, err)
}
