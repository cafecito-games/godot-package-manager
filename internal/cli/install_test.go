package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInstallCommandJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"),
		[]byte("[addons]\n[addons.x]\nsource = \"git\"\nurl = \"u\"\n"), 0o644)) // missing version

	cmd := newInstallCommand(&Options{})
	cmd.SetArgs([]string{"--dir", dir})
	require.Error(t, cmd.Execute()) // validation failure
}
