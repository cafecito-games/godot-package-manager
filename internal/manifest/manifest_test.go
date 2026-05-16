package manifest

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManifestRoundTrip(t *testing.T) {
	m := &Manifest{Addons: map[string]AddonSpec{
		"dialogue": {Source: SourceGit, URL: "https://example.com/d.git", Version: "v1.0", SourcePath: "addons/dialogue"},
		"thing":    {Source: SourceArchive, URL: "https://example.com/t.zip"},
	}}
	path := filepath.Join(t.TempDir(), "addons.toml")
	require.NoError(t, m.Save(path))

	loaded, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, "dialogue", loaded.Addons["dialogue"].Name)
	require.Equal(t, SourceGit, loaded.Addons["dialogue"].Source)
	require.Equal(t, "v1.0", loaded.Addons["dialogue"].Version)
	require.Equal(t, m.Addons["dialogue"].Hash(), loaded.Addons["dialogue"].Hash())
}

func TestInstallNameDefaultsToKey(t *testing.T) {
	require.Equal(t, "foo", AddonSpec{Name: "foo"}.InstallName())
	require.Equal(t, "bar", AddonSpec{Name: "foo", InstallAs: "bar"}.InstallName())
}

func TestHashChangesWithFields(t *testing.T) {
	a := AddonSpec{Source: SourceGit, URL: "u", Version: "v1"}
	b := a
	b.Version = "v2"
	require.NotEqual(t, a.Hash(), b.Hash())
}
