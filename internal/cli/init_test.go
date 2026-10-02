package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/project"
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

// TestInitGitignoresTheStateFile pins that the machine-local state file is
// ignored by default: committing it would publish one machine's slice layout to
// the whole team.
func TestInitGitignoresTheStateFile(t *testing.T) {
	t.Run("creates .gitignore when none exists", func(t *testing.T) {
		dir := t.TempDir()
		cmd := newInitCommand(&Options{})
		cmd.SetArgs([]string{"--dir", dir})
		require.NoError(t, cmd.Execute())

		data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
		require.NoError(t, err)
		require.Equal(t, project.StateFileName+"\n", string(data))
	})

	t.Run("appends to an existing .gitignore", func(t *testing.T) {
		dir := t.TempDir()
		gitignore := filepath.Join(dir, ".gitignore")
		require.NoError(t, os.WriteFile(gitignore, []byte(".godot/\nexport_presets.cfg"), 0o644))

		cmd := newInitCommand(&Options{})
		cmd.SetArgs([]string{"--dir", dir})
		require.NoError(t, cmd.Execute())

		data, err := os.ReadFile(gitignore)
		require.NoError(t, err)
		require.Equal(t, ".godot/\nexport_presets.cfg\n"+project.StateFileName+"\n", string(data))
	})

	t.Run("leaves an existing entry alone", func(t *testing.T) {
		dir := t.TempDir()
		gitignore := filepath.Join(dir, ".gitignore")
		existing := ".godot/\n  " + project.StateFileName + "  \n"
		require.NoError(t, os.WriteFile(gitignore, []byte(existing), 0o644))

		cmd := newInitCommand(&Options{})
		cmd.SetArgs([]string{"--dir", dir})
		require.NoError(t, cmd.Execute())

		data, err := os.ReadFile(gitignore)
		require.NoError(t, err)
		require.Equal(t, existing, string(data), "an entry already present is not duplicated")
	})
}

// TestInitLeavesNoManifestWhenTheGitignoreWriteFails pins that a failed init
// performs no mutation it cannot retry: the idempotent .gitignore entry is
// written first, so a failure there leaves no addons.toml for the retry to trip
// over.
func TestInitLeavesNoManifestWhenTheGitignoreWriteFails(t *testing.T) {
	dir := t.TempDir()
	// A directory where .gitignore belongs is neither readable nor writable as
	// a file, without depending on the suite's effective user as a mode-based
	// test would.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".gitignore"), 0o755))

	cmd := newInitCommand(&Options{})
	cmd.SetArgs([]string{"--dir", dir})
	require.Error(t, cmd.Execute())

	_, err := os.Stat(filepath.Join(dir, "addons.toml"))
	require.True(t, os.IsNotExist(err), "a failed init must not leave a manifest behind")
}
