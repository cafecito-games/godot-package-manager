package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
)

// packageExtension is a .gdextension with one platform binary, enough to produce
// a core slice and one platform slice.
const packageExtension = `[configuration]

entry_symbol = "addon_main"

[libraries]

linux.template_release.x86_64 = "bin/addon_linux.so"
`

// writePackageAddon lays out an addon repository `gpm package` can run in. It
// holds no project.godot, because an addon repository does not have one.
func writePackageAddon(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "gpm-package.toml"), []byte(`
[package]
name       = "addon"
addon_path = "addons/addon"
version    = "1.2.3"
`), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "addons", "addon", "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "addons", "addon", "addon.gdextension"), []byte(packageExtension), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "addons", "addon", "plugin.gd"), []byte("extends Node\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "addons", "addon", "bin", "addon_linux.so"), []byte("linux"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.godot.not-here"), []byte(""), 0o644))
	return root
}

func TestPackageIsRegisteredOnTheRootCommand(t *testing.T) {
	root := newRootCommand(&Options{})

	var found bool
	for _, command := range root.Commands() {
		if command.Name() == "package" {
			found = true
			require.NotNil(t, command.Flags().Lookup("dir"))
			require.NotNil(t, command.Flags().Lookup("out"))
			require.NotNil(t, command.Flags().Lookup("version"))
			require.Equal(t, "dist", command.Flags().Lookup("out").DefValue)
		}
	}
	require.True(t, found, "gpm package is not registered")
}

func TestPackageWritesArchivesAndTheIndex(t *testing.T) {
	addonRepository := writePackageAddon(t)
	outputDirectory := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Execute([]string{"package", "--dir", addonRepository, "--out", outputDirectory}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())

	require.FileExists(t, filepath.Join(outputDirectory, "gpm-index.toml"))
	require.FileExists(t, filepath.Join(outputDirectory, "addon-1.2.3-core.zip"))
	require.FileExists(t, filepath.Join(outputDirectory, "addon-1.2.3-linux.x86_64.zip"))
	require.Contains(t, stdout.String(), "addon-1.2.3-linux.x86_64.zip")
	require.Contains(t, stdout.String(), "gpm-index.toml")
}

func TestPackageJSONEmitsEverySliceWithItsFileSizeAndChecksum(t *testing.T) {
	addonRepository := writePackageAddon(t)
	outputDirectory := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Execute([]string{"package", "--dir", addonRepository, "--out", outputDirectory, "--json"}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())

	var payload struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Index   string `json:"index"`
		Slices  []struct {
			ID     string `json:"id"`
			File   string `json:"file"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"slices"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &payload))
	require.Equal(t, "addon", payload.Name)
	require.Equal(t, "1.2.3", payload.Version)
	require.Equal(t, filepath.Join(outputDirectory, "gpm-index.toml"), payload.Index)
	require.Len(t, payload.Slices, 2)
	require.Equal(t, "core", payload.Slices[0].ID)
	require.Equal(t, "linux.x86_64", payload.Slices[1].ID)
	for _, published := range payload.Slices {
		require.NotEmpty(t, published.File)
		require.Greater(t, published.Size, int64(0))
		require.Len(t, published.SHA256, 64)
	}
}

func TestPackageQuietSuppressesTextOutput(t *testing.T) {
	addonRepository := writePackageAddon(t)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Execute([]string{"package", "--dir", addonRepository, "--out", t.TempDir(), "--quiet"}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())
	require.Empty(t, stdout.String())
}

func TestPackageVerboseDiagnosticsGoToStderr(t *testing.T) {
	addonRepository := writePackageAddon(t)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Execute([]string{"package", "--dir", addonRepository, "--out", t.TempDir(), "--verbose"}, stdout, stderr)
	require.Equal(t, output.ExitOK, code, stderr.String())
	require.Contains(t, stderr.String(), "gpm-package.toml")
	require.NotContains(t, stdout.String(), "gpm-package.toml")
}

func TestPackageVersionFlagOverridesTheConfiguredVersion(t *testing.T) {
	addonRepository := writePackageAddon(t)
	outputDirectory := t.TempDir()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Execute(
		[]string{"package", "--dir", addonRepository, "--out", outputDirectory, "--version", "2.0.0"},
		stdout, stderr,
	)
	require.Equal(t, output.ExitOK, code, stderr.String())
	require.FileExists(t, filepath.Join(outputDirectory, "addon-2.0.0-core.zip"))
}

func TestPackageReportsAMissingConfigWithTheManifestExitCode(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Execute([]string{"package", "--dir", t.TempDir()}, stdout, stderr)
	require.Equal(t, output.ExitManifest, code)
	require.Contains(t, stderr.String(), "gpm-package.toml")
}

func TestPackageRejectsPositionalArgumentsAsAUsageError(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Execute([]string{"package", "unexpected"}, stdout, stderr)
	require.Equal(t, output.ExitUsage, code)
}
