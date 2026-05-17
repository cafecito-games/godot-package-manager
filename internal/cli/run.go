package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/cafecito-games/godot-package-manager/internal/installer"
	"github.com/cafecito-games/godot-package-manager/internal/manifest"
	"github.com/cafecito-games/godot-package-manager/internal/output"
	"github.com/cafecito-games/godot-package-manager/internal/source"
)

// InstallMode controls whether the lockfile pins are honored.
type InstallMode int

const (
	// ModeInstall installs addons at their locked versions when the lock entry
	// is still consistent with addons.toml.
	ModeInstall InstallMode = iota
	// ModeUpdate ignores existing pins and re-resolves every target.
	ModeUpdate
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
// When mode is ModeInstall, existing lock entries that are consistent with the
// manifest are honored for reproducible installs. When mode is ModeUpdate,
// every target is re-resolved regardless of lock state.
func (r *Runner) InstallAddons(ctx context.Context, addonManifest *manifest.Manifest, names []string, mode InstallMode) ([]AddonResult, error) {
	lock, err := manifest.LoadLock(r.LockPath)
	if err != nil {
		return nil, err
	}
	targets := selectAddons(addonManifest, names)
	var results []AddonResult
	for _, spec := range targets {
		useLock := mode == ModeInstall && !manifest.NeedsResolve(spec, lock)

		effectiveSpec := spec
		if useLock && spec.Source == manifest.SourceGit {
			// Pin the git fetch to the exact locked commit SHA so the shallow
			// clone checks out the same revision that was originally resolved.
			effectiveSpec.Version = lock.Addons[spec.Name].ResolvedVersion
		}

		fetcher, err := r.FetcherFor(effectiveSpec)
		if err != nil {
			return nil, err
		}
		fetched, err := fetcher.Fetch(ctx, effectiveSpec)
		if err != nil {
			return nil, err
		}

		if useLock {
			entry := lock.Addons[spec.Name]
			if entry.Checksum != "" && fetched.Checksum != "" && entry.Checksum != fetched.Checksum {
				_ = os.RemoveAll(fetched.Dir)
				return nil, &output.FetchError{Err: fmt.Errorf(
					"addon %q: checksum mismatch (lock: %s, fetched: %s)",
					spec.Name, entry.Checksum, fetched.Checksum,
				)}
			}
		}

		err = installer.Install(fetched, spec, r.AddonsDir)
		_ = os.RemoveAll(fetched.Dir)
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
