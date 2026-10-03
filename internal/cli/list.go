package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/spf13/cobra"
)

// addonListing is the per-addon record emitted by `gpm list`.
type addonListing struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`

	// Slices is what this machine has materialized, read from .gpm-state.toml.
	// It is never read from the lock's slices table, which records the set the
	// addon publishes rather than the set that is on this disk.
	Slices []string `json:"slices,omitempty"`
}

// newListCommand builds `gpm list`.
func newListCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List addons declared in addons.toml",
		Args:  usageNoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			discovered, addonManifest, err := loadProject(dir)
			if err != nil {
				return err
			}
			verbosef(cmd, opts, "project: %s\nmanifest: %s\nlockfile: %s\nstate: %s\n",
				discovered.Root, discovered.ManifestPath, discovered.LockPath, discovered.StatePath)
			state, err := manifest.LoadState(discovered.StatePath)
			var corrupt *manifest.CorruptStateError
			if errors.As(err, &corrupt) {
				// An unreadable machine-local cache is not a reason to fail a
				// read-only listing: the slice column is simply empty.
				verbosef(cmd, opts, "%v\n", corrupt)
			} else if err != nil {
				return err
			}
			names := make([]string, 0, len(addonManifest.Addons))
			for name := range addonManifest.Addons {
				names = append(names, name)
			}
			sort.Strings(names)
			listings := make([]addonListing, 0, len(names))
			for _, name := range names {
				spec := addonManifest.Addons[name]
				installed := false
				if info, statErr := os.Stat(filepath.Join(discovered.AddonsDir, spec.InstallName())); statErr == nil {
					installed = info.IsDir()
				}
				// Preserve the historical directory-presence answer for addons
				// without a file manifest (including manually placed addons), but
				// use the stronger answer whenever gpm has recorded one.
				if state.Addons[name].FileManifestVersion == manifest.CurrentFileManifestVersion {
					_, installed = installedFilesComplete(spec, state, discovered.AddonsDir)
				}
				listings = append(listings, addonListing{
					Name:      name,
					Source:    string(spec.Source),
					Version:   spec.Version,
					Installed: installed,
					Slices:    slices.Clone(state.Addons[name].Slices),
				})
			}
			return output.Render(cmd.OutOrStdout(), opts.JSON, listings, func() {
				if opts.Quiet {
					return
				}
				for _, listing := range listings {
					mark := " "
					if listing.Installed {
						mark = "x"
					}
					line := fmt.Sprintf("[%s] %-20s %-16s %s", mark, listing.Name, listing.Source, listing.Version)
					if len(listing.Slices) > 0 {
						line += "  " + strings.Join(listing.Slices, " ")
					}
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), line)
				}
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}
