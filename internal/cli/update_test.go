package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateUnknownAddon(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))
	cmd := newUpdateCommand(&Options{})
	cmd.SetArgs([]string{"nope", "--dir", dir})
	err := cmd.Execute()
	require.Error(t, err)
	var usageErr *UsageError
	require.ErrorAs(t, err, &usageErr)
}
