package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-addon-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestInitCreatesManifest(t *testing.T) {
	dir := t.TempDir()
	cmd := newInitCommand(&Options{})
	cmd.SetArgs([]string{"--dir", dir})
	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(filepath.Join(dir, "addons.toml"))
	require.NoError(t, err)
	require.Contains(t, string(data), "[addons]")
	require.Contains(t, string(data), "# [addons.dialogue_manager]")
}

func TestInitDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("existing"), 0o644))
	cmd := newInitCommand(&Options{})
	cmd.SetArgs([]string{"--dir", dir})
	err := cmd.Execute()
	require.Error(t, err)
	var manifestErr *output.ManifestError
	require.ErrorAs(t, err, &manifestErr)
}
