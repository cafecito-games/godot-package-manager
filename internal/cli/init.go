package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/project"
	"github.com/spf13/cobra"
)

const starterManifest = `# Godot addon manifest managed by gpm.
# Add addons with ` + "`gpm add`" + ` or by hand. Example:
#
# [addons.dialogue_manager]
# source      = "git"
# url         = "https://github.com/owner/dialogue.git"
# version     = "v2.1.0"
# source_path = "addons/dialogue_manager"

[addons]
`

// newInitCommand builds `gpm init`.
func newInitCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a starter addons.toml in the current directory",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				workingDir, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = workingDir
			}
			path := filepath.Join(dir, "addons.toml")
			if _, err := os.Stat(path); err == nil {
				return &output.ManifestError{Err: fmt.Errorf("%s already exists", path)}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := os.WriteFile(path, []byte(starterManifest), 0o644); err != nil {
				return &output.ManifestError{Err: err}
			}
			if err := ignoreStateFile(dir); err != nil {
				return err
			}
			verbosef(cmd, opts, "manifest: %s\n", path)
			if !opts.Quiet {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "directory to create addons.toml in (default: current directory)")
	return cmd
}

// ignoreStateFile makes sure .gitignore in dir lists the machine-local state
// file. The state file records which slices this particular disk holds, so
// committing it would publish one machine's layout to the whole team.
//
// An existing .gitignore is appended to rather than rewritten, and an entry
// that is already there is left alone, so running `gpm init` in a project that
// already ignores it changes nothing.
func ignoreStateFile(dir string) error {
	gitignorePath := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(gitignorePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &output.ManifestError{Err: fmt.Errorf("reading %s: %w", gitignorePath, err)}
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == project.StateFileName {
			return nil
		}
	}
	appended := string(existing)
	if appended != "" && !strings.HasSuffix(appended, "\n") {
		appended += "\n"
	}
	appended += project.StateFileName + "\n"
	if err := os.WriteFile(gitignorePath, []byte(appended), 0o644); err != nil {
		return &output.ManifestError{Err: fmt.Errorf("writing %s: %w", gitignorePath, err)}
	}
	return nil
}
