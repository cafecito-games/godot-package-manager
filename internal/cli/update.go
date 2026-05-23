package cli

import (
	"fmt"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/spf13/cobra"
)

// newUpdateCommand builds `gpm update [name...]`.
func newUpdateCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "update [addon...]",
		Short: "Re-resolve and reinstall addons, rewriting addons.lock",
		RunE: func(cmd *cobra.Command, args []string) error {
			discovered, addonManifest, err := loadProject(dir)
			if err != nil {
				return err
			}
			verbosef(cmd, opts, "project: %s\nmanifest: %s\nlockfile: %s\n", discovered.Root, discovered.ManifestPath, discovered.LockPath)
			for _, name := range args {
				if _, ok := addonManifest.Addons[name]; !ok {
					return &UsageError{Err: fmt.Errorf("unknown addon %q", name)}
				}
			}
			runner := NewRunner(discovered.AddonsDir, discovered.LockPath, limitsFor(opts))
			results, err := runner.InstallAddons(cmd.Context(), addonManifest, args, ModeUpdate)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts.JSON, results, func() {
				if opts.Quiet {
					return
				}
				for _, result := range results {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "updated %s @ %s\n", result.Name, result.ResolvedVersion)
				}
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}
