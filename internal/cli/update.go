package cli

import (
	"fmt"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/spf13/cobra"
)

// newUpdateCommand builds `gpm update [name...]`.
func newUpdateCommand(opts *Options) *cobra.Command {
	var dir string
	var allPlatforms, hostOnly bool
	cmd := &cobra.Command{
		Use:   "update [addon...]",
		Short: "Re-resolve and reinstall addons, rewriting addons.lock",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolved before project discovery so contradictory flags fail
			// without touching the filesystem or the network.
			selectionMode, err := resolveSelectionMode(cmd, opts, allPlatforms, hostOnly)
			if err != nil {
				return err
			}
			discovered, addonManifest, err := loadProject(dir)
			if err != nil {
				return err
			}
			verbosef(cmd, opts, "project: %s\nmanifest: %s\nlockfile: %s\nstate: %s\n",
				discovered.Root, discovered.ManifestPath, discovered.LockPath, discovered.StatePath)
			for _, name := range args {
				if _, ok := addonManifest.Addons[name]; !ok {
					return &UsageError{Err: fmt.Errorf("unknown addon %q", name)}
				}
			}
			runner := NewRunner(discovered.AddonsDir, discovered.LockPath, discovered.StatePath, limitsFor(opts), selectionMode)
			runner.Diagnosef = func(format string, args ...any) { verbosef(cmd, opts, format, args...) }
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
	registerSelectionFlags(cmd, &allPlatforms, &hostOnly)
	return cmd
}
