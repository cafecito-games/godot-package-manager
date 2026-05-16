package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func gitInitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "plugin.cfg"), []byte("[plugin]"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	run("tag", "v1.0")
	return repo
}

func TestEndToEndInstallGitAddon(t *testing.T) {
	repo := gitInitRepo(t)
	proj := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(proj, "project.godot"), nil, 0o644))
	manifestBody := "[addons]\n[addons.dlg]\nsource = \"git\"\nurl = \"" + repo + "\"\nversion = \"v1.0\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(proj, "addons.toml"), []byte(manifestBody), 0o644))

	cmd := NewRootCommand()
	cmd.SetArgs([]string{"install", "--dir", proj})
	require.NoError(t, cmd.Execute())

	_, err := os.Stat(filepath.Join(proj, "addons", "dlg", "plugin.cfg"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(proj, "addons.lock"))
	require.NoError(t, err)
}
