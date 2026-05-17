package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/stretchr/testify/require"
)

func TestRootHelpListsGlobalFlags(t *testing.T) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), "gpm")
	require.Contains(t, out.String(), "--json")
	require.Contains(t, out.String(), "--verbose")
	require.Contains(t, out.String(), "--quiet")
}

func TestRootHelpListsCompletion(t *testing.T) {
	cmd := NewRootCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), "completion")
}

func TestExecuteRendersJSONError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"--json", "install", "--dir", filepath.Join(t.TempDir(), "missing")}, &stdout, &stderr)

	require.Equal(t, output.ExitManifest, code)
	require.Empty(t, stderr.String())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &payload))
	require.Contains(t, payload["error"], "no project.godot found")
	require.Equal(t, float64(output.ExitManifest), payload["code"])
}

func TestExecuteMapsCobraArgumentErrorToUsageExit(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Execute([]string{"remove"}, &stdout, &stderr)

	require.Equal(t, output.ExitUsage, code)
	require.Empty(t, stdout.String())
	require.Contains(t, stderr.String(), "accepts 1 arg(s), received 0")
}

func TestNoArgCommandsRejectUnexpectedArguments(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "project.godot"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "addons.toml"), []byte("[addons]\n"), 0o644))

	for _, args := range [][]string{
		{"install", "unexpected", "--dir", dir},
		{"list", "unexpected", "--dir", dir},
		{"init", "unexpected", "--dir", dir},
		{"add", "unexpected", "--dir", dir},
	} {
		t.Run(args[0], func(t *testing.T) {
			cmd := NewRootCommand()
			cmd.SetArgs(args)
			err := cmd.Execute()
			require.Error(t, err)
			var usageErr *UsageError
			require.ErrorAs(t, err, &usageErr)
		})
	}
}
