package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/spf13/cobra"
)

// addonListing is the per-addon record emitted by `gpm list`.
type addonListing struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
}

// newListCommand builds `gpm list`.
func newListCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List addons declared in addons.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			discovered, addonManifest, err := loadProject(dir)
			if err != nil {
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
				listings = append(listings, addonListing{
					Name:      name,
					Source:    string(spec.Source),
					Version:   spec.Version,
					Installed: installed,
				})
			}
			return output.Render(cmd.OutOrStdout(), opts.JSON, listings, func() {
				for _, listing := range listings {
					mark := " "
					if listing.Installed {
						mark = "x"
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[%s] %-20s %-16s %s\n", mark, listing.Name, listing.Source, listing.Version)
				}
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}
