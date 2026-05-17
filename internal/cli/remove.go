package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/spf13/cobra"
)

// newRemoveCommand builds `gpm remove <addon>`.
func newRemoveCommand(opts *Options) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "remove <addon>",
		Short: "Remove an addon from addons.toml, addons.lock, and disk",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			discovered, addonManifest, err := loadProject(dir)
			if err != nil {
				return err
			}
			spec, ok := addonManifest.Addons[name]
			if !ok {
				return &UsageError{Err: fmt.Errorf("unknown addon %q", name)}
			}
			if err := os.RemoveAll(filepath.Join(discovered.AddonsDir, spec.InstallName())); err != nil {
				return &output.InstallError{Err: err}
			}
			delete(addonManifest.Addons, name)
			if err := addonManifest.Save(discovered.ManifestPath); err != nil {
				return err
			}
			lock, err := manifest.LoadLock(discovered.LockPath)
			if err != nil {
				return err
			}
			if _, exists := lock.Addons[name]; exists {
				delete(lock.Addons, name)
				if err := lock.Save(discovered.LockPath); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "start directory for project discovery")
	return cmd
}
