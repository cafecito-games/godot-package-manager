package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/stretchr/testify/require"
)

// gitCommit adds all files in repo and commits them with msg, returning the
// resulting commit SHA.
func gitCommit(t *testing.T, repo, msg string) string {
	t.Helper()
	run := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = gitTestEnv()
		out, err := c.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	run("add", ".")
	run("commit", "-q", "-m", msg)
	return run("rev-parse", "HEAD")
}

// TestInstallReproducible verifies that:
//  1. gpm install records a resolved SHA in addons.lock.
//  2. A second gpm install after a new commit does NOT update the SHA (lock is honored).
//  3. gpm update after a new commit DOES update the SHA and installs the new file.
func TestInstallReproducible(t *testing.T) {
	// Create a git repo with an initial commit on branch "main".
	repo := t.TempDir()
	runInRepo := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = gitTestEnv()
		out, err := c.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	runInRepo("init", "-q", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "plugin.cfg"), []byte("[plugin]"), 0o644))
	firstSHA := gitCommit(t, repo, "init")

	// Set up a project directory.
	proj := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(proj, "project.godot"), nil, 0o644))
	manifestBody := "[addons]\n[addons.myaddon]\nsource = \"git\"\nurl = \"" + repo + "\"\nversion = \"main\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(proj, "addons.toml"), []byte(manifestBody), 0o644))

	lockPath := filepath.Join(proj, "addons.lock")

	runGPM := func(args ...string) {
		t.Helper()
		cmd := NewRootCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append(args, "--dir", proj))
		require.NoError(t, cmd.Execute())
	}

	// First install — resolves and pins firstSHA.
	runGPM("install")

	lock, err := manifest.LoadLock(lockPath)
	require.NoError(t, err)
	require.Equal(t, firstSHA, lock.Addons["myaddon"].ResolvedVersion, "first install should pin the initial SHA")

	// Add a second commit to the repo (a new file).
	secondFile := filepath.Join(repo, "second.txt")
	require.NoError(t, os.WriteFile(secondFile, []byte("new"), 0o644))
	secondSHA := gitCommit(t, repo, "second commit")
	require.NotEqual(t, firstSHA, secondSHA)

	// Second install — must honor the lock and stay on firstSHA.
	runGPM("install")

	lock, err = manifest.LoadLock(lockPath)
	require.NoError(t, err)
	require.Equal(t, firstSHA, lock.Addons["myaddon"].ResolvedVersion, "second install should keep the pinned SHA")

	// The installed directory must NOT contain the second file.
	_, err = os.Stat(filepath.Join(proj, "addons", "myaddon", "second.txt"))
	require.True(t, os.IsNotExist(err), "installed addon must not contain file from second commit")

	// Update — must re-resolve to secondSHA.
	runGPM("update")

	lock, err = manifest.LoadLock(lockPath)
	require.NoError(t, err)
	require.Equal(t, secondSHA, lock.Addons["myaddon"].ResolvedVersion, "update should advance to the second SHA")

	// The installed directory must now contain the second file.
	_, err = os.Stat(filepath.Join(proj, "addons", "myaddon", "second.txt"))
	require.NoError(t, err, "updated addon must contain file from second commit")
}
