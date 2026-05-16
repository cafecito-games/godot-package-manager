package cli

import (
	"errors"
	"fmt"

	"github.com/cafecito-games/godot-addon-manager/internal/manifest"
	"github.com/cafecito-games/godot-addon-manager/internal/output"
	"github.com/cafecito-games/godot-addon-manager/internal/source"
	"github.com/cafecito-games/godot-addon-manager/internal/tui"
	"github.com/spf13/cobra"
)

// testFetcherFor, when non-nil, overrides the source layer in tests.
var testFetcherFor func(manifest.AddonSpec) (source.Fetcher, error)

// runTUI launches the interactive add wizard.
var runTUI = tui.RunAddWizard

// addFlags collects the flag values for `gam add`.
type addFlags struct {
	name, source, url, repo, version, asset, sourcePath, installAs, dir string
}

// newAddCommand builds `gam add`.
func newAddCommand(opts *Options) *cobra.Command {
	flagValues := &addFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add an addon to addons.toml and install it",
		RunE: func(cmd *cobra.Command, args []string) error {
			discovered, addonManifest, err := loadProject(flagValues.dir)
			if err != nil {
				return err
			}
			var spec manifest.AddonSpec
			// With neither identifying flag set, fall back to the interactive wizard.
			if flagValues.name == "" && flagValues.source == "" {
				spec, err = runTUI()
				if err != nil {
					return err
				}
			} else {
				spec, err = specFromFlags(flagValues)
				if err != nil {
					return err
				}
			}
			if _, exists := addonManifest.Addons[spec.Name]; exists {
				return &UsageError{Err: fmt.Errorf("addon %q already exists", spec.Name)}
			}
			single := &manifest.Manifest{Addons: map[string]manifest.AddonSpec{spec.Name: spec}}
			if err := single.Validate(); err != nil {
				var manifestErr *output.ManifestError
				if errors.As(err, &manifestErr) {
					return &UsageError{Err: manifestErr.Err}
				}
				return &UsageError{Err: err}
			}
			addonManifest.Addons[spec.Name] = spec
			if err := addonManifest.Save(discovered.ManifestPath); err != nil {
				return err
			}
			runner := NewRunner(discovered.AddonsDir, discovered.LockPath)
			if testFetcherFor != nil {
				runner.FetcherFor = testFetcherFor
			}
			results, err := runner.InstallAddons(cmd.Context(), addonManifest, []string{spec.Name}, ModeInstall)
			if err != nil {
				return err
			}
			return output.Render(cmd.OutOrStdout(), opts.JSON, results, func() {
				for _, result := range results {
					fmt.Fprintf(cmd.OutOrStdout(), "added and installed %s\n", result.Name)
				}
			})
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&flagValues.name, "name", "", "addon name (table key under [addons])")
	flags.StringVar(&flagValues.source, "source", "", "source type: git, github-release, archive")
	flags.StringVar(&flagValues.url, "url", "", "clone or archive URL")
	flags.StringVar(&flagValues.repo, "repo", "", "GitHub owner/repo (github-release)")
	flags.StringVar(&flagValues.version, "version", "", "git ref or release tag")
	flags.StringVar(&flagValues.asset, "asset", "", "release asset name/glob (github-release)")
	flags.StringVar(&flagValues.sourcePath, "source-path", "", "subdirectory within the source to install")
	flags.StringVar(&flagValues.installAs, "install-as", "", "install directory name (default: addon name)")
	flags.StringVar(&flagValues.dir, "dir", "", "start directory for project discovery")
	return cmd
}

// specFromFlags builds an AddonSpec from flag values.
func specFromFlags(flagValues *addFlags) (manifest.AddonSpec, error) {
	if flagValues.name == "" {
		return manifest.AddonSpec{}, &UsageError{Err: fmt.Errorf("--name is required")}
	}
	return manifest.AddonSpec{
		Name:       flagValues.name,
		Source:     manifest.SourceType(flagValues.source),
		URL:        flagValues.url,
		Repo:       flagValues.repo,
		Version:    flagValues.version,
		Asset:      flagValues.asset,
		SourcePath: flagValues.sourcePath,
		InstallAs:  flagValues.installAs,
	}, nil
}
