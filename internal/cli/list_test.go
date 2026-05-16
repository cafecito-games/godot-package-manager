package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
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
