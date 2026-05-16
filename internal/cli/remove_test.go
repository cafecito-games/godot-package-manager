package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoveDeletesAddon(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"),
		[]byte("[addons]\n[addons.x]\nsource = \"archive\"\nurl = \"u\"\n"), 0o644))
	installed := filepath.Join(dir, "addons", "x")
	require.NoError(t, os.MkdirAll(installed, 0o755))

	cmd := newRemoveCommand(&Options{})
	cmd.SetArgs([]string{"x", "--dir", dir})
	require.NoError(t, cmd.Execute())

	_, err := os.Stat(installed)
	require.True(t, os.IsNotExist(err))
	data, _ := os.ReadFile(filepath.Join(dir, "addons.toml"))
	require.NotContains(t, string(data), "addons.x")
}

func TestRemoveUnknownAddon(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))
	cmd := newRemoveCommand(&Options{})
	cmd.SetArgs([]string{"nope", "--dir", dir})
	err := cmd.Execute()
	require.Error(t, err)
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
}
