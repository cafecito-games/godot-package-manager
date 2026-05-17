package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/source"
	"github.com/stretchr/testify/require"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
}

func TestInstallExplicitSourcePath(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"addons/dlg/plugin.cfg": "[plugin]"})
	addonsDir := t.TempDir()

	err := Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{
		Name: "dlg", SourcePath: "addons/dlg",
	}, addonsDir)
	require.NoError(t, err)
	got, err := os.ReadFile(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "[plugin]", string(got))
}

func TestInstallAutoDetectsSingleAddonsDir(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"addons/dlg/plugin.cfg": "x"})
	addonsDir := t.TempDir()
	require.NoError(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))
	_, err := os.Stat(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
}

func TestInstallUsesRootWhenNoAddonsDir(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"plugin.cfg": "x"})
	addonsDir := t.TempDir()
	require.NoError(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))
	_, err := os.Stat(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
}

func TestInstallAmbiguousFails(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"addons/a/x": "1", "addons/b/y": "2"})
	err := Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, t.TempDir())
	require.Error(t, err)
	var installErr *output.InstallError
	require.ErrorAs(t, err, &installErr)
}

func TestInstallReturnsErrorWhenAddonsPathCannotBeRead(t *testing.T) {
	fetched := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(fetched, "addons"), []byte("not a directory"), 0o644))

	err := Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, t.TempDir())
	require.Error(t, err)
	var installErr *output.InstallError
	require.ErrorAs(t, err, &installErr)
	require.Contains(t, err.Error(), "reading addons directory")
}

func TestInstallBadExplicitSourcePath(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"addons/dlg/plugin.cfg": "[plugin]"})
	err := Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{
		Name: "dlg", SourcePath: "addons/does_not_exist",
	}, t.TempDir())
	require.Error(t, err)
	var installErr *output.InstallError
	require.ErrorAs(t, err, &installErr)
}

func TestInstallReplacesStaleFiles(t *testing.T) {
	addonsDir := t.TempDir()
	stale := filepath.Join(addonsDir, "dlg", "old.gd")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o755))
	require.NoError(t, os.WriteFile(stale, []byte("old"), 0o644))

	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"plugin.cfg": "new"})
	require.NoError(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))
	_, err := os.Stat(stale)
	require.True(t, os.IsNotExist(err))
}

func TestInstallRejectsEscapingInstallName(t *testing.T) {
	// Place a sentinel file in the parent of addonsDir to verify it survives.
	projectDir := t.TempDir()
	addonsDir := filepath.Join(projectDir, "addons")
	require.NoError(t, os.MkdirAll(addonsDir, 0o755))
	sentinelPath := filepath.Join(projectDir, "project.godot")
	require.NoError(t, os.WriteFile(sentinelPath, []byte("sentinel"), 0o644))

	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"plugin.cfg": "x"})

	spec := manifest.AddonSpec{Name: "dlg", InstallAs: ".."}
	err := Install(source.FetchResult{Dir: fetched}, spec, addonsDir)
	require.Error(t, err)
	var installErr *output.InstallError
	require.ErrorAs(t, err, &installErr)

	// Sentinel must still exist — the parent directory must not have been deleted.
	_, statErr := os.Stat(sentinelPath)
	require.NoError(t, statErr, "sentinel file was deleted; Install escaped addonsDir")
}

func TestInstallRollbackOnCopyFailure(t *testing.T) {
	addonsDir := t.TempDir()

	// First install: put a known file in place.
	firstFetched := t.TempDir()
	writeTree(t, firstFetched, map[string]string{"plugin.cfg": "original"})
	require.NoError(t, Install(source.FetchResult{Dir: firstFetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))
	originalFile := filepath.Join(addonsDir, "dlg", "plugin.cfg")
	content, err := os.ReadFile(originalFile)
	require.NoError(t, err)
	require.Equal(t, "original", string(content))

	// Second install: fetched tree contains a symlink, which copyTree must reject.
	badFetched := t.TempDir()
	writeTree(t, badFetched, map[string]string{"plugin.cfg": "new"})
	symlinkPath := filepath.Join(badFetched, "evil_link")
	require.NoError(t, os.Symlink("/etc/passwd", symlinkPath))

	err = Install(source.FetchResult{Dir: badFetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir)
	require.Error(t, err)
	var installErr *output.InstallError
	require.ErrorAs(t, err, &installErr)

	// Original addon must still be present (rollback worked).
	content, err = os.ReadFile(originalFile)
	require.NoError(t, err)
	require.Equal(t, "original", string(content), "rollback failed: original file was lost")
}

func TestInstallRejectsSymlink(t *testing.T) {
	addonsDir := t.TempDir()
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"plugin.cfg": "x"})
	symlinkPath := filepath.Join(fetched, "link_to_secret")
	require.NoError(t, os.Symlink("/etc/passwd", symlinkPath))

	err := Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir)
	require.Error(t, err)
	var installErr *output.InstallError
	require.ErrorAs(t, err, &installErr)
}

func TestInstallRecoversInterruptedBackup(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"plugin.cfg": "new"})
	addonsDir := t.TempDir()

	// Simulate a crash mid-swap: the destination is gone and only the
	// .gpm-backup copy survives.
	writeTree(t, filepath.Join(addonsDir, "dlg.gpm-backup"), map[string]string{"plugin.cfg": "old"})

	require.NoError(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))

	got, err := os.ReadFile(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "new", string(got))

	_, err = os.Stat(filepath.Join(addonsDir, "dlg.gpm-backup"))
	require.True(t, os.IsNotExist(err), "leftover backup should be consumed")
}

func TestInstallDiscardsStaleBackupWhenDestinationPresent(t *testing.T) {
	fetched := t.TempDir()
	writeTree(t, fetched, map[string]string{"plugin.cfg": "new"})
	addonsDir := t.TempDir()

	writeTree(t, filepath.Join(addonsDir, "dlg"), map[string]string{"plugin.cfg": "current"})
	writeTree(t, filepath.Join(addonsDir, "dlg.gpm-backup"), map[string]string{"plugin.cfg": "stale"})

	require.NoError(t, Install(source.FetchResult{Dir: fetched}, manifest.AddonSpec{Name: "dlg"}, addonsDir))

	got, err := os.ReadFile(filepath.Join(addonsDir, "dlg", "plugin.cfg"))
	require.NoError(t, err)
	require.Equal(t, "new", string(got))

	_, err = os.Stat(filepath.Join(addonsDir, "dlg.gpm-backup"))
	require.True(t, os.IsNotExist(err), "stale backup should be removed")
}
