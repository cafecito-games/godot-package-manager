package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/slice"
)

// withEnvironment drives GPM_HOST_ONLY through the package-level seam rather
// than through the real process environment, so the tests stay parallel-safe and
// never depend on how the suite was invoked.
func withEnvironment(t *testing.T, values map[string]string) {
	t.Helper()
	previous := lookupEnvironment
	lookupEnvironment = func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
	t.Cleanup(func() { lookupEnvironment = previous })
}

// selectionCommand builds a command carrying the selection flags and parses
// args against it, returning the flag values the command body would see.
func selectionCommand(t *testing.T, args ...string) (*cobra.Command, *bytes.Buffer, bool, bool) {
	t.Helper()
	var allPlatforms, hostOnly bool
	cmd := &cobra.Command{Use: "install"}
	registerSelectionFlags(cmd, &allPlatforms, &hostOnly)
	stderr := &bytes.Buffer{}
	cmd.SetErr(stderr)
	require.NoError(t, cmd.Flags().Parse(args))
	return cmd, stderr, allPlatforms, hostOnly
}

func resolveWith(t *testing.T, opts *Options, args ...string) (slice.SelectionMode, string, error) {
	t.Helper()
	cmd, stderr, allPlatforms, hostOnly := selectionCommand(t, args...)
	mode, err := resolveSelectionMode(cmd, opts, allPlatforms, hostOnly)
	return mode, stderr.String(), err
}

func TestResolveSelectionModeFromFlags(t *testing.T) {
	withEnvironment(t, nil)

	rows := map[string]struct {
		args     []string
		expected slice.SelectionMode
	}{
		"no flags":              {nil, slice.SelectDeclaredPlatforms},
		"--all-platforms":       {[]string{"--all-platforms"}, slice.SelectAllPublishedSlices},
		"--host-only":           {[]string{"--host-only"}, slice.SelectHostOnly},
		"--host-only=false":     {[]string{"--host-only=false"}, slice.SelectDeclaredPlatforms},
		"--all-platforms=false": {[]string{"--all-platforms=false"}, slice.SelectDeclaredPlatforms},
	}
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			mode, _, err := resolveWith(t, &Options{}, row.args...)
			require.NoError(t, err)
			require.Equal(t, row.expected, mode)
		})
	}
}

// TestResolveSelectionModeRejectsContradictoryFlags pins that gpm refuses to
// guess: the two modes contradict each other and installing the wrong set
// silently is worse than failing.
func TestResolveSelectionModeRejectsContradictoryFlags(t *testing.T) {
	withEnvironment(t, nil)
	_, _, err := resolveWith(t, &Options{}, "--host-only", "--all-platforms")
	require.Error(t, err)
	var usageError *UsageError
	require.True(t, errors.As(err, &usageError))
	require.Equal(t, output.ExitUsage, codeForError(err))
}

func TestResolveSelectionModeFromTheEnvironment(t *testing.T) {
	truthy := []string{"1", "t", "T", "true", "TRUE", "True"}
	for _, value := range truthy {
		t.Run("truthy "+value, func(t *testing.T) {
			withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: value})
			mode, _, err := resolveWith(t, &Options{})
			require.NoError(t, err)
			require.Equal(t, slice.SelectHostOnly, mode)
		})
	}
	falsy := []string{"0", "f", "F", "false", "FALSE", "False"}
	for _, value := range falsy {
		t.Run("falsy "+value, func(t *testing.T) {
			withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: value})
			mode, _, err := resolveWith(t, &Options{})
			require.NoError(t, err)
			require.Equal(t, slice.SelectDeclaredPlatforms, mode)
		})
	}
	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: "  true\n"})
		mode, _, err := resolveWith(t, &Options{})
		require.NoError(t, err)
		require.Equal(t, slice.SelectHostOnly, mode)
	})
	for name, values := range map[string]map[string]string{
		"unset":          nil,
		"empty":          {hostOnlyEnvironmentVariable: ""},
		"whitespace":     {hostOnlyEnvironmentVariable: "   "},
		"other variable": {"GITHUB_TOKEN": "irrelevant"},
	} {
		t.Run("absent: "+name, func(t *testing.T) {
			withEnvironment(t, values)
			mode, _, err := resolveWith(t, &Options{})
			require.NoError(t, err)
			require.Equal(t, slice.SelectDeclaredPlatforms, mode)
		})
	}
}

