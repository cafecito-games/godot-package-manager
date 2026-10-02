package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/slice"
	"github.com/spf13/cobra"
)

// lookupEnvironment is the seam through which the CLI reads the process
// environment, so tests drive GPM_HOST_ONLY without mutating the real one.
var lookupEnvironment = os.LookupEnv

// hostOnlyEnvironmentVariable is the one place the variable is named.
const hostOnlyEnvironmentVariable = "GPM_HOST_ONLY"

// allPlatformsFlagName and hostOnlyFlagName are the selection flags, named once
// so the registration and the explicitly-set check cannot drift apart.
const (
	allPlatformsFlagName = "all-platforms"
	hostOnlyFlagName     = "host-only"
)

// registerSelectionFlags adds the slice selection flags to a command that
// materializes addons. They are per-command rather than global for the same
// reason --dir is: they are meaningless for init, list, remove, add and
// assetlib.
func registerSelectionFlags(cmd *cobra.Command, allPlatforms, hostOnly *bool) {
	cmd.Flags().BoolVar(allPlatforms, allPlatformsFlagName, false,
		"install every slice an addon publishes, for projects that vendor addons/ into git")
	cmd.Flags().BoolVar(hostOnly, hostOnlyFlagName, false,
		"install only this machine's slices, ignoring the project's declared platforms "+
			"(also settable with "+hostOnlyEnvironmentVariable+")")
}

// resolveSelectionMode turns the --all-platforms and --host-only flags and the
// GPM_HOST_ONLY environment variable into one slice.SelectionMode. An explicit
// flag always wins over the environment; the two flags together are a usage
// error, because guessing which the caller meant would install the wrong set.
//
// It is pure apart from reading the environment once, so a command resolves it
// at the point it assembles the rest of its configuration and hands the result
// down. Nothing below internal/cli ever reads the environment.
func resolveSelectionMode(cmd *cobra.Command, opts *Options, allPlatforms, hostOnly bool) (slice.SelectionMode, error) {
	if allPlatforms && hostOnly {
		return slice.SelectDeclaredPlatforms, &UsageError{Err: fmt.Errorf(
			"--%s and --%s contradict each other; pass whichever one of the two sets you want installed",
			allPlatformsFlagName, hostOnlyFlagName)}
	}
	// An unset flag must not read as an explicit false, so --host-only=false is
	// a statement that overrides the environment while an absent flag is not.
	environmentValue, environmentFound := lookupEnvironment(hostOnlyEnvironmentVariable)
	environmentValue = strings.TrimSpace(environmentValue)
	if allPlatforms || cmd.Flags().Changed(hostOnlyFlagName) {
		if environmentFound && environmentValue != "" {
			verbosef(cmd, opts, "ignoring %s=%s: the command line is explicit\n",
				hostOnlyEnvironmentVariable, environmentValue)
		}
		switch {
		case hostOnly:
			return slice.SelectHostOnly, nil
		case allPlatforms:
			return slice.SelectAllPublishedSlices, nil
		default:
			return slice.SelectDeclaredPlatforms, nil
		}
	}
	// An unset or blank variable is absent, matching how the GitHub credentials
	// are read. A malformed one is not: a credential has no malformed form but a
	// boolean does, and reading GPM_HOST_ONLY=yes as false would hand an
	// unattended worktree bootstrap — the case the variable exists for — a full
	// multi-platform download with no signal that the setting was misspelled.
	if !environmentFound || environmentValue == "" {
		return slice.SelectDeclaredPlatforms, nil
	}
	parsed, err := strconv.ParseBool(environmentValue)
	if err != nil {
		return slice.SelectDeclaredPlatforms, &UsageError{Err: fmt.Errorf(
			"%s=%q is not a boolean; use one of true, false, 1 or 0",
			hostOnlyEnvironmentVariable, environmentValue)}
	}
	if parsed {
		return slice.SelectHostOnly, nil
	}
	return slice.SelectDeclaredPlatforms, nil
}
