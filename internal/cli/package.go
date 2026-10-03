package cli

import (
	"fmt"
	"path/filepath"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/packager"
	"github.com/spf13/cobra"
)

// newPackageCommand builds `gpm package`.
//
// Unlike every other subcommand it does not go through loadProject: `gpm
// package` runs in an addon author's own repository, which holds
// gpm-package.toml and no project.godot, so there is no Godot project to
// discover, no addons.toml to read, and no lockfile to write.
func newPackageCommand(opts *Options) *cobra.Command {
	var directory string
	var outputDirectory string
	var version string
	cmd := &cobra.Command{
		Use:   "package",
		Short: "Partition this addon into platform slice archives and an index",
		Long: "Read " + packager.ConfigFileName + " from an addon repository, partition the addon subtree " +
			"into a core slice plus one slice per platform, shared dependency artifacts when needed, and " +
			packager.IndexFileName + ". Publishing the artifacts remains the author's job.",
		Args: usageNoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			verbosef(cmd, opts, "config: %s\n", filepath.Join(directory, packager.ConfigFileName))
			result, err := packager.Package(packager.Options{
				Directory:       directory,
				OutputDirectory: outputDirectory,
				Version:         version,
			})
			if err != nil {
				return err
			}
			verbosef(cmd, opts, "index: %s\n", result.Index)
			return output.Render(cmd.OutOrStdout(), opts.JSON, result, func() {
				if opts.Quiet {
					return
				}
				writer := cmd.OutOrStdout()
				_, _ = fmt.Fprintf(writer, "Packaged %s %s into %d slices\n",
					result.Name, result.Version, len(result.Slices))
				for _, published := range result.Slices {
					_, _ = fmt.Fprintf(writer, "  %-16s %s  %d bytes\n", published.ID, published.File, published.Size)
				}
				for _, published := range result.Artifacts {
					_, _ = fmt.Fprintf(writer, "  shared:%-9s %s  %d bytes\n", published.ID, published.File, published.Size)
				}
				_, _ = fmt.Fprintf(writer, "Index: %s\n", result.Index)
			})
		},
	}
	cmd.Flags().StringVar(&directory, "dir", "",
		"addon repository directory holding "+packager.ConfigFileName+" (default: current directory)")
	cmd.Flags().StringVar(&outputDirectory, "out", packager.DefaultOutputDirectory,
		"directory to write slice archives, any shared-artifact archives, and "+packager.IndexFileName+" into")
	cmd.Flags().StringVar(&version, "version", "",
		"version to package, overriding [package] version")
	return cmd
}
