package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/project"
)

const listAddonsToml = `[addons]
[addons.my-addon]
source = "archive"
url = "https://example.com/my-addon.zip"
version = "1.0.0"
`

func TestListCommand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte(listAddonsToml), 0o644))

	// Pre-create the addon directory so it shows as installed.
	addonDir := filepath.Join(dir, "addons", "my-addon")
	require.NoError(t, os.MkdirAll(addonDir, 0o755))

	var buf bytes.Buffer
	cmd := newListCommand(&Options{})
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--dir", dir})
	require.NoError(t, cmd.Execute())

	output := buf.String()
	require.Contains(t, output, "my-addon")
	require.Contains(t, output, "[x]")
}

func TestListCommandJSON(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte(listAddonsToml), 0o644))

	// Pre-create the addon directory so it shows as installed.
	addonDir := filepath.Join(dir, "addons", "my-addon")
	require.NoError(t, os.MkdirAll(addonDir, 0o755))

	var buf bytes.Buffer
	cmd := newListCommand(&Options{JSON: true})
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--dir", dir})
	require.NoError(t, cmd.Execute())

	var listings []map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &listings))
	require.Len(t, listings, 1)

	listing := listings[0]
	require.Equal(t, "my-addon", listing["name"])
	require.Equal(t, true, listing["installed"])
}

func TestListReportsAnAddonWithADeletedRecordedFileAsNotInstalled(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")
	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--dir", projectRoot))
	require.NoError(t, os.Remove(filepath.Join(
		projectRoot, "addons", slicedAddonName, "scripts", "sliced_tool.gd")))

	stdout := &bytes.Buffer{}
	require.NoError(t, executeGPM(t, stdout, io.Discard, "list", "--json", "--dir", projectRoot))
	var listings []addonListing
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &listings))
	require.Len(t, listings, 1)
	require.False(t, listings[0].Installed)
}

// TestListReportsInstalledSlicesFromState pins that `gpm list` reports what is
// on this disk. It never falls back to the lock's slices table, which is the
// published set and not what was materialized.
func TestListReportsInstalledSlicesFromState(t *testing.T) {
	withEnvironment(t, nil)
	withHost(t, hostA)
	publisher := servePublisher(t, packSlicedFixture(t, "one"))
	projectRoot := newSlicedProject(t, publisher, "ios.arm64")
	require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--host-only", "--dir", projectRoot))

	listSlices := func(t *testing.T, args ...string) ([]string, string) {
		t.Helper()
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		require.NoError(t, executeGPM(t, stdout, stderr,
			append([]string{"list", "--json", "--dir", projectRoot}, args...)...))
		var listings []struct {
			Name   string   `json:"name"`
			Slices []string `json:"slices"`
		}
		require.NoError(t, json.Unmarshal(stdout.Bytes(), &listings))
		require.Len(t, listings, 1)
		return listings[0].Slices, stderr.String()
	}

	slices, _ := listSlices(t)
	require.Equal(t, []string{"core", "macos"}, slices,
		"after a host-only install the listing is core plus the host slice alone")

	statePath := filepath.Join(projectRoot, project.StateFileName)
	require.NoError(t, os.Remove(statePath))
	slices, _ = listSlices(t)
	require.Empty(t, slices, "an absent state file reports an empty slice list")

	require.NoError(t, os.WriteFile(statePath, []byte("not = = toml ["), 0o644))
	slices, _ = listSlices(t)
	require.Empty(t, slices, "an unparseable state file reports an empty slice list and still exits 0")

	t.Run("the text branch names the slices and respects --quiet", func(t *testing.T) {
		require.NoError(t, executeGPM(t, io.Discard, io.Discard, "install", "--host-only", "--dir", projectRoot))
		stdout := &bytes.Buffer{}
		require.NoError(t, executeGPM(t, stdout, io.Discard, "list", "--dir", projectRoot))
		require.Contains(t, stdout.String(), "core macos")

		quiet := &bytes.Buffer{}
		require.NoError(t, executeGPM(t, quiet, io.Discard, "list", "--quiet", "--dir", projectRoot))
		require.Empty(t, quiet.String())
	})

	t.Run("an unparseable state file is reported under --verbose", func(t *testing.T) {
		require.NoError(t, os.WriteFile(statePath, []byte("not = = toml ["), 0o644))
		stderr := &bytes.Buffer{}
		require.NoError(t, executeGPM(t, io.Discard, stderr, "list", "--verbose", "--dir", projectRoot))
		require.Contains(t, stderr.String(), "ignoring unparseable state file")
	})
}
