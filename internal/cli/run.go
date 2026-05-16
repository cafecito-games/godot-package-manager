package cli

import (
	"context"
	"os"

	"github.com/CafecitoGames/godot-addon-manager/internal/installer"
	"github.com/CafecitoGames/godot-addon-manager/internal/manifest"
	"github.com/CafecitoGames/godot-addon-manager/internal/source"
)

// AddonResult reports the outcome of installing a single addon.
type AddonResult struct {
	Name            string `json:"name"`
	ResolvedVersion string `json:"resolved_version"`
	InstallPath     string `json:"install_path"`
}

// Runner performs install orchestration. FetcherFor is injectable for testing;
// production code sets it to source.FetcherFor via NewRunner.
type Runner struct {
	AddonsDir  string
	LockPath   string
	FetcherFor func(manifest.AddonSpec) (source.Fetcher, error)
}

// NewRunner builds a Runner wired to the real source layer.
func NewRunner(addonsDir, lockPath string) *Runner {
	return &Runner{AddonsDir: addonsDir, LockPath: lockPath, FetcherFor: source.FetcherFor}
}

// InstallAddons fetches and installs the named addons (all addons when names is
// nil/empty), then writes addons.lock. It returns one AddonResult per addon.
func (r *Runner) InstallAddons(ctx context.Context, addonManifest *manifest.Manifest, names []string) ([]AddonResult, error) {
	lock, err := manifest.LoadLock(r.LockPath)
	if err != nil {
		return nil, err
	}
	targets := selectAddons(addonManifest, names)
	var results []AddonResult
	for _, spec := range targets {
		fetcher, err := r.FetcherFor(spec)
		if err != nil {
			return nil, err
		}
		fetched, err := fetcher.Fetch(ctx, spec)
		if err != nil {
			return nil, err
		}
		err = installer.Install(fetched, spec, r.AddonsDir)
		os.RemoveAll(fetched.Dir)
		if err != nil {
			return nil, err
		}
		lock.Addons[spec.Name] = manifest.LockEntry{
			ResolvedVersion: fetched.ResolvedVersion,
			SourcePath:      spec.SourcePath,
			Checksum:        fetched.Checksum,
			SpecHash:        spec.Hash(),
		}
		results = append(results, AddonResult{
			Name:            spec.Name,
			ResolvedVersion: fetched.ResolvedVersion,
			InstallPath:     spec.InstallName(),
		})
	}
	if err := lock.Save(r.LockPath); err != nil {
		return nil, err
	}
	return results, nil
}

// selectAddons returns the specs to operate on. An empty names slice selects
// all addons.
func selectAddons(addonManifest *manifest.Manifest, names []string) []manifest.AddonSpec {
	if len(names) == 0 {
		out := make([]manifest.AddonSpec, 0, len(addonManifest.Addons))
		for _, spec := range addonManifest.Addons {
			out = append(out, spec)
		}
		return out
	}
	var out []manifest.AddonSpec
	for _, name := range names {
		if spec, ok := addonManifest.Addons[name]; ok {
			out = append(out, spec)
		}
	}
	return out
}
