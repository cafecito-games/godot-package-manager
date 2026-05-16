package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-addon-manager/internal/manifest"
	"github.com/cafecito-games/godot-addon-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func makeLocalRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	run("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.cfg"), []byte("[plugin]"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")
	run("tag", "v1.0")
	return dir
}

func TestGitFetchChecksOutTag(t *testing.T) {
	repo := makeLocalRepo(t)
	f := &GitFetcher{}
	res, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGit, URL: repo, Version: "v1.0",
	})
	require.NoError(t, err)
	defer os.RemoveAll(res.Dir)
	require.Len(t, res.ResolvedVersion, 40) // full SHA
	_, err = os.Stat(filepath.Join(res.Dir, "plugin.cfg"))
	require.NoError(t, err)
}

func TestGitFetchBadRef(t *testing.T) {
	repo := makeLocalRepo(t)
	f := &GitFetcher{}
	_, err := f.Fetch(context.Background(), manifest.AddonSpec{
		Source: manifest.SourceGit, URL: repo, Version: "v9.9",
	})
	require.Error(t, err)
	var fetchError *output.FetchError
	require.ErrorAs(t, err, &fetchError)
}
