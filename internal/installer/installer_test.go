package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
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