// TestResolveSelectionModeRejectsAMalformedEnvironmentValue pins that malformed
// input is never read as absent. Silently taking GPM_HOST_ONLY=yes for false
// would hand an unattended worktree bootstrap a full multi-platform download
// with no signal that the setting was misspelled.
func TestResolveSelectionModeRejectsAMalformedEnvironmentValue(t *testing.T) {
	for _, value := range []string{"yes", "on", "maybe", "host", "1 2"} {
		t.Run(value, func(t *testing.T) {
			withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: value})
			_, _, err := resolveWith(t, &Options{})
			require.Error(t, err)
			var usageError *UsageError
			require.True(t, errors.As(err, &usageError))
			require.Equal(t, output.ExitUsage, codeForError(err))
			require.Contains(t, err.Error(), hostOnlyEnvironmentVariable)
			require.Contains(t, err.Error(), value)
		})
	}
}

// TestResolveSelectionModeLetsAnExplicitFlagWin pins the precedence: an explicit
// flag is a stronger statement of intent than inherited process state, and the
// override is named under --verbose so it is not silent.
func TestResolveSelectionModeLetsAnExplicitFlagWin(t *testing.T) {
	rows := map[string]struct {
		args     []string
		expected slice.SelectionMode
	}{
		"--all-platforms over a truthy variable":   {[]string{"--all-platforms"}, slice.SelectAllPublishedSlices},
		"--host-only=false over a truthy variable": {[]string{"--host-only=false"}, slice.SelectDeclaredPlatforms},
		"--host-only over a falsy variable":        {[]string{"--host-only"}, slice.SelectHostOnly},
	}
	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			value := "true"
			if name == "--host-only over a falsy variable" {
				value = "false"
			}
			withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: value})
			mode, stderr, err := resolveWith(t, &Options{Verbose: true}, row.args...)
			require.NoError(t, err)
			require.Equal(t, row.expected, mode)
			require.Contains(t, stderr, hostOnlyEnvironmentVariable)
			require.Contains(t, stderr, "the command line is explicit")
		})
	}

	t.Run("the override diagnostic is silent without --verbose", func(t *testing.T) {
		withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: "true"})
		_, stderr, err := resolveWith(t, &Options{}, "--all-platforms")
		require.NoError(t, err)
		require.Empty(t, stderr)
	})
}

// TestResolveSelectionModeIgnoresAMalformedValueUnderAnExplicitFlag pins that
// the environment is not even parsed once the command line has spoken, so a
// stale variable in an agent container cannot fail an explicit invocation.
func TestResolveSelectionModeIgnoresAMalformedValueUnderAnExplicitFlag(t *testing.T) {
	withEnvironment(t, map[string]string{hostOnlyEnvironmentVariable: "yes"})
	mode, _, err := resolveWith(t, &Options{}, "--host-only")
	require.NoError(t, err)
	require.Equal(t, slice.SelectHostOnly, mode)
}

func TestInstallAndUpdateRegisterTheSelectionFlags(t *testing.T) {
	for name, build := range map[string]func(*Options) *cobra.Command{
		"install": newInstallCommand,
		"update":  newUpdateCommand,
	} {
		t.Run(name, func(t *testing.T) {
			cmd := build(&Options{})
			require.NotNil(t, cmd.Flags().Lookup(allPlatformsFlagName))
			require.NotNil(t, cmd.Flags().Lookup(hostOnlyFlagName))
			require.Contains(t, cmd.UsageString(), "--"+allPlatformsFlagName)
			require.Contains(t, cmd.UsageString(), "--"+hostOnlyFlagName)
			require.Contains(t, cmd.UsageString(), hostOnlyEnvironmentVariable)
		})
	}
}

// TestSelectionFlagsAreNotGlobal pins that the flags stay per-command: they are
// meaningless for init, list, remove, add and assetlib, which is the same reason
// --dir is not on Options.
func TestSelectionFlagsAreNotGlobal(t *testing.T) {
	root := newRootCommand(&Options{})
	require.Nil(t, root.PersistentFlags().Lookup(allPlatformsFlagName))
	require.Nil(t, root.PersistentFlags().Lookup(hostOnlyFlagName))
	for _, name := range []string{"init", "list", "remove", "add"} {
		for _, child := range root.Commands() {
			if child.Name() != name {
				continue
			}
			require.Nil(t, child.Flags().Lookup(allPlatformsFlagName), name)
			require.Nil(t, child.Flags().Lookup(hostOnlyFlagName), name)
		}
	}
}
