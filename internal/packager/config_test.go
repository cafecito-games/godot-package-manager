package packager_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/packager"
)

func TestLoadConfigReadsThePackageTable(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, packager.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte(`
[package]
name       = "limboai"
addon_path = "addons/limboai"
version    = "1.4.0"

[package.slices]
"android.arm64" = ["android/limboai.aar"]
`), 0o644))

	config, err := packager.LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "limboai", config.Package.Name)
	require.Equal(t, "addons/limboai", config.Package.AddonPath)
	require.Equal(t, "1.4.0", config.Package.Version)
	require.Equal(t, map[string][]string{"android.arm64": {"android/limboai.aar"}}, config.Package.Slices)
}

func TestLoadConfigRejectsAMissingFileAsAManifestError(t *testing.T) {
	path := filepath.Join(t.TempDir(), packager.ConfigFileName)

	config, err := packager.LoadConfig(path)
	require.Nil(t, config)
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.ErrorAs(t, err, &manifestError)
	require.Equal(t, output.ExitManifest, output.CodeFor(err))
	require.Contains(t, err.Error(), path)
}

func TestLoadConfigRejectsAnUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), packager.ConfigFileName)
	require.NoError(t, os.WriteFile(path, []byte(`
[package]
name        = "limboai"
addon_path  = "addons/limboai"
version     = "1.4.0"
addon_paths = "addons/limboai"
`), 0o644))

	_, err := packager.LoadConfig(path)
	require.Error(t, err)
	var manifestError *output.ManifestError
	require.True(t, errors.As(err, &manifestError))
	require.Contains(t, err.Error(), "addon_paths")
}

func TestLoadConfigRejectsAKeyThatDiffersOnlyInCase(t *testing.T) {
	cases := []struct {
		name     string
		document string
		wantText string
	}{
		{
			name:     "table name",
			document: "[Package]\nname = \"addon\"\naddon_path = \"addons/addon\"\nversion = \"1.0.0\"\n",
			wantText: "Package",
		},
		{
			name:     "field name",
			document: "[package]\nName = \"addon\"\naddon_path = \"addons/addon\"\nversion = \"1.0.0\"\n",
			wantText: "Name",
		},
		{
			name:     "nested table name",
			document: "[package]\nname = \"addon\"\naddon_path = \"addons/addon\"\nversion = \"1.0.0\"\n\n[package.Slices]\n\"ios.arm64\" = [\"a\"]\n",
			wantText: "Slices",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), packager.ConfigFileName)
			require.NoError(t, os.WriteFile(path, []byte(testCase.document), 0o644))

			_, err := packager.LoadConfig(path)
			require.Error(t, err)
			var manifestError *output.ManifestError
			require.ErrorAs(t, err, &manifestError)
			require.Contains(t, err.Error(), testCase.wantText)
		})
	}
}
