package cli

import (
	"fmt"
	"os"

	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/output"
	"github.com/CafecitoGames/godot-addon-manager/internal/project"
	"github.com/spf13/cobra"
)

// loadProject discovers the Godot project from dir (or the current working
// directory) and loads its validated manifest.
func loadProject(dir string) (*project.Project, *manifest.Manifest, error) {
	if dir == "" {
		workingDir, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		dir = workingDir
	}
	discovered, err := project.Discover(dir)
	if err != nil {
		return nil, nil, err
	}
	addonManifest, err := manifest.Load(discovered.ManifestPath)
	if err != nil {
		return nil, nil, &output.ManifestError{Err: err}
	}
	if err := addonManifest.Validate(); err != nil {
		return nil, nil, err
	}
	return discovered, addonManifest, nil
}

// newInstallCommand builds `gam install`.
func newInstallCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install all addons declared in addons.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			discovered, addonManifest, err := loadProject(dir)
			if err != nil {
				return err
			}
			runner := NewRunner(discovered.AddonsDir, discovered.LockPath)
			results, err := runner.InstallAddons(cmd.Context(), addonManifest, nil)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts.JSON, results, func() {
				for _, result := range results {
					fmt.Fprintf(cmd.OutOrStdout(), "installed %s @ %s\n", result.Name, result.ResolvedVersion)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%d addon(s) installed\n", len(results))
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}
